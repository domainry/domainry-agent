package agent

import (
	"context"
	"database/sql"
	"math"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) contractPublicationRecord(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	var out persistence.ConversationContractPublicationRecord
	if revision < 0 || revision == math.MaxInt64 {
		return out, conversationError("bad_request", "agreement_revision_invalid")
	}
	if revision == 0 {
		revision = max(1, d.AgreementRevision)
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(
		delegationHistoryPredicate(conversationOwner(delegationRecordAuthority(d, a)), conversationItemDelegationAgreement, d.ID, ""),
		query.Equal("seq", revision),
	)).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = db.QueryRowContext(ctx, q, args...).Scan(&raw); err == sql.ErrNoRows {
		return out, conversationError("forbidden", "contract_sources_unverified")
	} else if err != nil {
		return out, err
	}
	if err = unmarshalDurableJSON(raw, &out.Agreement); err != nil {
		return out, err
	}
	if out.Agreement.Revision != revision {
		return out, conversationError("forbidden", "contract_sources_unverified")
	}
	if out.Agreement.Requirements == nil {
		return out, conversationError("forbidden", "contract_sources_unverified")
	}
	out.Requirements = *out.Agreement.Requirements
	return out, nil
}

func (s *ConversationStore) ConversationContractPublicationRecord(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (persistence.ConversationContractPublicationRecord, error) {
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return persistence.ConversationContractPublicationRecord{}, err
	}
	return s.contractPublicationRecord(ctx, s.store.Database(), d, revision, a)
}

func (s *ConversationStore) republishContract(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	if in.ContractPublication == nil || in.Publication != nil || in.ToolRequest != nil || len(in.ContractPublication.RecordDigest) != 64 || in.Delivery != nil || in.Brief != nil || in.Review != nil || in.Disagreement != nil || in.Transfer != nil || in.Inspection != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Participants != nil {
		return d, conversationError("bad_request", "contract_publication_invalid")
	}
	subjects, _, err := s.delegationSubjects(ctx, tx, d.ID, a)
	if err != nil {
		return d, err
	}
	if conversationOwner(a) != conversationOwner(subjects.source) || d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
		return d, conversationError("forbidden", "delegation_actor_invalid")
	}
	if d.Revision != in.ExpectedRevision {
		return d, conversationError("conflict", "revision_conflict")
	}
	original, err := s.contractPublicationRecord(ctx, tx, d, in.ContractPublication.AgreementRevision, a)
	if err != nil {
		return d, err
	}
	if conversationHash(original) != in.ContractPublication.RecordDigest {
		return d, conversationError("conflict", "contract_record_changed")
	}
	for _, reader := range []sdk.ConversationAuthority{a, subjects.execution} {
		if _, err = s.flattenTaskDependencies(ctx, tx, original.Agreement.Dependencies, reader); err != nil {
			return d, err
		}
	}
	// Upstream publications keep their own publisher and current audience.
	// Republishing this agreement does not publish an upstream private source.
	direct := original
	direct.Agreement.Dependencies = nil
	if err = s.saveSourceReleases(ctx, tx, d.ID, "contract", a, subjects.execution, direct.SourceReferences()); err != nil {
		return d, err
	}
	d.Revision++
	d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err = s.saveConversationDelegation(ctx, tx, d, in.ExpectedRevision, a); err != nil {
		return d, err
	}
	// Store the original binding and the actual publication actor independently.
	entry := sdk.ConversationContractPublicationReceipt{ConversationContractPublication: *in.ContractPublication, Revision: d.Revision, Publisher: a, RecipientUserID: subjects.execution.UserID, PublishedAt: d.UpdatedAt, Reason: in.Reason}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationContractPublicationTable).Columns("owner_key", "delegation_id", "revision", "payload_json").Values(conversationOwner(delegationRecordAuthority(d, a)), d.ID, entry.Revision, conversationJSON(entry)).Build()
	return d, conversationExec(ctx, tx, q, args, err)
}

func (s *ConversationStore) ConversationContractPublicationHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationHistory, error) {
	out := sdk.ConversationContractPublicationHistory{Items: []sdk.ConversationContractPublicationReceipt{}, Complete: true}
	if before < 0 || before == math.MaxInt64 {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	d, err := s.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	p := query.And(query.Equal("owner_key", conversationOwner(delegationRecordAuthority(d, a))), query.Equal("delegation_id", d.ID))
	if before > 0 {
		p = query.And(p, query.LessThan("revision", before))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationContractPublicationTable).Columns("payload_json").Where(p).OrderBy(query.Descending("revision")).Limit(21).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item sdk.ConversationContractPublicationReceipt
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = unmarshalDurableJSON(raw, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	if len(out.Items) > 20 {
		out.Complete = false
		out.Items = out.Items[:20]
		out.NextBefore = out.Items[19].Revision
	}
	return out, rows.Err()
}

var _ persistence.ConversationContractPublicationRepository = (*ConversationStore)(nil)
