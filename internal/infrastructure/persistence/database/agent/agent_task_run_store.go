package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-orm/query"
)

type AgentTaskRunStore struct {
	store *Store
	db    modulehost.Database
}

func RegisterAgentTaskWorkerScope(ctx context.Context, store *Store, executor modulehost.Executor, workspaceID string, updatedAt time.Time) error {
	if store == nil {
		return fmt.Errorf("agent task worker scope store unavailable")
	}
	return store.registerWorkerScope(ctx, executor, workspaceID, updatedAt.UTC().Format(time.RFC3339Nano))
}

func NewAgentTaskRunStore(store *Store) *AgentTaskRunStore {
	if store == nil {
		return &AgentTaskRunStore{}
	}
	return &AgentTaskRunStore{store: store, db: store.Database()}
}

func (s *AgentTaskRunStore) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	statement, args, buildErr := query.NewWorkspaceInsertBuilder(s.store.Renderer(), agentRunTable, run.WorkspaceID).
		Columns(agentTaskRunColumns()...).Values(agentRunKindTask, run.WorkspaceID, run.ID, run.IdempotencyKey, run.TaskKey, run.ProcessID, string(run.Status), "", int64(0), int64(0), timeMillis(run.NextAttemptAt), payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli()).Build()
	if buildErr != nil {
		return agentmodel.AgentTaskRun{}, false, buildErr
	}
	_, err = s.db.ExecContext(ctx, statement, args...)
	if err == nil {
		if registerErr := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, run.WorkspaceID, run.UpdatedAt); registerErr != nil {
			return agentmodel.AgentTaskRun{}, false, registerErr
		}
		return run, false, nil
	}
	existing, found, getErr := s.getByIdempotency(ctx, run.WorkspaceID, run.IdempotencyKey)
	if getErr == nil && found {
		if registerErr := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, existing.WorkspaceID, existing.UpdatedAt); registerErr != nil {
			return agentmodel.AgentTaskRun{}, false, registerErr
		}
		return existing, true, nil
	}
	return agentmodel.AgentTaskRun{}, false, err
}

func (s *AgentTaskRunStore) Get(ctx context.Context, workspaceID, runID string) (agentmodel.AgentTaskRun, bool, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.store.Renderer(), agentRunTable, strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(query.And(agentRunKindPredicate(agentRunKindTask), query.Equal("run_id", strings.TrimSpace(runID)))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return s.scanRun(s.db.QueryRowContext(ctx, statement, args...))
}

func (s *AgentTaskRunStore) getByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentTaskRun, bool, error) {
	statement, args, err := query.NewWorkspaceSelectBuilder(s.store.Renderer(), agentRunTable, workspaceID).
		Columns("payload_json").Where(query.And(agentRunKindPredicate(agentRunKindTask), query.Equal("idempotency_key", key))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return s.scanRun(s.db.QueryRowContext(ctx, statement, args...))
}

type rowScanner interface{ Scan(...any) error }

func (s *AgentTaskRunStore) scanRun(row rowScanner) (agentmodel.AgentTaskRun, bool, error) {
	var payload []byte
	if err := row.Scan(&payload); err == sql.ErrNoRows {
		return agentmodel.AgentTaskRun{}, false, nil
	} else if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, true, nil
}

func (s *AgentTaskRunStore) List(ctx context.Context, workspaceID string, filter agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	predicates := []query.Predicate{agentRunKindPredicate(agentRunKindTask)}
	if filter.ProcessID != "" {
		predicates = append(predicates, query.Equal("process_id", strings.TrimSpace(filter.ProcessID)))
	}
	if filter.TaskKey != "" {
		predicates = append(predicates, query.Equal("task_key", strings.TrimSpace(filter.TaskKey)))
	}
	if len(filter.Statuses) > 0 {
		values := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			values[index] = string(status)
		}
		predicates = append(predicates, query.In("status", values...))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	builder := query.NewWorkspaceSelectBuilder(s.store.Renderer(), agentRunTable, strings.TrimSpace(workspaceID)).Columns("payload_json")
	if len(predicates) > 0 {
		builder = builder.Where(query.And(predicates...))
	}
	statement, args, buildErr := builder.OrderBy(query.Ascending("created_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentmodel.AgentTaskRun{}
	for rows.Next() {
		run, _, err := s.scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func agentTaskRunColumns() []string {
	return []string{"run_kind", "scope_key", "run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
}

func agentRunKindPredicate(kind string) query.Predicate { return query.Equal("run_kind", kind) }

func agentTaskStatusPredicate(runID string, status agentmodel.AgentTaskRunStatus) query.Predicate {
	return query.And(agentRunKindPredicate(agentRunKindTask), query.Equal("run_id", runID), query.Equal("status", string(status)))
}

func agentTaskLeasePredicate(runID, owner string, token int64) query.Predicate {
	return query.And(
		agentTaskStatusPredicate(runID, agentmodel.AgentTaskRunRunning),
		query.Equal("lease_owner", owner),
		query.Equal("fencing_token", token),
	)
}

func agentTaskEligiblePredicate(now time.Time) query.Predicate {
	return query.Or(
		query.And(
			agentRunKindPredicate(agentRunKindTask),
			query.In("status", string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled)),
			query.LessThanOrEqual("next_attempt_at", now.UnixMilli()),
		),
		query.And(
			agentRunKindPredicate(agentRunKindTask),
			query.Equal("status", string(agentmodel.AgentTaskRunRunning)),
			query.LessThanOrEqual("lease_expires_at", now.UnixMilli()),
		),
	)
}

func agentTaskClaimUpdate(store *Store, workspaceID, runID, owner string, token, priorToken int64, expires, now time.Time, payload []byte) (string, []any, error) {
	return query.NewWorkspaceUpdateBuilder(store.Renderer(), agentRunTable, workspaceID).
		Set("status", string(agentmodel.AgentTaskRunRunning)).Set("lease_owner", owner).Set("fencing_token", token).
		Set("lease_expires_at", expires.UnixMilli()).Set("payload_json", payload).Set("updated_at", now.UnixMilli()).
		Where(query.And(
			query.Equal("run_id", runID), query.Equal("fencing_token", priorToken), agentTaskEligiblePredicate(now),
		)).Build()
}

func agentTaskPayloadSelect(store *Store, workspaceID, runID, status string) (string, []any, error) {
	return query.NewWorkspaceSelectBuilder(store.Renderer(), agentRunTable, workspaceID).
		Columns("payload_json").Where(query.And(
		agentRunKindPredicate(agentRunKindTask), query.Equal("run_id", runID), query.Equal("status", status),
	)).Limit(1).Build()
}

func agentTaskStatusUpdate(store *Store, run agentmodel.AgentTaskRun, payload []byte, expectedStatus agentmodel.AgentTaskRunStatus) (string, []any, error) {
	return query.NewWorkspaceUpdateBuilder(store.Renderer(), agentRunTable, run.WorkspaceID).
		Set("status", string(run.Status)).Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).
		Where(agentTaskStatusPredicate(run.ID, expectedStatus)).Build()
}

func timeMillis(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return value.UTC().UnixMilli()
}

func agentTaskExactlyOneRow(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

var _ agentpersistence.AgentTaskRunRepository = (*AgentTaskRunStore)(nil)
