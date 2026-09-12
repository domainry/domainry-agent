package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-orm/query"
)

type LifecycleStore struct{ store *Store }

func NewLifecycleStore(store *Store) LifecycleStore { return LifecycleStore{store: store} }

func (s LifecycleStore) ListLifecycleCandidates(ctx context.Context, workspaceID string, queryValue agentpersistence.LifecycleQuery) ([]agentpersistence.LifecycleCandidate, error) {
	workspaceID, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	type kindRetention struct {
		kind      string
		retention time.Duration
	}
	kinds := []kindRetention{}
	switch strings.TrimSpace(queryValue.PolicyKey) {
	case "agent.dialog.v1":
		kinds = []kindRetention{{"session", queryValue.Retention}, {"proposal", queryValue.Retention}}
	}
	result := []agentpersistence.LifecycleCandidate{}
	if strings.TrimSpace(queryValue.PolicyKey) == "agent.dialog.v1" {
		conversations, conversationErr := s.listConversationLifecycleCandidates(ctx, workspaceID, queryValue)
		if conversationErr != nil {
			return nil, conversationErr
		}
		result = append(result, conversations...)
		if queryValue.Limit > 0 && len(result) >= queryValue.Limit {
			return result[:queryValue.Limit], nil
		}
	}
	for _, item := range kinds {
		builder := query.NewWorkspaceSelectBuilder(s.store.Renderer(), "_agent_runtime_states", workspaceID).
			Columns("state_key", "user_id", "role_key", "payload_json", "updated_at").
			Where(query.And(query.Equal("kind", item.kind), query.LessThanOrEqual("updated_at", queryValue.Now.Add(-item.retention).UTC().UnixNano()))).
			OrderBy(query.Ascending("updated_at"), query.Ascending("state_key"))
		statement, args, buildErr := builder.Build()
		if buildErr != nil {
			return nil, buildErr
		}
		rows, queryErr := s.store.Database().QueryContext(ctx, statement, args...)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			value := agentstate.AgentStateRecord{WorkspaceID: workspaceID, Kind: item.kind}
			var payload []byte
			if scanErr := rows.Scan(&value.Key, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt); scanErr != nil {
				_ = rows.Close()
				return nil, scanErr
			}
			value.Payload = payload
			if !agentLifecycleEligible(value.Kind, payload) {
				continue
			}
			result = append(result, agentpersistence.LifecycleCandidate{State: value, ResourceType: "agent.state", ResourceID: value.Kind + ":" + value.Key, UpdatedAt: time.Unix(0, value.UpdatedAt).UTC(), Payload: append(json.RawMessage(nil), payload...)})
			if queryValue.Limit > 0 && len(result) >= queryValue.Limit {
				_ = rows.Close()
				return result, nil
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return nil, rowsErr
		}
		_ = rows.Close()
	}
	return result, nil
}

func (s LifecycleStore) listConversationLifecycleCandidates(ctx context.Context, workspaceID string, queryValue agentpersistence.LifecycleQuery) ([]agentpersistence.LifecycleCandidate, error) {
	retention := queryValue.Retention
	if value := queryValue.StatusRetention["archived"]; value > 0 {
		retention = value
	}
	cutoff := queryValue.Now.Add(-retention).UTC().UnixMilli()
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "revision", "updated_at", "payload_json").Where(query.And(query.Equal("archived", 1), query.LessThanOrEqual("updated_at", cutoff))).OrderBy(query.Ascending("updated_at"), query.Ascending("conversation_id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	type conversationRow struct {
		owner, id         string
		revision, updated int64
		raw               []byte
	}
	scanned := []conversationRow{}
	for rows.Next() {
		var row conversationRow
		var conversation struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := rows.Scan(&row.owner, &row.id, &row.revision, &row.updated, &row.raw); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if json.Unmarshal(row.raw, &conversation) != nil || conversation.WorkspaceID != workspaceID {
			continue
		}
		scanned = append(scanned, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := []agentpersistence.LifecycleCandidate{}
	for _, row := range scanned {
		active, activeErr := s.conversationLifecycleActive(ctx, row.owner, row.id)
		if activeErr != nil {
			return nil, activeErr
		}
		if active {
			continue
		}
		payload, payloadErr := s.conversationLifecyclePayload(ctx, row.owner, row.id, row.raw)
		if payloadErr != nil {
			return nil, payloadErr
		}
		result = append(result, agentpersistence.LifecycleCandidate{ResourceType: "agent.conversation", ResourceID: row.id, OwnerKey: row.owner, Revision: row.revision, UpdatedAt: time.UnixMilli(row.updated).UTC(), Payload: payload})
		if queryValue.Limit > 0 && len(result) >= queryValue.Limit {
			break
		}
	}
	return result, rows.Err()
}

func (s LifecycleStore) conversationLifecycleActive(ctx context.Context, owner, id string) (bool, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("status").Where(query.And(query.Equal("owner_key", owner), query.Equal("conversation_id", id))).Build()
	if err != nil {
		return false, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "queued", "running", "waiting", "pending", "uncertain":
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s LifecycleStore) conversationLifecyclePayload(ctx context.Context, owner, id string, conversation []byte) (json.RawMessage, error) {
	graph := map[string]any{"conversation": json.RawMessage(append([]byte(nil), conversation...))}
	for _, table := range []string{"_agent_conversation_messages", "_agent_conversation_runs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_conversation_inputs", "_agent_conversation_steps", "_agent_conversation_tool_calls", interactionTable, conversationTaskTable} {
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("conversation_id", id))).Build()
		if table == conversationTaskTable {
			statement, args, err = query.NewSelectBuilder(s.store.Renderer(), table).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("source_conversation_id", id))).Build()
		}
		if err != nil {
			return nil, err
		}
		rows, err := s.store.Database().QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		items := []json.RawMessage{}
		for rows.Next() {
			var raw json.RawMessage
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, append(json.RawMessage(nil), raw...))
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if len(items) > 0 {
			graph[table] = items
		}
	}
	return json.Marshal(graph)
}

