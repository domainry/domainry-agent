package agent

import (
	"context"
	"database/sql"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

// View of the exact upstream agreement is distinct from managing its task.
// Physical owner routing never replaces the actor passed to application policy.
func (s *ConversationStore) dependencyDelegation(ctx context.Context, db conversationDB, id string, actor sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	d, _, err := s.participantDelegation(ctx, db, id, actor, "view")
	if err != nil {
		return d, err
	}
	if d.SubjectExited {
		return d, conversationError("forbidden", "dependency_source_unavailable")
	}
	executor := d.ExecutionSubject != nil && d.ExecutionSubject.UserID == actor.UserID && d.ExecutionSubject.RuntimeID == actor.RuntimeID && d.ExecutionSubject.WorkspaceID == actor.WorkspaceID
	if d.OwnerUserID != actor.UserID && !executor {
		grant, found := delegationParticipant(d, actor.UserID, "view")
		if !found || !sdk.ParticipantPublisherVerified(grant, d.OwnerUserID, actor) {
			return d, conversationError("forbidden", "dependency_source_unavailable")
		}
	}
	return d, nil
}

// System invalidation follows the already persisted goal graph across record
// owners. Scope is verified against each physical owner key; the graph and
// private records are never returned to the changing participant.
func (s *ConversationStore) dependencyGoalDelegations(ctx context.Context, db conversationDB, root string, realm sdk.ConversationAuthority) ([]sdk.ConversationDelegation, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationTable).Columns("owner_key", "payload_json").Where(query.Equal("root_conversation_id", root)).OrderBy(query.Ascending("delegation_id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var legacy []struct {
		key string
		d   sdk.ConversationDelegation
	}
	items := []sdk.ConversationDelegation{}
	for rows.Next() {
		var key string
		var raw []byte
		var d sdk.ConversationDelegation
		if err = rows.Scan(&key, &raw); err == nil {
			err = json.Unmarshal(raw, &d)
		}
		if err != nil {
			break
		}
		if d.RootConversationID != root {
			err = conversationError("forbidden", "dependency_source_unavailable")
			break
		}
		if d.OwnerUserID == "" {
			legacy = append(legacy, struct {
				key string
				d   sdk.ConversationDelegation
			}{key, d})
			continue
		}
		if conversationOwner(delegationRecordAuthority(d, realm)) != key {
			continue // Same opaque root in another workspace is outside this graph.
		}
		normalizeAgreement(&d)
		items = append(items, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, item := range legacy {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Columns("source_authority_json").Where(query.And(query.Equal("owner_key", item.key), query.Equal("delegation_id", item.d.ID))).Build()
		if err != nil {
			return nil, err
		}
		var raw []byte
		var owner sdk.ConversationAuthority
		err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
		if err == nil {
			if err := json.Unmarshal(raw, &owner); err != nil || conversationAuthority(owner) != nil || conversationOwner(owner) != item.key {
				return nil, conversationError("forbidden", "dependency_source_unavailable")
			}
			if sameConversationWorkspace(owner, realm) {
				item.d.OwnerUserID = owner.UserID
				normalizeAgreement(&item.d)
				items = append(items, item.d)
			}
			continue
		} else if err != sql.ErrNoRows {
			return nil, err
		}
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(item.key, item.d.TaskID)).Build()
		if err != nil {
			return nil, err
		}
		task, err := scanConversationTask(db.QueryRowContext(ctx, q, args...))
		if err != nil {
			return nil, err
		}
		if conversationOwner(task.authority) != item.key || task.task.DelegationID != item.d.ID || task.task.ExecutionConversationID != item.d.ConversationID {
			return nil, conversationError("forbidden", "dependency_source_unavailable")
		}
		if !sameConversationWorkspace(task.authority, realm) {
			continue
		}
		item.d.OwnerUserID = task.authority.UserID
		normalizeAgreement(&item.d)
		items = append(items, item.d)
	}
	if len(items) > 256 {
		return nil, conversationError("conflict", "delegation_limit")
	}
	return items, nil
}
