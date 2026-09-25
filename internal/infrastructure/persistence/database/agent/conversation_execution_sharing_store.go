package agent

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) executionPublications(ctx context.Context, db conversationDB, d sdk.ConversationDelegation, reader sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(reader)), query.Equal("delegation_id", d.ID))).OrderBy(query.Ascending("release_id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []persistence.ConversationSourceRelease{}
	for rows.Next() {
		var raw []byte
		var release persistence.ConversationSourceRelease
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := unmarshalDurableJSON(raw, &release); err != nil {
			return nil, err
		}
		if release.Purpose == "execution" {
			out = append(out, release)
			if len(out) > 32 {
				return nil, conversationError("unavailable", "source_limit_exceeded")
			}
		}
	}
	return out, rows.Err()
}

func (s *ConversationStore) ConversationDelegationExecutions(ctx context.Context, id string, a sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "execution_read")
	if err != nil {
		return nil, err
	}
	return s.executionPublications(ctx, s.store.Database(), d, a)
}

func (s *ConversationStore) ConversationDelegationExecutionPublications(ctx context.Context, id string, a sdk.ConversationAuthority) ([]persistence.ConversationSourceRelease, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	d, _, err := s.participantDelegation(ctx, s.store.Database(), id, a, "view")
	if err != nil {
		return nil, err
	}
	values, err := s.executionPublications(ctx, s.store.Database(), d, a)
	if err != nil {
		return nil, err
	}
	out := []persistence.ConversationSourceRelease{}
	for _, value := range values {
		if value.Publisher != nil && value.Publisher.UserID == a.UserID && value.Producer.UserID == a.UserID && value.Publisher.RuntimeID == a.RuntimeID && value.Publisher.WorkspaceID == a.WorkspaceID && value.Producer.RuntimeID == a.RuntimeID && value.Producer.WorkspaceID == a.WorkspaceID {
			out = append(out, value)
		}
	}
	return out, nil
}

func (s *ConversationStore) PublishConversationDelegationExecution(ctx context.Context, id string, in sdk.ConversationExecutionShare, a sdk.ConversationAuthority) (sdk.ConversationExecutionPublication, error) {
	var out sdk.ConversationExecutionPublication
	if conversationAuthority(a) != nil || a.RoleKey == "" || !personalMemoryKey(in.ClientID) || in.ExpectedRevision < 1 || in.Reference.ConversationID == "" || in.Reference.RunID == "" || in.Reference.BeforeStep != 0 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 {
		return out, conversationError("bad_request", "execution_publication_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := s.validatePeerMutation(ctx, tx, in.ToolRequest, a); err != nil {
			return err
		}
		key := conversationHash([]string{"execution-publication", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		d, _, err := s.participantDelegation(ctx, tx, id, a, "view")
		if err != nil {
			return err
		}
		if d.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		run, err := s.runRow(ctx, tx, in.Reference.ConversationID, in.Reference.RunID, a)
		if err != nil {
			return err
		}
		// A private owner-scoped lookup plus the immutable task association is
		// required. Neither a caller-selected user nor an arbitrary run qualifies.
		task := run.Run.BackgroundTask
		if task == nil || task.DelegationID != d.ID || conversationOwner(run.Authority) != conversationOwner(a) {
			return conversationError("forbidden", "delegation_source_not_released")
		}
		valid := task.TaskID == d.TaskID && run.Run.ConversationID == d.ConversationID
		if !valid {
			assignments, err := s.conversationAssignments(ctx, tx, d, delegationRecordAuthority(d, a))
			if err != nil {
				return err
			}
			for _, assignment := range assignments {
				valid = valid || assignment.TaskID == task.TaskID && assignment.ConversationID == run.Run.ConversationID
			}
		}
		if !valid {
			return conversationError("forbidden", "delegation_source_not_released")
		}
		if in.Withdraw {
			// Remove only execution publications for this actual provider and
			// exact run. Contract and delivery publications remain separate.
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("owner_key", "release_id", "payload_json").Where(query.And(query.Equal("producer_key", conversationOwner(a)), query.Equal("delegation_id", id), query.Equal("conversation_id", in.Reference.ConversationID), query.Equal("run_id", in.Reference.RunID))).Build()
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			type deletion struct{ owner, key string }
			deletes := []deletion{}
			for rows.Next() {
				var owner, key string
				var raw []byte
				var release persistence.ConversationSourceRelease
				if err = rows.Scan(&owner, &key, &raw); err == nil {
					err = unmarshalDurableJSON(raw, &release)
				}
				if err != nil {
					break
				}
				if release.Purpose == "execution" {
					deletes = append(deletes, deletion{owner, key})
				}
			}
			if err == nil {
				err = rows.Err()
			}
			closeErr := rows.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			for _, item := range deletes {
				q, args, err := query.NewDeleteBuilder(s.store.Renderer(), conversationSourceReleaseTable).Where(query.And(query.Equal("owner_key", item.owner), query.Equal("release_id", item.key))).Build()
				if err = conversationExec(ctx, tx, q, args, err); err != nil {
					return err
				}
			}
		} else {
			readers := map[string]bool{d.OwnerUserID: true, a.UserID: true}
			for _, grant := range d.Participants {
				if sdk.ParticipantPublisherVerified(grant, d.OwnerUserID, a) && slices.Contains(grant.Operations, "execution_read") {
					readers[grant.UserID] = true
				}
			}
			for user := range readers {
				reader := a
				reader.UserID, reader.RoleKey = user, ""
				if err := s.saveSourceReleases(ctx, tx, id, "execution", a, reader, []sdk.ConversationRunReference{in.Reference}); err != nil {
					return err
				}
			}
		}
		d.Revision++
		d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := s.saveConversationDelegation(ctx, tx, d, in.ExpectedRevision, delegationRecordAuthority(d, a)); err != nil {
			return err
		}
		out = sdk.ConversationExecutionPublication{Reference: in.Reference, Publisher: a, Revision: d.Revision, Withdrawn: in.Withdraw}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

var _ persistence.ConversationExecutionSharingRepository = (*ConversationStore)(nil)