func agentLifecycleEligible(kind string, payload []byte) bool {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	switch kind {
	case "session":
		archived, _ := value["archived"].(bool)
		return archived
	case "proposal":
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(value["status"])))
		return status != "" && status != "draft" && status != "pending" && status != "open"
	default:
		return false
	}
}

func (s LifecycleStore) DeleteLifecycleCandidate(ctx context.Context, workspaceID string, candidate agentpersistence.LifecycleCandidate) (bool, error) {
	if candidate.ResourceType == "agent.conversation" {
		return s.deleteConversationLifecycleCandidate(ctx, workspaceID, candidate)
	}
	statement, args, err := query.NewWorkspaceDeleteBuilder(s.store.Renderer(), "_agent_runtime_states", workspaceID).
		Where(query.And(query.Equal("kind", candidate.State.Kind), query.Equal("state_key", candidate.State.Key), query.Equal("updated_at", candidate.State.UpdatedAt))).Build()
	if err != nil {
		return false, err
	}
	result, err := s.store.Database().ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s LifecycleStore) deleteConversationLifecycleCandidate(ctx context.Context, workspaceID string, candidate agentpersistence.LifecycleCandidate) (bool, error) {
	workspaceID, err := normalizeWorkspaceID(workspaceID)
	if err != nil || candidate.OwnerKey == "" || candidate.ResourceID == "" || candidate.Revision < 1 {
		return false, err
	}
	var deleted bool
	err = (&ConversationStore{store: s.store}).transaction(ctx, func(tx *sql.Tx) error {
		lookup, lookupArgs, buildErr := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("payload_json").Where(query.And(query.Equal("owner_key", candidate.OwnerKey), query.Equal("conversation_id", candidate.ResourceID), query.Equal("revision", candidate.Revision))).Build()
		if buildErr != nil {
			return buildErr
		}
		var raw []byte
		if scanErr := tx.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&raw); errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		} else if scanErr != nil {
			return scanErr
		}
		var conversation struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if json.Unmarshal(raw, &conversation) != nil || conversation.WorkspaceID != workspaceID {
			return nil
		}
		statement, args, buildErr := query.NewDeleteBuilder(s.store.Renderer(), "_agent_conversations").Where(query.And(query.Equal("owner_key", candidate.OwnerKey), query.Equal("conversation_id", candidate.ResourceID), query.Equal("revision", candidate.Revision))).Build()
		if buildErr != nil {
			return buildErr
		}
		result, execErr := tx.ExecContext(ctx, statement, args...)
		if execErr != nil {
			return execErr
		}
		rows, execErr := result.RowsAffected()
		if execErr != nil || rows != 1 {
			return execErr
		}
		deleted = true
		for _, table := range []string{"_agent_conversation_messages", "_agent_conversation_runs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_conversation_inputs", "_agent_conversation_steps", "_agent_conversation_tool_calls", interactionTable} {
			statement, args, buildErr = query.NewDeleteBuilder(s.store.Renderer(), table).Where(query.And(query.Equal("owner_key", candidate.OwnerKey), query.Equal("conversation_id", candidate.ResourceID))).Build()
			if buildErr != nil {
				return buildErr
			}
			if _, execErr = tx.ExecContext(ctx, statement, args...); execErr != nil {
				return execErr
			}
		}
		statement, args, buildErr = query.NewDeleteBuilder(s.store.Renderer(), conversationTaskTable).Where(query.And(query.Equal("owner_key", candidate.OwnerKey), query.Equal("source_conversation_id", candidate.ResourceID))).Build()
		if buildErr != nil {
			return buildErr
		}
		_, execErr = tx.ExecContext(ctx, statement, args...)
		return execErr
	})
	return deleted, err
}

var _ agentpersistence.AgentLifecycleRepository = LifecycleStore{}
