package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) deliveryPublicationRecord(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, revision int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryRecord, error) {
	var out sdk.ConversationDeliveryRecord
	if revision < 0 || revision == math.MaxInt64 {
		return out, conversationError("bad_request", "delivery_revision_invalid")
	}
	if revision == 0 {
		if d.Delivery == nil {
			return out, conversationError("not_found", "delivery_record_not_found")
		}
		out.Kind, out.Delivery = "current", *d.Delivery
		if d.Verification != nil {
			out.Verification = *d.Verification
		}
		return out, nil
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(
		delegationHistoryPredicate(conversationOwner(delegationRecordAuthority(d, a)), conversationItemDelegationDelivery, d.ID, ""),
		query.Equal("seq", revision),
	)).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); err == sql.ErrNoRows {
		return out, conversationError("not_found", "delivery_record_not_found")
	} else if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *ConversationStore) ConversationDeliveryPublicationRecord(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryRecord, error) {
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return sdk.ConversationDeliveryRecord{}, err
	}
	return s.deliveryPublicationRecord(ctx, s.store.Database(), d, revision, a)
}

// Publication changes only relationship revision/audit and precise grants.
// The canonical delivery, assessment, accepted status and task stay untouched.
func (s *ConversationStore) republishDelivery(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if in.Publication == nil || in.ContractPublication != nil || in.ToolRequest != nil || in.Publication.RecordDigest == "" || in.Delivery != nil || in.Brief != nil || in.Review != nil || in.Disagreement != nil || in.Transfer != nil || in.Inspection != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Participants != nil {
		return d, conversationError("bad_request", "delivery_publication_invalid")
	}
	subjects, _, err := s.delegationSubjects(ctx, tx, d.ID, a)
	if err != nil {
		return d, err
	}
	if conversationOwner(a) != conversationOwner(subjects.execution) {
		return d, conversationError("forbidden", "delegation_actor_invalid")
	}
	if d.Revision != in.ExpectedRevision {
		return d, conversationError("conflict", "revision_conflict")
	}
	original, err := s.deliveryPublicationRecord(ctx, tx, d, in.Publication.DeliveryRevision, a)
	if err != nil {
		return d, err
	}
	if conversationHash(original) != in.Publication.RecordDigest {
		return d, conversationError("conflict", "delivery_record_changed")
	}
	refs := append([]sdk.ConversationRunReference{}, original.Delivery.Evidence...)
	for _, condition := range original.Delivery.Conditions {
		for _, ref := range condition.Receipts {
			refs = append(refs, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	for _, check := range original.Verification.Checks {
		for _, ref := range check.Receipts {
			refs = append(refs, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	if original.Verification.Source != nil && original.Verification.ActorID == a.UserID {
		refs = append(refs, *original.Verification.Source)
	}
	if err = s.saveSourceReleases(ctx, tx, d.ID, "delivery", a, subjects.source, refs); err != nil {
		return d, err
	}
	if err = s.publishDeliveryToParticipants(ctx, tx, d, a, refs); err != nil {
		return d, err
	}
	d.Revision++
	d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err = s.saveConversationDelegation(ctx, tx, d, in.ExpectedRevision, a); err != nil {
		return d, err
	}
	entry := sdk.ConversationDeliveryRecord{Revision: d.Revision, Kind: "republish_delivery", Delivery: original.Delivery, Verification: original.Verification, Reason: in.Reason, Publication: &sdk.ConversationDeliveryPublicationReceipt{ConversationDeliveryPublication: *in.Publication, Publisher: a, RecipientUserID: subjects.source.UserID, PublishedAt: d.UpdatedAt}}
	return d, s.insertDelegationHistory(ctx, tx, conversationItemDelegationDelivery, d, "", entry.Revision, entry.Verification.Source, entry, a)
}

var _ persistence.ConversationDeliveryPublicationRepository = (*ConversationStore)(nil)
