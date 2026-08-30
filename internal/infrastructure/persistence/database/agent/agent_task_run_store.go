package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

type AgentTaskRunStore struct {
	store *Store
	db    modulehost.Database
}

const agentTaskWorkerQueueKind = "agent_task"

func RegisterAgentTaskWorkerScope(ctx context.Context, store *Store, executor modulehost.Executor, workspaceID string, updatedAt time.Time) error {
	if store == nil {
		return fmt.Errorf("agent task worker scope store unavailable")
	}
	return store.registerWorkerScope(ctx, executor, workspaceID, updatedAt.UTC().UnixMilli())
}

func NewAgentTaskRunStore(store *Store) *AgentTaskRunStore {
	if store == nil {
		return &AgentTaskRunStore{}
	}
	return &AgentTaskRunStore{store: store, db: store.Database()}
}

// BackfillWorkerScopes inventories legacy Agent tasks once during Runtime
// startup. It is deliberately not part of EnsureSchema: ordinary Agent reads
// and writes must not perform a global workspace inventory.
func (s *AgentTaskRunStore) BackfillWorkerScopes(ctx context.Context) error {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.Renderer(), "agent_task_runs").Projections(
		ormbuilder.Project(ormbuilder.Column("workspace_id")),
		ormbuilder.Project(ormbuilder.Max(ormbuilder.Column("updated_at"))),
	).GroupBy(ormbuilder.Column("workspace_id")).Build()
	if buildErr != nil {
		return buildErr
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	type workerScope struct {
		workspaceID     string
		updatedAtMillis int64
	}
	scopes := []workerScope{}
	for rows.Next() {
		var scope workerScope
		if err := rows.Scan(&scope.workspaceID, &scope.updatedAtMillis); err != nil {
			_ = rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, scope := range scopes {
		if err := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, scope.workspaceID, time.UnixMilli(scope.updatedAtMillis)); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentTaskRunStore) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.Renderer(), "agent_task_runs", run.WorkspaceID).
		Columns(agentTaskRunColumns()...).Values(run.ID, run.IdempotencyKey, run.TaskKey, run.ProcessID, string(run.Status), "", int64(0), int64(0), timeMillis(run.NextAttemptAt), payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli()).Build()
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
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(ormbuilder.Equal("run_id", strings.TrimSpace(runID))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return s.scanRun(s.db.QueryRowContext(ctx, statement, args...))
}

func (s *AgentTaskRunStore) getByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentTaskRun, bool, error) {
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Columns("payload_json").Where(ormbuilder.Equal("idempotency_key", key)).Limit(1).Build()
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

func (s *AgentTaskRunStore) List(ctx context.Context, workspaceID string, filter agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	predicates := []ormbuilder.Predicate{}
	if filter.ProcessID != "" {
		predicates = append(predicates, ormbuilder.Equal("process_id", strings.TrimSpace(filter.ProcessID)))
	}
	if filter.TaskKey != "" {
		predicates = append(predicates, ormbuilder.Equal("task_key", strings.TrimSpace(filter.TaskKey)))
	}
	if len(filter.Statuses) > 0 {
		values := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			values[index] = string(status)
		}
		predicates = append(predicates, ormbuilder.In("status", values...))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", strings.TrimSpace(workspaceID)).Columns("payload_json")
	if len(predicates) > 0 {
		builder = builder.Where(ormbuilder.And(predicates...))
	}
	statement, args, buildErr := builder.OrderBy(ormbuilder.Ascending("created_at")).Limit(limit).Build()
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
	return []string{"run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
}

func agentTaskStatusPredicate(runID string, status agentmodel.AgentTaskRunStatus) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", string(status)))
}

func agentTaskLeasePredicate(runID, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(
		agentTaskStatusPredicate(runID, agentmodel.AgentTaskRunRunning),
		ormbuilder.Equal("lease_owner", owner),
		ormbuilder.Equal("fencing_token", token),
	)
}

func agentTaskEligiblePredicate(now time.Time) ormbuilder.Predicate {
	return ormbuilder.Or(
		ormbuilder.And(
			ormbuilder.In("status", string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled)),
			ormbuilder.LessThanOrEqual("next_attempt_at", now.UnixMilli()),
		),
		ormbuilder.And(
			ormbuilder.Equal("status", string(agentmodel.AgentTaskRunRunning)),
			ormbuilder.LessThanOrEqual("lease_expires_at", now.UnixMilli()),
		),
	)
}

func agentTaskClaimUpdate(store *Store, workspaceID, runID, owner string, token, priorToken int64, expires, now time.Time, payload []byte) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.Renderer(), "agent_task_runs", workspaceID).
		Set("status", string(agentmodel.AgentTaskRunRunning)).Set("lease_owner", owner).Set("fencing_token", token).
		Set("lease_expires_at", expires.UnixMilli()).Set("payload_json", payload).Set("updated_at", now.UnixMilli()).
		Where(ormbuilder.And(
			ormbuilder.Equal("run_id", runID), ormbuilder.Equal("fencing_token", priorToken), agentTaskEligiblePredicate(now),
		)).Build()
}

func agentTaskPayloadSelect(store *Store, workspaceID, runID, status string) (string, []any, error) {
	return ormbuilder.NewWorkspaceSelectBuilder(store.Renderer(), "agent_task_runs", workspaceID).
		Columns("payload_json").Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", status),
	)).Limit(1).Build()
}

func agentTaskStatusUpdate(store *Store, run agentmodel.AgentTaskRun, payload []byte, expectedStatus agentmodel.AgentTaskRunStatus) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.Renderer(), "agent_task_runs", run.WorkspaceID).
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

var _ agentrepository.AgentTaskRunRepository = (*AgentTaskRunStore)(nil)
