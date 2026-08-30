// Agent state persistence.
package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

type AgentStateStore struct {
	store *Store
	db    modulehost.Database
}

func NewAgentStateStore(store *Store) AgentStateStore {
	if store == nil {
		return AgentStateStore{}
	}
	return AgentStateStore{store: store, db: store.Database()}
}

func (r AgentStateStore) Put(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord) error {
	return r.PutBatch(ctx, workspaceID, []agentmodel.AgentStateRecord{value})
}

func (r AgentStateStore) PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error {
	workspace, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	workspaceID = workspace
	byKey := make(map[string]agentmodel.AgentStateRecord, len(values))
	order := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.WorkspaceID) != workspaceID {
			return fmt.Errorf("agent state workspace %q does not match repository workspace %q", value.WorkspaceID, workspaceID)
		}
		key := strings.TrimSpace(value.Kind) + "\x00" + strings.TrimSpace(value.Key)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = value
	}
	if len(order) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	columns := []string{"kind", "state_key", "user_id", "role_key", "payload_json", "updated_at"}
	for start := 0; start < len(order); start += 50 {
		end := min(start+50, len(order))
		insert := ormbuilder.NewWorkspaceInsertBuilder(r.store.Renderer(), "agent_runtime_state", workspaceID).Columns(columns...)
		for _, key := range order[start:end] {
			value := byKey[key]
			insert.Values(strings.TrimSpace(value.Kind), strings.TrimSpace(value.Key), strings.TrimSpace(value.UserID), strings.TrimSpace(value.RoleKey), []byte(value.Payload), value.UpdatedAt)
		}
		insert, buildErr := r.store.Profile().ApplyUpsert(insert, []string{"workspace_id", "kind", "state_key"},
			ormbuilder.AssignExpression("user_id", ormbuilder.InsertedValue("user_id")),
			ormbuilder.AssignExpression("role_key", ormbuilder.InsertedValue("role_key")),
			ormbuilder.AssignExpression("payload_json", ormbuilder.InsertedValue("payload_json")),
			ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")),
		)
		if buildErr != nil {
			return buildErr
		}
		statement, args, buildErr := insert.Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r AgentStateStore) CompareAndSwap(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error) {
	workspace, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	workspaceID = workspace
	if strings.TrimSpace(value.WorkspaceID) != workspaceID {
		return false, fmt.Errorf("agent state workspace %q does not match repository workspace %q", value.WorkspaceID, workspaceID)
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.Renderer(), "agent_runtime_state", workspaceID).
		Set("payload_json", []byte(value.Payload)).Set("updated_at", value.UpdatedAt).Where(ormbuilder.And(
		ormbuilder.Equal("kind", strings.TrimSpace(value.Kind)),
		ormbuilder.Equal("state_key", strings.TrimSpace(value.Key)),
		ormbuilder.Equal("updated_at", expectedUpdatedAt),
	)).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := r.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r AgentStateStore) Get(ctx context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error) {
	workspace, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	workspaceID = workspace
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.Renderer(), "agent_runtime_state", workspaceID).
		Columns("workspace_id", "user_id", "role_key", "payload_json", "updated_at").Where(ormbuilder.And(
		ormbuilder.Equal("kind", strings.TrimSpace(kind)), ormbuilder.Equal("state_key", strings.TrimSpace(key)),
	)).Limit(1).Build()
	if buildErr != nil {
		return agentmodel.AgentStateRecord{}, false, buildErr
	}
	value := agentmodel.AgentStateRecord{Kind: kind, Key: key}
	var payload []byte
	err = r.db.QueryRowContext(ctx, statement, args...).Scan(&value.WorkspaceID, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt)
	if err == sql.ErrNoRows {
		return agentmodel.AgentStateRecord{}, false, nil
	}
	if err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	value.Payload = append(value.Payload[:0], payload...)
	return value, true, nil
}

func (r AgentStateStore) List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error) {
	workspace, err := normalizeWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = workspace
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("kind", strings.TrimSpace(kind))}
	for _, filter := range []struct{ column, value string }{{"user_id", userID}, {"role_key", roleKey}} {
		if strings.TrimSpace(filter.value) == "" {
			continue
		}
		predicates = append(predicates, ormbuilder.Equal(filter.column, strings.TrimSpace(filter.value)))
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.Renderer(), "agent_runtime_state", workspaceID).
		Columns("state_key", "workspace_id", "user_id", "role_key", "payload_json", "updated_at").
		Where(ormbuilder.And(predicates...)).OrderBy(ormbuilder.Descending("updated_at")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []agentmodel.AgentStateRecord{}
	for rows.Next() {
		value := agentmodel.AgentStateRecord{Kind: kind}
		var payload []byte
		if err := rows.Scan(&value.Key, &value.WorkspaceID, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt); err != nil {
			return nil, err
		}
		value.Payload = append(value.Payload[:0], payload...)
		values = append(values, value)
	}
	return values, rows.Err()
}

func normalizeWorkspaceID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("Agent workspace is required")
	}
	return value, nil
}
