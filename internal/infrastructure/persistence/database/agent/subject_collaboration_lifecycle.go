package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func subjectIndexRecords(rows *sql.Rows, columns []string) ([]json.RawMessage, error) {
	items := []json.RawMessage{}
	for rows.Next() {
		values, destinations := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		item := map[string]any{}
		for i, column := range columns {
			value := values[i]
			if bytes, ok := value.([]byte); ok {
				value = string(bytes)
			}
			if text, ok := value.(string); ok && strings.HasSuffix(column, "_json") {
				if !json.Valid([]byte(text)) {
					return nil, conversationError("unavailable", "execution_subject_mismatch")
				}
				value = json.RawMessage(text)
			}
			item[column] = value
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	return items, rows.Err()
}

// Erasure is a system operation scoped to the exact subject owner key. Stored
// admission authorities route counterpart records; they never authorize a user
// tool invocation. Stop effects before deleting the routing/lease records.
func (s *ConversationStore) eraseSubjectCollaboration(ctx context.Context, tx *sql.Tx, a sdk.ConversationAuthority, changed map[string]int64) error {
	type affected struct {
		delegation sdk.ConversationDelegation
		source     sdk.ConversationAuthority
		owned      bool
	}
	items := map[string]affected{}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("payload_json").Where(query.And(query.Equal("link_kind", conversationPeerLinkKindDelegation), query.Equal("owner_key", conversationOwner(a)))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw []byte
		var d sdk.ConversationDelegation
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &d)
		}
		if err != nil {
			break
		}
		if d.OwnerUserID != "" && d.OwnerUserID != a.UserID {
			err = conversationError("unavailable", "execution_subject_mismatch")
			break
		}
		d.OwnerUserID = a.UserID
		items[d.ID] = affected{d, a, true}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationDelegationSubjectTable).Columns("owner_key", "delegation_id", "source_authority_json", "execution_authority_json").Where(query.Equal("execution_owner_key", conversationOwner(a))).Build()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	type binding struct {
		id     string
		source sdk.ConversationAuthority
	}
	bindings := []binding{}
	for rows.Next() {
		var ownerKey, id string
		var sourceRaw, executionRaw []byte
		var source, executor sdk.ConversationAuthority
		if err = rows.Scan(&ownerKey, &id, &sourceRaw, &executionRaw); err == nil {
			err = json.Unmarshal(sourceRaw, &source)
		}
		if err == nil {
			err = json.Unmarshal(executionRaw, &executor)
		}
		if err != nil {
			break
		}
		if conversationAuthority(source) != nil || conversationAuthority(executor) != nil || !sameConversationWorkspace(a, source) || !sameConversationWorkspace(a, executor) || conversationOwner(source) != ownerKey || conversationOwner(executor) != conversationOwner(a) {
			err = conversationError("unavailable", "execution_subject_mismatch")
			break
		}
		bindings = append(bindings, binding{id, source})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if _, found := items[binding.id]; found {
			continue
		}
		d, err := s.ownedConversationDelegation(ctx, tx, binding.id, binding.source)
		if err != nil {
			return err
		}
		items[d.ID] = affected{d, binding.source, false}
	}
	for _, item := range items {
		d, err := s.ownedConversationDelegation(ctx, tx, item.delegation.ID, item.source)
		if err != nil {
			return err
		}
		executor, err := s.delegationExecutionAuthority(ctx, tx, d, item.source)
		if err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(executor), d.TaskID)).Build()
		if err != nil {
			return err
		}
		task, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
		if err != nil {
			return err
		}
		if !task.task.Terminal() {
			if err := s.controlDelegationTask(ctx, tx, d, "cancel", item.source); err != nil {
				return err
			}
			changed["stopped_collaboration_tasks"]++
		}
		if !item.owned {
			d, err = s.ownedConversationDelegation(ctx, tx, d.ID, item.source)
			if err != nil {
				return err
			}
			previous := d.Revision
			d.SubjectExited = true
			if d.Status != "accepted_delivery" && d.Status != "rejected" && d.Status != "cancelled" {
				d.Status = "cancelled"
			}
			d.Decision = "execution_subject_exited"
			d.Revision++
			d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
			if err := s.saveConversationDelegation(ctx, tx, d, previous, item.source); err != nil {
				return err
			}
			if err := s.propagateRequirementChange(ctx, tx, d, []string{"assignment"}, item.source); err != nil {
				return err
			}
			changed["counterpart_delegations"]++
		}
	}
	// Propagation can create notices for another affected delegation. Finish
	// every invalidation before superseding notices, independent of map order.
	for _, item := range items {
		d := item.delegation
		if err := s.eraseSubjectDelegationReleases(ctx, tx, d.ID, a, item.owned, changed); err != nil {
			return err
		}
		if err := s.supersedeSubjectExitMessages(ctx, tx, d, item.source, changed); err != nil {
			return err
		}
	}
	if err := s.removeSubjectParticipation(ctx, tx, a, changed); err != nil {
		return err
	}
	return s.removeSubjectAgentGrants(ctx, tx, a, changed)
}

