package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	"github.com/domainry/domainry-orm/query"
)

func (s *AgentTaskRunStore) InsertAgentTask(ctx context.Context, executor modulehost.Executor, run agentrepository.AgentTaskMutation) error {
	if s == nil || executor == nil {
		return fmt.Errorf("Agent task transaction store is unavailable")
	}
	columns := []string{"run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	statement, args, err := query.NewWorkspaceInsertBuilder(s.store.Renderer(), "_agent_task_runs", run.WorkspaceID).Columns(columns...).Values(
		run.RunID, run.IdempotencyKey, run.TaskKey, run.ProcessID, run.Status, run.LeaseOwner, run.FencingToken, run.LeaseExpiresAt, run.NextAttemptAt, append([]byte(nil), run.Payload...), run.CreatedAtMillis, run.UpdatedAtMillis,
	).Build()
	if err != nil {
		return fmt.Errorf("build Agent task insert: %w", err)
	}
	if _, err := executor.ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	return RegisterAgentTaskWorkerScope(ctx, s.store, executor, run.WorkspaceID, time.UnixMilli(run.UpdatedAtMillis))
}

func (s *AgentTaskRunStore) UpdateAgentTask(ctx context.Context, executor modulehost.Executor, run agentrepository.AgentTaskMutation) error {
	if s == nil || executor == nil {
		return fmt.Errorf("Agent task transaction store is unavailable")
	}
	expected := strings.TrimSpace(run.ExpectedStatus)
	if expected == "" {
		expected = "running"
	}
	predicate := query.And(query.Equal("run_id", run.RunID), query.Equal("status", expected))
	if expected == "running" {
		predicate = query.And(predicate, query.Equal("lease_owner", run.LeaseOwner), query.Equal("fencing_token", run.FencingToken))
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(s.store.Renderer(), "_agent_task_runs", run.WorkspaceID).
		Set("status", run.Status).Set("payload_json", append([]byte(nil), run.Payload...)).Set("updated_at", run.UpdatedAtMillis).Where(predicate).Build()
	if err != nil {
		return fmt.Errorf("build Agent task update: %w", err)
	}
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return agentError("conflict", "agent.task.workflow_fence_rejected")
	}
	return nil
}

var _ agentrepository.AgentTaskTransactionRepository = (*AgentTaskRunStore)(nil)

func (s *AgentTaskRunStore) ApplyAgentTaskInsert(ctx context.Context, run agentrepository.AgentTaskMutation) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("Agent task mutation store is unavailable")
	}
	return s.InsertAgentTask(ctx, s.store.Database(), run)
}

func (s *AgentTaskRunStore) ApplyAgentTaskUpdate(ctx context.Context, run agentrepository.AgentTaskMutation) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("Agent task mutation store is unavailable")
	}
	return s.UpdateAgentTask(ctx, s.store.Database(), run)
}

var _ agentrepository.AgentTaskMutationRepository = (*AgentTaskRunStore)(nil)
