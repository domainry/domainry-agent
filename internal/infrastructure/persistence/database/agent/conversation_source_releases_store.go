package agent

import (
	"context"
	"database/sql"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) saveSourceReleases(ctx context.Context, tx *sql.Tx, id, purpose string, producer, reader sdk.ConversationAuthority, refs []sdk.ConversationRunReference) error {
	for _, ref := range refs {
		if ref.ConversationID == "" || ref.RunID == "" || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
			return conversationError("bad_request", "source_reference_invalid")
		}
		run, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, producer)
		if err != nil {
			return err
		}
		// Storage ownership is user-scoped. A publication's proof identity
		// comes from the immutable run, including its original selected role,
		// even when another role of that user submits the reference.
		original := run.Authority
		if conversationAuthority(original) != nil || conversationOwner(original) != conversationOwner(producer) {
			return conversationError("forbidden", "execution_subject_mismatch")
		}
		// Delivery submissions record their actual publisher even when the
		// original provider and recipient have the same selected role. A later
		// role still needs this proof and current sharing/read authorization;
		// ordinary exact-role access does not depend on the publication.
		if original == reader && purpose != "delivery" {
			continue
		}
		publisher := producer
		release := persistence.ConversationSourceRelease{DelegationID: id, Purpose: purpose, Reference: ref, Producer: original, Publisher: &publisher}
		key := conversationHash(release)
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(reader)), query.Equal("release_id", key))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err == nil {
			continue
		} else if err != sql.ErrNoRows {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("owner_key", "release_id", "producer_key", "delegation_id", "conversation_id", "run_id", "before_step", "payload_json").Values(conversationOwner(reader), key, conversationOwner(producer), id, ref.ConversationID, ref.RunID, ref.BeforeStep, conversationJSON(release)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) saveDelegationContractReleases(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	subjects, _, err := s.delegationSubjects(ctx, tx, d.ID, a)
	if err != nil {
		return err
	}
	refs := append([]sdk.ConversationRunReference{}, d.Requirements.Sources...)
	if d.BriefSource != nil {
		refs = append(refs, *d.BriefSource)
	}
	if d.InputSource != nil {
		refs = append(refs, *d.InputSource)
	}
	// The publishing role is the actual submitting role, not the role frozen
	// when the relationship was first admitted.
	if conversationOwner(a) != conversationOwner(subjects.source) {
		return conversationError("forbidden", "execution_subject_mismatch")
	}
	if err := s.saveSourceReleases(ctx, tx, d.ID, "contract", a, subjects.execution, refs); err != nil {
		return err
	}
	for _, grant := range d.Participants {
		if grant.Revision < 1 {
			continue
		}
		reader := a
		reader.UserID, reader.RoleKey = grant.UserID, ""
		if err := s.saveSourceReleases(ctx, tx, d.ID, "contract", a, reader, refs); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) saveDelegationDeliveryReleases(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	subjects, _, err := s.delegationSubjects(ctx, tx, d.ID, a)
	if err != nil || d.Delivery == nil {
		return err
	}
	if conversationOwner(a) != conversationOwner(subjects.execution) {
		return conversationError("forbidden", "execution_subject_mismatch")
	}
	// A legacy/same-identity delegation has no separate subject row; the
	// caller-scoped fallback still publishes older roles owned by that user.
	// saveSourceReleases resolves each immutable run's original authority.
	refs := append([]sdk.ConversationRunReference{}, d.Delivery.Evidence...)
	for _, claim := range d.Delivery.Conditions {
		for _, ref := range claim.Receipts {
			refs = append(refs, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	if d.Verification != nil && d.Verification.Source != nil && d.Verification.ActorID == subjects.execution.UserID {
		refs = append(refs, *d.Verification.Source)
	}
	if err := s.saveSourceReleases(ctx, tx, d.ID, "delivery", a, subjects.source, refs); err != nil {
		return err
	}
	return s.publishDeliveryToParticipants(ctx, tx, d, a, refs)
}

func (s *ConversationStore) ConversationSourceReleases(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	prefix := query.Equal("before_step", 0)
	if ref.BeforeStep > 0 {
		prefix = query.Or(prefix, query.GreaterThanOrEqual("before_step", ref.BeforeStep))
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("conversation_id", ref.ConversationID), query.Equal("run_id", ref.RunID), prefix)).OrderBy(query.Ascending("release_id")).Limit(33).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []persistence.ConversationSourceRelease{}
	for rows.Next() {
		var raw []byte
		var item persistence.ConversationSourceRelease
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) > 32 {
		return nil, conversationError("unavailable", "source_limit_exceeded")
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return s.restoreLegacySourcePublishers(ctx, items, a)
}

var _ persistence.ConversationSourceReleaseRepository = (*ConversationStore)(nil)
