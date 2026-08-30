package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func (s *AgentTaskRunStore) ClaimNext(ctx context.Context, workspaceID string, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		claim, found, err := s.claimNextOnce(ctx, workspaceID, owner, now, duration)
		if err == nil {
			return claim, found, nil
		}
		if !s.store.IsTransientError(err) {
			return agentrepository.AgentTaskClaim{}, false, err
		}
		lastErr = err
		delay := time.Duration(attempt+1) * time.Millisecond
		select {
		case <-ctx.Done():
			return agentrepository.AgentTaskClaim{}, false, ctx.Err()
		case <-time.After(delay):
		}
	}
	return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task claim retry exhausted: %w", lastErr)
}

func (s *AgentTaskRunStore) ClaimAgentTaskRun(ctx context.Context, workspaceID, runID, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	workspaceID, runID, owner = strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(owner)
	if len(workspaceID) == 0 {
		return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim is invalid")
	}
	if runID == "" || owner == "" || duration <= 0 {
		return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim is invalid")
	}
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		claim, found, err := s.claimAgentTaskRunOnce(ctx, workspaceID, runID, owner, now, duration)
		if err == nil {
			return claim, found, nil
		}
		if !s.store.IsTransientError(err) {
			return agentrepository.AgentTaskClaim{}, false, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return agentrepository.AgentTaskClaim{}, false, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Millisecond):
		}
	}
	return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim retry exhausted: %w", lastErr)
}

func (s *AgentTaskRunStore) claimAgentTaskRunOnce(ctx context.Context, workspaceID, runID, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Columns("payload_json", "fencing_token").Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), agentTaskEligiblePredicate(now),
	)).Limit(1).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &priorToken)
	if err == sql.ErrNoRows {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	token, expires := priorToken+1, now.Add(duration)
	run.Status, run.Attempt, run.Revision, run.UpdatedAt = agentmodel.AgentTaskRunRunning, run.Attempt+1, run.Revision+1, now
	run.Lease = agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}
	run.Attempts = append(run.Attempts, agentmodel.AgentTaskAttempt{Number: run.Attempt, StartedAt: now})
	updatedPayload, _ := json.Marshal(run)
	update, updateArgs, buildErr := agentTaskClaimUpdate(s.store, workspaceID, runID, owner, token, priorToken, expires, now, updatedPayload)
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil || !oneRow {
		return agentrepository.AgentTaskClaim{}, false, rowsErr
	}
	if err := tx.Commit(); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	return agentrepository.AgentTaskClaim{Run: run, Lease: run.Lease}, true, nil
}

func (s *AgentTaskRunStore) claimNextOnce(ctx context.Context, workspaceID string, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", workspaceID).
		Columns("run_id", "payload_json", "fencing_token").Where(agentTaskEligiblePredicate(now)).
		OrderBy(ormbuilder.Ascending("created_at")).Limit(1).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	var runID string
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, queryArgs...).Scan(&runID, &payload, &priorToken)
	if err == sql.ErrNoRows {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	token, expires := priorToken+1, now.Add(duration)
	run.Status, run.Attempt, run.Revision, run.UpdatedAt = agentmodel.AgentTaskRunRunning, run.Attempt+1, run.Revision+1, now
	run.Lease = agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}
	run.Attempts = append(run.Attempts, agentmodel.AgentTaskAttempt{Number: run.Attempt, StartedAt: now})
	updatedPayload, _ := json.Marshal(run)
	update, updateArgs, buildErr := agentTaskClaimUpdate(s.store, workspaceID, runID, owner, token, priorToken, expires, now, updatedPayload)
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentrepository.AgentTaskClaim{}, false, rowsErr
	}
	if !oneRow {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	return agentrepository.AgentTaskClaim{Run: run, Lease: run.Lease}, true, nil
}
