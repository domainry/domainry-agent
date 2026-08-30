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
	ormbuilder "github.com/domainry/domainry-orm/query"
)

var _ agentrepository.AgentToolCallLedger = (*AgentTaskRunStore)(nil)

func (s *AgentTaskRunStore) BeginAgentToolCall(ctx context.Context, start agentrepository.AgentToolCallStart) (string, int, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", start.WorkspaceID).
		Columns("payload_json", "status", "lease_owner", "fencing_token").Where(ormbuilder.Equal("run_id", start.TaskRunID)).Limit(1).Build()
	if buildErr != nil {
		return "", 0, buildErr
	}
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &status, &owner, &token); err != nil {
		return "", 0, err
	}
	if status != string(agentmodel.AgentTaskRunRunning) || owner != start.Owner || token != start.FencingToken {
		return "", 0, agentError("conflict", "agent.task.tool_fence_rejected")
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return "", 0, err
	}
	if start.MaxToolCalls <= 0 || run.ToolCallCount >= start.MaxToolCalls {
		return "", run.ToolCallCount, agentError("rate_limited", "agent.task.tool_call_limit")
	}
	usedCost := 0
	for _, invocation := range run.Evidence.ToolInvocations {
		usedCost += invocation.CostUnits
	}
	if start.CostUnits <= 0 || start.MaxCostUnits <= 0 || usedCost+start.CostUnits > start.MaxCostUnits {
		return "", run.ToolCallCount, agentError("rate_limited", "agent.task.cost_budget_exceeded")
	}
	run.ToolCallCount++
	run.Revision++
	run.UpdatedAt = time.Now().UTC()
	ref := fmt.Sprintf("agent_tool_%s_%d", run.ID, run.ToolCallCount)
	run.Evidence.Authorization = append(run.Evidence.Authorization, start.Authorization)
	run.Evidence.ToolInvocationRefs = append(run.Evidence.ToolInvocationRefs, ref)
	run.Evidence.ToolInvocations = append(run.Evidence.ToolInvocations, agentmodel.AgentTaskToolInvocationEvidence{Ref: ref, Tool: start.Tool, InputHash: start.InputHash, Status: "running", Authorization: start.Authorization, StartedAt: run.UpdatedAt, CostUnits: start.CostUnits})
	updated, _ := json.Marshal(run)
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", start.WorkspaceID).
		Set("payload_json", updated).Set("updated_at", run.UpdatedAt.UnixMilli()).Where(agentTaskLeasePredicate(start.TaskRunID, start.Owner, start.FencingToken)).Build()
	if buildErr != nil {
		return "", 0, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return "", 0, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return "", 0, rowsErr
	}
	if !oneRow {
		return "", 0, agentError("conflict", "agent.task.tool_fence_rejected")
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return ref, run.ToolCallCount, nil
}

func (s *AgentTaskRunStore) FinishAgentToolCall(ctx context.Context, finish agentrepository.AgentToolCallFinish) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.Renderer(), "agent_task_runs", finish.WorkspaceID).
		Columns("payload_json", "status", "lease_owner", "fencing_token").Where(ormbuilder.Equal("run_id", finish.TaskRunID)).Limit(1).Build()
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &status, &owner, &token); err != nil {
		return err
	}
	if status != string(agentmodel.AgentTaskRunRunning) || owner != finish.Owner || token != finish.FencingToken {
		return agentError("conflict", "agent.task.tool_fence_rejected")
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return err
	}
	now := time.Now().UTC()
	found := false
	for index := range run.Evidence.ToolInvocations {
		item := &run.Evidence.ToolInvocations[index]
		if item.Ref != finish.CallRef {
			continue
		}
		if item.FinishedAt != nil {
			return nil
		}
		item.Status, item.ErrorCode, item.FinishedAt = finish.Status, finish.ErrorCode, &now
		item.DurationMilliseconds = now.Sub(item.StartedAt).Milliseconds()
		item.OutputHash = strings.TrimSpace(fmt.Sprint(finish.Evidence["output_hash"]))
		found = true
		break
	}
	if !found {
		return agentError("not_found", "agent.task.tool_call_not_found")
	}
	run.UpdatedAt = now
	run.Revision++
	updated, _ := json.Marshal(run)
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.Renderer(), "agent_task_runs", finish.WorkspaceID).
		Set("payload_json", updated).Set("updated_at", now.UnixMilli()).Where(agentTaskLeasePredicate(finish.TaskRunID, finish.Owner, finish.FencingToken)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !oneRow {
		return agentError("conflict", "agent.task.tool_fence_rejected")
	}
	return tx.Commit()
}
