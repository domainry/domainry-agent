package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/query"
)

func (s *AgentTaskRunStore) Heartbeat(ctx context.Context, workspaceID, runID string, owner string, token int64, now time.Time, duration time.Duration) (agentrepository.AgentTaskHeartbeatResult, error) {
	expires := now.Add(duration)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Set("lease_expires_at", expires.UnixMilli()).Set("updated_at", now.UnixMilli()).Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", string(agentmodel.AgentTaskRunRunning)),
		ormbuilder.Equal("lease_owner", owner), ormbuilder.Equal("fencing_token", token), ormbuilder.GreaterThan("lease_expires_at", now.UnixMilli()),
	)).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, buildErr
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, rowsErr
	}
	if !oneRow {
		return agentrepository.AgentTaskHeartbeatResult{Lost: true}, nil
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	run.Lease.ExpiresAt, run.UpdatedAt, run.Revision = expires, now, run.Revision+1
	payload, _ := json.Marshal(run)
	persist, persistArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Set("payload_json", payload).Where(agentTaskLeasePredicate(runID, owner, token)).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, buildErr
	}
	if _, err := s.db.ExecContext(ctx, persist, persistArgs...); err != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	return agentrepository.AgentTaskHeartbeatResult{Lease: agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}}, nil
}

func (s *AgentTaskRunStore) SaveRunning(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", run.WorkspaceID).
		Columns("payload_json").Where(agentTaskLeasePredicate(run.ID, owner, token)).Limit(1).Build()
	if buildErr != nil {
		return buildErr
	}
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&currentPayload); err == sql.ErrNoRows {
		return agentError("conflict", "agent.task.terminal_fence_rejected")
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(currentPayload, &current); err != nil {
		return err
	}
	// Tool calls run concurrently with the provider execution and append durable
	// evidence under the same lease. A terminal/retry write must merge those
	// facts instead of replacing them with the worker's older in-memory copy.
	run.ToolCallCount = current.ToolCallCount
	run.Evidence.Authorization = current.Evidence.Authorization
	run.Evidence.ToolInvocationRefs = current.Evidence.ToolInvocationRefs
	run.Evidence.ToolInvocations = current.Evidence.ToolInvocations
	if run.Revision <= current.Revision {
		run.Revision = current.Revision + 1
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	next := timeMillis(run.NextAttemptAt)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", run.WorkspaceID).
		Set("status", string(run.Status)).Set("next_attempt_at", next).Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).
		Where(agentTaskLeasePredicate(run.ID, owner, token)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return agentError("conflict", "agent.task.terminal_fence_rejected")
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) SaveWaitingApproval(ctx context.Context, run agentmodel.AgentTaskRun, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(agentmodel.AgentTaskRunWaitingApproval))
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&payload); err == sql.ErrNoRows {
		return agentError("conflict", "agent.task.approval_state_conflict")
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return agentError("conflict", "agent.task.approval_state_conflict")
	}
	if current.Approval == nil {
		return agentError("conflict", "agent.task.approval_state_conflict")
	}
	if run.Approval == nil {
		return agentError("conflict", "agent.task.approval_state_conflict")
	}
	if current.Approval.ProposalID != run.Approval.ProposalID {
		return agentError("conflict", "agent.task.approval_state_conflict")
	}
	payload, err = json.Marshal(run)
	if err != nil {
		return err
	}
	statement, args, buildErr := agentTaskStatusUpdate(s.store, run, payload, agentmodel.AgentTaskRunWaitingApproval)
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return agentError("conflict", "agent.task.approval_state_conflict")
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) SaveTerminalOverride(ctx context.Context, run agentmodel.AgentTaskRun, expectedRevision int64) error {
	if !run.Status.Terminal() {
		return agentError("conflict", "agent.task.override_terminal_required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(run.Status))
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&payload); err == sql.ErrNoRows {
		return agentError("conflict", "agent.task.override_state_conflict")
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return agentError("conflict", "agent.task.override_state_conflict")
	}
	payload, err = json.Marshal(run)
	if err != nil {
		return err
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", run.WorkspaceID).
		Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).Where(agentTaskStatusPredicate(run.ID, run.Status)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return agentError("conflict", "agent.task.override_state_conflict")
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) RequestCancel(ctx context.Context, workspaceID, runID, reason string, now time.Time) (agentmodel.AgentTaskRun, bool, error) {
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	if run.Status.Terminal() || run.CancelRequestedAt != nil {
		return run, true, nil
	}
	previousUpdatedAt := run.UpdatedAt
	run.CancelRequestedAt, run.CancellationReason, run.UpdatedAt, run.Revision = &now, reason, now, run.Revision+1
	if run.Status == agentmodel.AgentTaskRunPending || run.Status == agentmodel.AgentTaskRunRetryScheduled {
		run.Status, run.CompletedAt = agentmodel.AgentTaskRunCancelled, &now
	}
	payload, _ := json.Marshal(run)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Set("status", string(run.Status)).Set("payload_json", payload).Set("updated_at", now.UnixMilli()).Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("updated_at", previousUpdatedAt.UnixMilli()),
	)).Build()
	if buildErr != nil {
		return agentmodel.AgentTaskRun{}, false, buildErr
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentmodel.AgentTaskRun{}, false, rowsErr
	}
	if !updated {
		latest, _, getErr := s.Get(ctx, workspaceID, runID)
		return latest, true, getErr
	}
	return run, false, nil
}

func (s *AgentTaskRunStore) SaveOperationalTransition(ctx context.Context, run agentmodel.AgentTaskRun, expectedStatus agentmodel.AgentTaskRunStatus, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(expectedStatus))
	if buildErr != nil {
		return buildErr
	}
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&currentPayload); err == sql.ErrNoRows {
		return agentError("conflict", "agent.task.operation_state_conflict")
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if json.Unmarshal(currentPayload, &current) != nil || current.Revision != expectedRevision {
		return agentError("conflict", "agent.task.operation_state_conflict")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	statement, args, buildErr := agentTaskStatusUpdate(s.store, run, payload, expectedStatus)
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return agentError("conflict", "agent.task.operation_state_conflict")
	}
	return tx.Commit()
}
