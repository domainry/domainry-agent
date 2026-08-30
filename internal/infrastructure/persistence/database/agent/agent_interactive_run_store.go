package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

func (s *AgentTaskRunStore) CreateInteractiveRun(ctx context.Context, run agentmodel.AgentInteractiveRun) (agentmodel.AgentInteractiveRun, bool, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	columns := []string{"run_id", "session_id", "user_id", "role_key", "surface", "status", "idempotency_key", "process_id", "task_run_id", "payload_json", "created_at", "updated_at"}
	statement, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.Renderer(), "agent_interactive_runs", run.WorkspaceID).Columns(columns...).Values(
		run.ID, run.SessionID, run.UserID, run.RoleKey, run.Surface, string(run.Status), run.IdempotencyKey, run.ProcessID, run.TaskRunID, payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli(),
	).Build()
	if buildErr != nil {
		return agentmodel.AgentInteractiveRun{}, false, buildErr
	}
	_, err = s.db.ExecContext(ctx, statement, args...)
	if err == nil {
		return run, false, nil
	}
	existing, found, getErr := s.getInteractiveByIdempotency(ctx, run.WorkspaceID, run.IdempotencyKey)
	if getErr == nil && found {
		return existing, true, nil
	}
	return agentmodel.AgentInteractiveRun{}, false, err
}

func (s *AgentTaskRunStore) GetInteractiveRun(ctx context.Context, workspaceID, runID string) (agentmodel.AgentInteractiveRun, bool, error) {
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_interactive_runs", strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(ormbuilder.Equal("run_id", strings.TrimSpace(runID))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return scanInteractiveRun(s.db.QueryRowContext(ctx, statement, args...))
}

func (s *AgentTaskRunStore) getInteractiveByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentInteractiveRun, bool, error) {
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_interactive_runs", strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(ormbuilder.Equal("idempotency_key", strings.TrimSpace(key))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return scanInteractiveRun(s.db.QueryRowContext(ctx, statement, args...))
}

func scanInteractiveRun(row *sql.Row) (agentmodel.AgentInteractiveRun, bool, error) {
	var payload []byte
	if err := row.Scan(&payload); err == sql.ErrNoRows {
		return agentmodel.AgentInteractiveRun{}, false, nil
	} else if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	var run agentmodel.AgentInteractiveRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return run, true, nil
}

func (s *AgentTaskRunStore) ListInteractiveRuns(ctx context.Context, workspaceID, userID, roleKey string, filter agentrepository.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("user_id", strings.TrimSpace(userID)), ormbuilder.Equal("role_key", strings.TrimSpace(roleKey))}
	if len(filter.Statuses) > 0 {
		values := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			values[index] = string(status)
		}
		predicates = append(predicates, ormbuilder.In("status", values...))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_interactive_runs", strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(ormbuilder.And(predicates...)).OrderBy(ormbuilder.Descending("created_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []agentmodel.AgentInteractiveRun{}
	for rows.Next() {
		var payload []byte
		var run agentmodel.AgentInteractiveRun
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *AgentTaskRunStore) SaveInteractiveRun(ctx context.Context, run agentmodel.AgentInteractiveRun, expectedRevision int64) (bool, error) {
	current, found, err := s.GetInteractiveRun(ctx, run.WorkspaceID, run.ID)
	if err != nil || !found || current.Revision != expectedRevision {
		return false, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return false, err
	}
	statement, args, buildErr := interactiveRunUpdateBuilder(s.store, run, payload, current.UpdatedAt.UnixMilli()).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *AgentTaskRunStore) CommitInteractiveTaskHandoff(ctx context.Context, run agentmodel.AgentInteractiveRun, expectedRevision int64, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_interactive_runs", run.WorkspaceID).
		Columns("payload_json").Where(ormbuilder.Equal("run_id", run.ID)).Limit(1).Build()
	if buildErr != nil {
		return agentmodel.AgentInteractiveRun{}, false, buildErr
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	var current agentmodel.AgentInteractiveRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if current.TaskRunID != "" {
		return current, true, nil
	}
	if current.Revision != expectedRevision || current.Status != agentmodel.AgentInteractiveRunRunning || current.WorkspaceID != task.WorkspaceID || task.InteractiveRunID != current.ID {
		return agentmodel.AgentInteractiveRun{}, false, agentError("conflict", "agent.interactive.handoff_conflict")
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	columns := []string{"run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.Renderer(), "agent_task_runs", task.WorkspaceID).Columns(columns...).Values(
		task.ID, task.IdempotencyKey, task.TaskKey, task.ProcessID, string(task.Status), "", int64(0), int64(0), timeMillis(task.NextAttemptAt), taskPayload, task.CreatedAt.UnixMilli(), task.UpdatedAt.UnixMilli(),
	).Build()
	if buildErr != nil {
		return agentmodel.AgentInteractiveRun{}, false, buildErr
	}
	if _, err := tx.ExecContext(ctx, insert, insertArgs...); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if err := RegisterAgentTaskWorkerScope(ctx, s.store, tx, task.WorkspaceID, task.UpdatedAt); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	now := time.Now().UTC()
	current.Status, current.RouteType, current.RoutedTargetKey, current.RoutedTargetVersion = agentmodel.AgentInteractiveRunHandedOff, run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion
	current.ProcessID, current.TaskRunID, current.UpdatedAt, current.CompletedAt, current.Revision = task.ProcessID, task.ID, now, &now, current.Revision+1
	updated, _ := json.Marshal(current)
	update, updateArgs, buildErr := interactiveRunUpdateBuilder(s.store, current, updated, run.UpdatedAt.UnixMilli()).Build()
	if buildErr != nil {
		return agentmodel.AgentInteractiveRun{}, false, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if rows != 1 {
		return agentmodel.AgentInteractiveRun{}, false, agentError("conflict", "agent.interactive.handoff_conflict")
	}
	if err := tx.Commit(); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return current, false, nil
}

func interactiveRunUpdateBuilder(store *Store, run agentmodel.AgentInteractiveRun, payload []byte, expectedUpdatedAt int64) *ormbuilder.UpdateBuilder {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.Renderer(), "agent_interactive_runs", run.WorkspaceID).
		Set("status", string(run.Status)).Set("process_id", run.ProcessID).Set("task_run_id", run.TaskRunID).
		Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).Where(ormbuilder.And(
		ormbuilder.Equal("run_id", run.ID), ormbuilder.Equal("updated_at", expectedUpdatedAt),
	))
}

var _ agentrepository.AgentInteractiveRunRepository = (*AgentTaskRunStore)(nil)
