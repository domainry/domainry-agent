package agent

import (
	"context"
	"database/sql"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) transferDelegationActor(ctx context.Context, db conversationDB, id string, actor sdk.ConversationAuthority) (sdk.ConversationDelegation, sdk.ConversationAuthority, error) {
	d, record, err := s.participantDelegation(ctx, db, id, actor, "manage")
	if err == nil && d.OwnerUserID != actor.UserID && !participantManagement(d, actor) {
		err = conversationError("forbidden", "delegation_actor_invalid")
	}
	return d, record, err
}

// A manager may use an already shared agreement, but cannot publish the
// issuer's private roots on the issuer's behalf. Keep the original release and
// verify the new reader's exact audience again inside the transfer transaction.
func (s *ConversationStore) validateTransferContractPublications(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, reader sdk.ConversationAuthority) error {
	refs := append([]sdk.ConversationRunReference{}, d.Requirements.Sources...)
	for _, ref := range []*sdk.ConversationRunReference{d.BriefSource, d.InputSource} {
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	owner := delegationRecordAuthority(d, reader)
	for _, ref := range refs {
		original, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, owner)
		if err != nil {
			return err
		}
		if original.Authority == reader {
			continue
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(reader)), query.Equal("delegation_id", d.ID), query.Equal("conversation_id", ref.ConversationID), query.Equal("run_id", ref.RunID), query.Equal("before_step", ref.BeforeStep))).Limit(33).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		verified, count := false, 0
		for rows.Next() {
			count++
			var raw []byte
			var release persistence.ConversationSourceRelease
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal(raw, &release)
			}
			if err != nil {
				break
			}
			publisher := release.Publisher
			verified = verified || release.DelegationID == d.ID && release.Purpose == "contract" && release.Reference == ref && release.Producer == original.Authority && publisher != nil && conversationAuthority(*publisher) == nil && publisher.RoleKey != "" && conversationOwner(*publisher) == conversationOwner(owner)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if !verified || count > 32 {
			return conversationError("forbidden", "delegation_contract_source_unavailable")
		}
	}
	return nil
}

func (s *ConversationStore) transferDelegationSubjects(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, previous conversationDelegationSubjects, executor sdk.ConversationAuthority) error {
	_, found, err := s.delegationSubjects(ctx, tx, d.ID, previous.source)
	if err != nil {
		return err
	}
	if !found {
		return s.saveDelegationSubjects(ctx, tx, d.ID, previous.source, executor, d.CreatedAt)
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Set("execution_owner_key", conversationOwner(executor)).Set("execution_authority_json", conversationJSON(executor)).Where(query.And(query.Equal("owner_key", conversationOwner(previous.source)), query.Equal("delegation_id", d.ID), query.Equal("execution_owner_key", conversationOwner(previous.execution)), query.Equal("execution_authority_json", conversationJSON(previous.execution)))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

// Current collaboration/Identity/data authorization happens in the service.
// Its publication lookup must still match the transaction's unchanged grants
// and exact original assignment; a withdrawal or grant change advances d.Revision.
func (s *ConversationStore) validateTransferExecutionPublications(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, refs []sdk.ConversationRunReference, actor, executor sdk.ConversationAuthority) error {
	for _, ref := range refs {
		original, err := s.assignmentRunAuthority(ctx, tx, d, ref.ConversationID, actor)
		if err != nil {
			return err
		}
		for _, reader := range []sdk.ConversationAuthority{actor, executor} {
			if conversationOwner(reader) == conversationOwner(original) {
				continue // Exact selected-role policy is independently checked by the service.
			}
			if reader.UserID != d.OwnerUserID && (d.ExecutionSubject == nil || reader.UserID != d.ExecutionSubject.UserID) {
				grant, allowed := delegationParticipant(d, reader.UserID, "execution_read")
				if !allowed || !sdk.ParticipantPublisherVerified(grant, d.OwnerUserID, reader) {
					return conversationError("forbidden", "delegation_handoff_source_unavailable")
				}
			}
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(reader)), query.Equal("delegation_id", d.ID), query.Equal("conversation_id", ref.ConversationID), query.Equal("run_id", ref.RunID), query.Equal("before_step", 0))).Limit(33).Build()
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			verified, count := false, 0
			for rows.Next() {
				count++
				var raw []byte
				var release persistence.ConversationSourceRelease
				if err = rows.Scan(&raw); err == nil {
					err = json.Unmarshal(raw, &release)
				}
				if err != nil {
					break
				}
				if release.Purpose != "execution" {
					continue
				}
				publisher := release.Publisher
				if release.DelegationID != d.ID || release.Reference != ref || release.Producer != original || publisher == nil || conversationAuthority(*publisher) != nil || publisher.RoleKey == "" || conversationOwner(*publisher) != conversationOwner(original) {
					err = conversationError("forbidden", "delegation_handoff_source_unavailable")
					break
				}
				verified = true
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
			if !verified || count > 32 {
				return conversationError("forbidden", "delegation_handoff_source_unavailable")
			}
		}
	}
	return nil
}