func (s *ConversationStore) supersedeSubjectExitMessages(ctx context.Context, tx *sql.Tx, d sdk.ConversationDelegation, a sdk.ConversationAuthority, changed map[string]int64) error {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("owner_key", "message_id", "payload_json").Where(query.And(delegationMessageScope(d, a), query.Equal("consumed_run_id", ""))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	type pending struct {
		owner, id string
		message   sdk.ConversationAgentMessage
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		var raw []byte
		if err = rows.Scan(&item.owner, &item.id, &raw); err == nil {
			err = json.Unmarshal(raw, &item.message)
		}
		if err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		item.message.Superseded = true
		q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("consumed_run_id", "subject_exited").Set("payload_json", conversationJSON(item.message)).Where(query.And(query.Equal("owner_key", item.owner), query.Equal("message_id", item.id), query.Equal("consumed_run_id", ""))).Build()
		if err := conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
		changed["superseded_subject_exit_messages"]++
	}
	return nil
}

func (s *ConversationStore) eraseSubjectDelegationReleases(ctx context.Context, tx *sql.Tx, id string, a sdk.ConversationAuthority, all bool, changed map[string]int64) error {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationSourceReleaseTable).Columns("owner_key", "release_id", "payload_json").Where(query.Equal("delegation_id", id)).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	type key struct{ owner, id string }
	keys := []key{}
	for rows.Next() {
		var k key
		var raw []byte
		var release persistence.ConversationSourceRelease
		if err = rows.Scan(&k.owner, &k.id, &raw); err == nil {
			err = json.Unmarshal(raw, &release)
		}
		if err != nil {
			break
		}
		if all || release.Publisher != nil && sameConversationWorkspace(a, *release.Publisher) && release.Publisher.UserID == a.UserID {
			keys = append(keys, k)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range keys {
		q, args, err := query.NewDeleteBuilder(s.store.Renderer(), conversationSourceReleaseTable).Where(query.And(query.Equal("owner_key", k.owner), query.Equal("release_id", k.id))).Build()
		if err := conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		changed["collaboration_source_releases"]++
	}
	return nil
}

func (s *ConversationStore) subjectForeignGrants(ctx context.Context, tx *sql.Tx, kind string, a sdk.ConversationAuthority) (map[string]sdk.ConversationAuthority, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationPeerLinkTable).Columns("link_id", "owner_key", "owner_user_id").Where(query.And(query.Equal("link_kind", kind), query.Equal("peer_key", conversationOwner(a)), query.Not(query.Equal("owner_key", conversationOwner(a))))).Build()
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]sdk.ConversationAuthority{}
	for rows.Next() {
		var id, owner, user string
		if err := rows.Scan(&id, &owner, &user); err != nil {
			return nil, err
		}
		authority := a
		authority.UserID = user
		if conversationAuthority(authority) != nil || conversationOwner(authority) != owner {
			return nil, conversationError("unavailable", "execution_subject_mismatch")
		}
		items[id] = authority
	}
	return items, rows.Err()
}

func (s *ConversationStore) removeSubjectParticipation(ctx context.Context, tx *sql.Tx, a sdk.ConversationAuthority, changed map[string]int64) error {
	items, err := s.subjectForeignGrants(ctx, tx, conversationPeerLinkKindParticipant, a)
	if err != nil {
		return err
	}
	for id, owner := range items {
		d, err := s.ownedConversationDelegation(ctx, tx, id, owner)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		previous := d.Revision
		d.Participants = slices.DeleteFunc(d.Participants, func(p sdk.ConversationDelegationParticipant) bool { return p.UserID == a.UserID })
		d.ParticipantsRevision++
		d.Revision++
		d.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := s.saveConversationDelegation(ctx, tx, d, previous, owner); err != nil {
			return err
		}
		if err := s.supersedePeerMessages(ctx, tx, d, owner); err != nil {
			return err
		}
		changed["removed_collaboration_participations"]++
	}
	return nil
}

func (s *ConversationStore) removeSubjectAgentGrants(ctx context.Context, tx *sql.Tx, a sdk.ConversationAuthority, changed map[string]int64) error {
	items, err := s.subjectForeignGrants(ctx, tx, conversationPeerLinkKindUseGrant, a)
	if err != nil {
		return err
	}
	for id, owner := range items {
		agent, err := s.ownedConversationAgent(ctx, tx, id, owner)
		if err != nil {
			return err
		}
		previous := agent.Revision
		agent.SharedWithUserIDs = slices.DeleteFunc(agent.SharedWithUserIDs, func(user string) bool { return user == a.UserID })
		agent.Revision++
		agent.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationPeerLinkTable).Set("revision", agent.Revision).Set("updated_at", agent.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(agent)).Where(query.And(query.Equal("link_kind", conversationPeerLinkKindInstance), query.Equal("owner_key", conversationOwner(owner)), query.Equal("link_id", id), query.Equal("peer_key", ""), query.Equal("revision", previous))).Build()
		if err := conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
		changed["removed_agent_memberships"]++
	}
	return nil
}
