package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type conversationTaskRow struct {
	task        agentsdk.ConversationTask
	authority   agentsdk.ConversationAuthority
	requestHash string
	schedule    *agentsdk.ConversationTaskScheduleRef
}

var conversationTaskColumns = []string{"payload_json", "authority_json", "request_hash", "scheduled_plan_id", "scheduler_run_id", "scheduled_for"}

type conversationTaskCursor struct {
	Owner, Query, ID string
	Created, Cutoff  int64
}

func scanConversationTask(row interface{ Scan(...any) error }) (conversationTaskRow, error) {
	var out conversationTaskRow
	var raw, authority []byte
	var planID, schedulerRunID sql.NullString
	var scheduledFor sql.NullInt64
	if err := row.Scan(&raw, &authority, &out.requestHash, &planID, &schedulerRunID, &scheduledFor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, conversationError("not_found", "task_not_found")
		}
		return out, err
	}
	if err := unmarshalDurableJSON(raw, &out.task); err != nil {
		return out, err
	}
	normalizeConversationTaskGoal(&out.task)
	if err := unmarshalDurableJSON(authority, &out.authority); err != nil {
		return out, err
	}
	if planID.Valid || schedulerRunID.Valid || scheduledFor.Valid {
		if !planID.Valid || !schedulerRunID.Valid || !scheduledFor.Valid {
			return out, conversationError("conflict", "scheduled_task_metadata_invalid")
		}
		out.schedule = &agentsdk.ConversationTaskScheduleRef{PlanID: planID.String, SchedulerRunID: schedulerRunID.String, ScheduledFor: time.UnixMilli(scheduledFor.Int64).UTC()}
	}
	return out, nil
}

func normalizeConversationTaskGoal(task *agentsdk.ConversationTask) {
	if task.CompletionMode == "" {
		task.CompletionMode = agentsdk.ConversationTaskCompletionModeLegacyResponse
	}
	if task.Brief == nil {
		brief := agentsdk.DefaultConversationTaskBrief(task.Goal)
		task.Brief = &brief
	}
	if task.AgreementRevision < 1 {
		task.AgreementRevision = 1
	}
	if task.GoalProgress.Revision < 1 {
		task.GoalProgress = agentsdk.ConversationGoalProgress{
			Revision: 1, Status: agentsdk.ConversationGoalStatusActive, Phase: "queued",
			CompletedItems: []string{}, RemainingItems: append([]string(nil), task.Brief.CompletionConditions...), UpdatedAt: task.UpdatedAt,
		}
		if task.GoalProgress.UpdatedAt.IsZero() {
			task.GoalProgress.UpdatedAt = task.CreatedAt
		}
		setConversationTaskGoalPhase(task, task.UpdatedAt)
	}
	if task.GoalProgress.CompletedItems == nil {
		task.GoalProgress.CompletedItems = []string{}
	}
	if task.GoalProgress.RemainingItems == nil {
		task.GoalProgress.RemainingItems = []string{}
	}
}

func canonicalConversationTaskBrief(value agentsdk.ConversationTaskBrief) agentsdk.ConversationTaskBrief {
	value.ExplicitFields = append([]string(nil), value.ExplicitFields...)
	value.InferredFields = append([]string(nil), value.InferredFields...)
	sort.Strings(value.ExplicitFields)
	sort.Strings(value.InferredFields)
	if value.DueAt != nil {
		due := value.DueAt.UTC().Truncate(time.Millisecond)
		value.DueAt = &due
	}
	return value
}

func conversationTaskBudgetExhausted(code string) bool {
	switch code {
	case "execution_limit", "work_budget_exhausted", "work_budget_provider_usage_exceeded", "agent.task.cost_budget_exceeded", "agent.task.tool_call_limit":
		return true
	default:
		return false
	}
}

func setConversationTaskGoalPhase(task *agentsdk.ConversationTask, now time.Time) {
	status, phase, blocker := agentsdk.ConversationGoalStatusActive, "queued", ""
	switch task.Status {
	case agentsdk.ConversationTaskStatusRunning:
		phase = "executing"
	case agentsdk.ConversationTaskStatusAwaitingReview:
		status, phase, blocker = agentsdk.ConversationGoalStatusBlocked, "awaiting_review", "completion_review_required"
	case agentsdk.ConversationTaskStatusCompleted:
		status, phase = agentsdk.ConversationGoalStatusCompleted, "completed"
	case agentsdk.ConversationTaskStatusCancelled:
		status, phase = agentsdk.ConversationGoalStatusPaused, "paused"
	case agentsdk.ConversationTaskStatusFailed:
		status, phase, blocker = agentsdk.ConversationGoalStatusBlocked, "failed", task.ErrorCode
		if conversationTaskBudgetExhausted(task.ErrorCode) {
			status, phase = agentsdk.ConversationGoalStatusBudgetExhausted, "budget_exhausted"
		}
	}
	task.GoalProgress.Revision = max(1, task.GoalProgress.Revision+1)
	task.GoalProgress.Status, task.GoalProgress.Phase, task.GoalProgress.Blocker = status, phase, blocker
	task.GoalProgress.UpdatedAt = now
	if status == agentsdk.ConversationGoalStatusCompleted {
		task.GoalProgress.CompletedItems = append([]string(nil), task.Brief.CompletionConditions...)
		task.GoalProgress.RemainingItems = []string{}
	} else if task.Status == agentsdk.ConversationTaskStatusAwaitingReview && task.Completion != nil {
		task.GoalProgress.CompletedItems, task.GoalProgress.RemainingItems = []string{}, []string{}
		for _, check := range task.Completion.Verification.Checks {
			if check.Verdict == "met" && check.Method != "recipient" && check.Method != "pending" {
				task.GoalProgress.CompletedItems = append(task.GoalProgress.CompletedItems, check.Requirement)
			} else {
				task.GoalProgress.RemainingItems = append(task.GoalProgress.RemainingItems, check.Requirement)
			}
		}
	} else if len(task.GoalProgress.RemainingItems) == 0 {
		task.GoalProgress.RemainingItems = append([]string(nil), task.Brief.CompletionConditions...)
	}
}

func conversationTaskReceipt(task agentsdk.ConversationTask) agentsdk.ConversationTaskReceipt {
	allowed := make([]string, 0, len(task.ToolScope))
	for _, tool := range task.ToolScope {
		allowed = append(allowed, tool.Key)
	}
	return agentsdk.ConversationTaskReceipt{
		ID: task.ID, Status: task.Status, AllowedTools: allowed, Budget: task.Budget,
		SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, CreatedAt: task.CreatedAt,
	}
}

func (s *ConversationStore) ApplyConversationTaskTool(ctx context.Context, in agentsdk.ConversationToolRequest, prepared agentsdk.ConversationTask) (agentsdk.ConversationToolResult, error) {
	definition := agentsdk.BackgroundTaskConversationTool()
	result, err := s.applyLocalTool(ctx, in, []agentsdk.ConversationToolDefinition{definition}, func(tx *sql.Tx, claim agentpersistence.ConversationClaim, call agentpersistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
		var start agentsdk.ConversationTaskStart
		if call.Call.Name != definition.Key || unmarshalDurableJSON([]byte(call.Call.Arguments), &start) != nil {
			return agentsdk.ConversationToolResult{}, conversationError("bad_request", "task_start_invalid")
		}
		expectedBrief := agentsdk.DefaultConversationTaskBrief(start.Goal)
		if start.Brief != nil {
			expectedBrief = canonicalConversationTaskBrief(*start.Brief)
		}
		if prepared.Brief == nil {
			prepared.Brief = &expectedBrief
		}
		if prepared.AgreementRevision == 0 {
			prepared.AgreementRevision = 1
		}
		if prepared.CompletionMode == "" {
			prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeLegacyResponse
			if start.Brief != nil {
				prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeAssessed
			}
		}
		if prepared.Agent == nil {
			prepared.Agent = claim.Run.Agent
		} else if conversationHash(prepared.Agent) != conversationHash(claim.Run.Agent) {
			return agentsdk.ConversationToolResult{}, conversationError("conflict", "tool_input_conflict")
		}
		if prepared.ID != "" || prepared.Status != "" || prepared.ExecutionRunID != "" || prepared.FollowUp != nil || prepared.SourceConversationID != in.ConversationID || prepared.SourceRunID != in.RunID || prepared.Goal != start.Goal || prepared.Input != start.Input || prepared.Budget != start.Budget || prepared.Brief == nil || conversationHash(*prepared.Brief) != conversationHash(expectedBrief) || prepared.AgreementRevision != 1 || len(prepared.ToolScope) != len(start.AllowedTools) || !validStoredConversationTaskContent(prepared) {
			return agentsdk.ConversationToolResult{}, conversationError("conflict", "tool_input_conflict")
		}
		seen := map[string]bool{}
		for index, tool := range prepared.ToolScope {
			if tool.Key != start.AllowedTools[index] || tool.Key == definition.Key || seen[tool.Key] || tool.Version == "" || tool.ActionKey == "" || tool.DefinitionHash == "" {
				return agentsdk.ConversationToolResult{}, conversationError("conflict", "tool_input_conflict")
			}
			seen[tool.Key] = true
		}
		countQuery, countArgs, countErr := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).
			Projections(query.Project(query.CountAll())).
			Where(query.And(conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("owner_key", conversationOwner(claim.Authority)), query.Equal("source_run_id", claim.Run.ID))).
			Build()
		if countErr != nil {
			return agentsdk.ConversationToolResult{}, countErr
		}
		children := 0
		if err := tx.QueryRowContext(ctx, countQuery, countArgs...).Scan(&children); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if children >= agentsdk.ConversationTaskMaxChildrenPerRun {
			return agentsdk.ConversationToolResult{}, conversationError("conflict", "task_child_limit")
		}
		if err := s.checkConversationQueueCapacity(ctx, tx, claim.Authority); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.ID = "task_" + conversationHash(call.IdempotencyKey)[:32]
		prepared.Status = agentsdk.ConversationTaskStatusQueued
		prepared.CreatedAt, prepared.UpdatedAt = now, now
		prepared.GoalProgress = agentsdk.ConversationGoalProgress{Revision: 1, Status: agentsdk.ConversationGoalStatusActive, Phase: "queued", CompletedItems: []string{}, RemainingItems: append([]string(nil), prepared.Brief.CompletionConditions...), UpdatedAt: now}
		q, args, buildErr := query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns(
			"record_kind", "owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json",
		).Values(
			conversationTaskKindTask, conversationOwner(claim.Authority), conversationHash([]string{claim.Authority.RuntimeID, claim.Authority.WorkspaceID}), prepared.ID, claim.Authority.RuntimeID, prepared.SourceConversationID, prepared.SourceRunID,
			prepared.Status, conversationJSON(claim.Authority), conversationHash([]any{call.Definition, call.Call}), now.UnixMilli(), now.UnixMilli(), conversationJSON(prepared),
		).Build()
		if err := conversationExec(ctx, tx, q, args, buildErr); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return agentsdk.ConversationToolResult{
			Completion: "accepted", Status: "completed", ResourceID: prepared.ID,
			Content: conversationAPIJSON(map[string]any{"task": conversationTaskReceipt(prepared)}),
		}, nil
	})
	return result, err
}

func (s *ConversationStore) AcceptScheduledConversationTask(ctx context.Context, in agentsdk.ScheduledConversationTaskRequest, prepared agentsdk.ConversationTask) (agentsdk.ScheduledConversationTaskReceipt, error) {
	var out agentsdk.ScheduledConversationTaskReceipt
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(in.Authority); err != nil {
			return err
		}
		expectedBrief := agentsdk.DefaultConversationTaskBrief(in.Input.Goal)
		if in.Input.Brief != nil {
			expectedBrief = canonicalConversationTaskBrief(*in.Input.Brief)
		}
		if prepared.Brief == nil {
			prepared.Brief = &expectedBrief
		}
		if prepared.AgreementRevision == 0 {
			prepared.AgreementRevision = 1
		}
		if prepared.CompletionMode == "" {
			prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeLegacyResponse
			if in.Input.Brief != nil {
				prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeAssessed
			}
		}
		if prepared.ID != "" || prepared.Status != "" || prepared.ExecutionRunID != "" || len(prepared.InputContent) != 0 || prepared.SourceConversationID != in.ConversationID || prepared.SourceRunID != in.SourceRunID ||
			prepared.Goal != in.Input.Goal || prepared.Input != in.Input.Input || prepared.Budget != in.Input.Budget || prepared.Brief == nil || conversationHash(*prepared.Brief) != conversationHash(expectedBrief) || prepared.AgreementRevision != 1 || !equalConversationFollowUpScope(prepared.FollowUp, in.Input.FollowUp) || !equalConversationTaskTools(prepared, in.Input.AllowedTools) {
			return conversationError("conflict", "scheduled_task_input_conflict")
		}
		conversation, err := s.get(ctx, tx, in.ConversationID, in.Authority)
		if err != nil {
			return err
		}
		if in.SourceRunID != "" {
			if _, err := s.runRow(ctx, tx, conversation.ID, in.SourceRunID, in.Authority); err != nil {
				return err
			}
		}
		requestHash := conversationHash(in)
		prepared.ID = "task_" + conversationHash([]any{conversationOwner(in.Authority), in.IdempotencyKey})[:32]
		prepared.Status = agentsdk.ConversationTaskStatusQueued
		readExisting := func() (conversationTaskRow, error) {
			statement, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(in.Authority), prepared.ID)).Build()
			if buildErr != nil {
				return conversationTaskRow{}, buildErr
			}
			return scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		}
		if existing, existingErr := readExisting(); existingErr == nil {
			if existing.requestHash != requestHash || conversationOwner(existing.authority) != conversationOwner(in.Authority) || existing.schedule == nil || existing.schedule.PlanID != in.PlanID || existing.schedule.SchedulerRunID != in.SchedulerRunID || !existing.schedule.ScheduledFor.Equal(in.ScheduledFor) {
				return conversationError("conflict", "scheduled_task_idempotency_conflict")
			}
			out.Task, out.Replay = existing.task, true
			return nil
		} else {
			var coded *agentsdk.Error
			if !errors.As(existingErr, &coded) || coded.Code != "agent.conversation.task_not_found" {
				return existingErr
			}
		}
		if err := s.checkConversationQueueCapacity(ctx, tx, in.Authority); err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.CreatedAt, prepared.UpdatedAt = now, now
		prepared.GoalProgress = agentsdk.ConversationGoalProgress{Revision: 1, Status: agentsdk.ConversationGoalStatusActive, Phase: "queued", CompletedItems: []string{}, RemainingItems: append([]string(nil), prepared.Brief.CompletionConditions...), UpdatedAt: now}
		statement, args, buildErr := query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns(
			"record_kind", "owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json", "scheduled_plan_id", "scheduler_run_id", "scheduled_for",
		).Values(
			conversationTaskKindTask, conversationOwner(in.Authority), conversationHash([]string{in.Authority.RuntimeID, in.Authority.WorkspaceID}), prepared.ID, in.Authority.RuntimeID, prepared.SourceConversationID, prepared.SourceRunID,
			prepared.Status, conversationJSON(in.Authority), requestHash, now.UnixMilli(), now.UnixMilli(), conversationJSON(prepared), in.PlanID, in.SchedulerRunID, in.ScheduledFor.UnixMilli(),
		).OnConflictDoNothing("record_kind", "owner_key", "task_id").Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 1 {
			out.Task = prepared
			return nil
		}
		if inserted != 0 {
			return conversationError("conflict", "scheduled_task_idempotency_conflict")
		}
		existing, err := readExisting()
		if err != nil {
			return err
		}
		if existing.requestHash != requestHash || conversationOwner(existing.authority) != conversationOwner(in.Authority) || existing.schedule == nil || existing.schedule.PlanID != in.PlanID || existing.schedule.SchedulerRunID != in.SchedulerRunID || !existing.schedule.ScheduledFor.Equal(in.ScheduledFor) {
			return conversationError("conflict", "scheduled_task_idempotency_conflict")
		}
		out.Task, out.Replay = existing.task, true
		return nil
	})
	return out, err
}

func (s *ConversationStore) AcceptBusinessEventConversationTask(ctx context.Context, in agentsdk.BusinessEventConversationTaskRequest, prepared agentsdk.ConversationTask) (agentsdk.BusinessEventConversationTaskReceipt, error) {
	var out agentsdk.BusinessEventConversationTaskReceipt
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(in.Authority); err != nil {
			return err
		}
		expectedBrief := agentsdk.DefaultConversationTaskBrief(in.Input.Goal)
		if in.Input.Brief != nil {
			expectedBrief = canonicalConversationTaskBrief(*in.Input.Brief)
		}
		if prepared.Brief == nil {
			prepared.Brief = &expectedBrief
		}
		if prepared.AgreementRevision == 0 {
			prepared.AgreementRevision = 1
		}
		if prepared.CompletionMode == "" {
			prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeLegacyResponse
			if in.Input.Brief != nil {
				prepared.CompletionMode = agentsdk.ConversationTaskCompletionModeAssessed
			}
		}
		expectedEvent := agentsdk.ConversationTaskBusinessEvent{
			Source: in.Source, Rule: in.Rule, Execution: agentsdk.ConversationBusinessEventExecutionIdentity{WorkspaceID: in.Authority.WorkspaceID, UserID: in.Authority.UserID, RoleKey: in.Authority.RoleKey}, IdempotencyKey: in.IdempotencyKey, Mode: in.Mode,
			TargetAgentID: in.AgentID, RelatedTaskID: in.RelatedTaskID,
		}
		if prepared.ID != "" || prepared.Status != "" || prepared.ExecutionRunID != "" || len(prepared.InputContent) != 0 || prepared.SourceConversationID != in.ConversationID || prepared.SourceRunID != "" ||
			prepared.Goal != in.Input.Goal || prepared.Input != in.Input.Input || prepared.Budget != in.Input.Budget || prepared.Brief == nil || conversationHash(*prepared.Brief) != conversationHash(expectedBrief) ||
			prepared.AgreementRevision != 1 || prepared.FollowUp != nil || prepared.BusinessEvent == nil || conversationHash(*prepared.BusinessEvent) != conversationHash(expectedEvent) ||
			prepared.Agent == nil || prepared.Agent.ID != in.AgentID || !equalConversationTaskTools(prepared, in.Input.AllowedTools) {
			return conversationError("conflict", "business_event_task_input_conflict")
		}
		conversation, err := s.get(ctx, tx, in.ConversationID, in.Authority)
		if err != nil {
			return err
		}
		if conversation.AgentID != in.AgentID {
			return conversationError("conflict", "business_event_agent_mismatch")
		}
		if in.Mode == "wake" {
			statement, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(in.Authority), in.RelatedTaskID)).Build()
			if buildErr != nil {
				return buildErr
			}
			related, readErr := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
			if readErr != nil {
				return readErr
			}
			if related.task.SourceConversationID != in.ConversationID || related.task.Agent == nil || related.task.Agent.ID != in.AgentID {
				return conversationError("conflict", "business_event_related_task_mismatch")
			}
			if !related.task.Terminal() {
				return conversationError("conflict", "business_event_related_task_active")
			}
		}
		requestHash := conversationHash(in)
		prepared.ID = "task_" + conversationHash([]any{"business_event", conversationOwner(in.Authority), in.IdempotencyKey})[:32]
		prepared.Status = agentsdk.ConversationTaskStatusQueued
		readExisting := func() (conversationTaskRow, error) {
			statement, args, buildErr := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(in.Authority), prepared.ID)).Build()
			if buildErr != nil {
				return conversationTaskRow{}, buildErr
			}
			return scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		}
		if existing, existingErr := readExisting(); existingErr == nil {
			if existing.requestHash != requestHash || conversationOwner(existing.authority) != conversationOwner(in.Authority) || existing.schedule != nil || existing.task.BusinessEvent == nil || conversationHash(*existing.task.BusinessEvent) != conversationHash(expectedEvent) {
				return conversationError("conflict", "business_event_task_idempotency_conflict")
			}
			out.Task, out.Replay = existing.task, true
			return nil
		} else {
			var coded *agentsdk.Error
			if !errors.As(existingErr, &coded) || coded.Code != "agent.conversation.task_not_found" {
				return existingErr
			}
		}
		if err := s.checkConversationQueueCapacity(ctx, tx, in.Authority); err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.CreatedAt, prepared.UpdatedAt = now, now
		prepared.GoalProgress = agentsdk.ConversationGoalProgress{Revision: 1, Status: agentsdk.ConversationGoalStatusActive, Phase: "queued", CompletedItems: []string{}, RemainingItems: append([]string(nil), prepared.Brief.CompletionConditions...), UpdatedAt: now}
		statement, args, buildErr := query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns(
			"record_kind", "owner_key", "workspace_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json",
		).Values(
			conversationTaskKindTask, conversationOwner(in.Authority), conversationHash([]string{in.Authority.RuntimeID, in.Authority.WorkspaceID}), prepared.ID, in.Authority.RuntimeID, prepared.SourceConversationID, "",
			prepared.Status, conversationJSON(in.Authority), requestHash, now.UnixMilli(), now.UnixMilli(), conversationJSON(prepared),
		).OnConflictDoNothing("record_kind", "owner_key", "task_id").Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 1 {
			out.Task = prepared
			return nil
		}
		if inserted != 0 {
			return conversationError("conflict", "business_event_task_idempotency_conflict")
		}
		existing, err := readExisting()
		if err != nil {
			return err
		}
		if existing.requestHash != requestHash || existing.schedule != nil || existing.task.BusinessEvent == nil || conversationHash(*existing.task.BusinessEvent) != conversationHash(expectedEvent) {
			return conversationError("conflict", "business_event_task_idempotency_conflict")
		}
		out.Task, out.Replay = existing.task, true
		return nil
	})
	return out, err
}

func equalConversationTaskTools(task agentsdk.ConversationTask, tools []string) bool {
	if len(task.ToolScope) != len(tools) {
		return false
	}
	for index, tool := range task.ToolScope {
		if tool.Key != tools[index] {
			return false
		}
	}
	return true
}

func equalConversationFollowUpScope(left, right *agentsdk.ConversationFollowUpScope) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.CompletionCondition == right.CompletionCondition
}

func (s *ConversationStore) ConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationTask{}, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(a), taskID)).Build()
	if err != nil {
		return agentsdk.ConversationTask{}, err
	}
	row, err := scanConversationTask(s.store.Database().QueryRowContext(ctx, q, args...))
	return row.task, err
}

func conversationTaskStatus(status string) bool {
	switch status {
	case "", agentsdk.ConversationTaskStatusQueued, agentsdk.ConversationTaskStatusRunning, agentsdk.ConversationTaskStatusAwaitingReview, agentsdk.ConversationTaskStatusCompleted, agentsdk.ConversationTaskStatusFailed, agentsdk.ConversationTaskStatusCancelled:
		return true
	default:
		return false
	}
}

func (s *ConversationStore) ConversationTasks(ctx context.Context, in agentsdk.ConversationTaskQuery, a agentsdk.ConversationAuthority) (agentpersistence.ConversationTaskRecordPage, error) {
	out := agentpersistence.ConversationTaskRecordPage{Items: []agentsdk.ConversationTask{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if !executionText(in.Query, 256, false) || !conversationTaskStatus(in.Status) || len(in.Cursor) > 2048 || in.Limit < 1 || in.Limit > 20 || in.SourceConversationID != "" && !personalMemoryKey(in.SourceConversationID) {
		return out, conversationError("bad_request", "task_query_invalid")
	}
	key := in
	key.Cursor = ""
	owner, hash := conversationOwner(a), conversationHash(key)
	cursor := conversationTaskCursor{Owner: owner, Query: hash, Cutoff: time.Now().UnixMilli()}
	filters := []query.Predicate{conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("owner_key", owner), query.LessThanOrEqual("created_at", cursor.Cutoff)}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || unmarshalDurableJSON(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || !personalMemoryKey(cursor.ID) || cursor.Created < 1 || cursor.Cutoff < cursor.Created {
			return out, conversationError("bad_request", "task_cursor_invalid")
		}
		filters = append(filters, query.Or(query.LessThan("created_at", cursor.Created), query.And(query.Equal("created_at", cursor.Created), query.LessThan("task_id", cursor.ID))))
	}
	if in.Status != "" {
		filters = append(filters, query.Equal("status", in.Status))
	}
	if in.SourceConversationID != "" {
		filters = append(filters, query.Equal("source_conversation_id", in.SourceConversationID))
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(filters...)).OrderBy(query.Descending("created_at"), query.Descending("task_id")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			out.Complete = false
			break
		}
		row, scanErr := scanConversationTask(rows)
		if scanErr != nil {
			return out, scanErr
		}
		scanned++
		cursor.ID, cursor.Created = row.task.ID, row.task.CreatedAt.UnixMilli()
		if strings.Contains(strings.ToLower(row.task.Goal), strings.ToLower(in.Query)) {
			out.Items = append(out.Items, row.task)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if !out.Complete {
		raw, _ := marshalDurableJSON(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}

func (s *ConversationStore) transitionQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority, resume bool) (agentsdk.ConversationTask, error) {
	var out agentsdk.ConversationTask
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(a), taskID)).Build()
		if err != nil {
			return err
		}
		row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
		if err != nil {
			return err
		}
		out = row.task
		from := out.Status
		if resume {
			if from == agentsdk.ConversationTaskStatusQueued {
				return nil
			}
			if from == agentsdk.ConversationTaskStatusAwaitingReview && out.ExecutionRunID != "" {
				appendConversationTaskPreviousRun(&out)
				out.ExecutionRunID = ""
			} else if from != agentsdk.ConversationTaskStatusCancelled || out.ExecutionRunID != "" {
				return conversationError("conflict", "task_resume_invalid")
			}
			if err = s.checkConversationQueueCapacity(ctx, tx, a); err != nil {
				return err
			}
			out.Status, out.ErrorCode, out.CompletedAt = agentsdk.ConversationTaskStatusQueued, "", nil
			out.CompletionEventID, out.CompletionEventSeq = "", 0
		} else {
			if out.Terminal() {
				return nil
			}
			if from != agentsdk.ConversationTaskStatusQueued || out.ExecutionRunID != "" {
				return conversationError("conflict", "task_cancel_invalid")
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			out.Status, out.CompletedAt = agentsdk.ConversationTaskStatusCancelled, &now
		}
		out.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		setConversationTaskGoalPhase(&out, out.UpdatedAt)
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", out.Status).Set("updated_at", out.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(conversationTaskPredicate(conversationOwner(a), taskID), query.Equal("status", from))).Build()
		return conversationCAS(ctx, tx, q, args, err)
	})
	return out, err
}

func (s *ConversationStore) CancelQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return s.transitionQueuedConversationTask(ctx, taskID, a, false)
}

func (s *ConversationStore) ResumeQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return s.transitionQueuedConversationTask(ctx, taskID, a, true)
}

func (s *ConversationStore) LaunchConversationTask(ctx context.Context, runtimeID string) (agentpersistence.ConversationTaskLaunch, bool, error) {
	var launch agentpersistence.ConversationTaskLaunch
	// The worker spends almost all of its life with an empty queue. Avoid
	// opening a serializable transaction for that case: SQLite can otherwise
	// make the read transaction contend with large event writes from an
	// unrelated foreground run. The transaction below repeats the predicate,
	// so this hint does not participate in correctness or claiming.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns("task_id").Where(query.And(conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("runtime_id", runtimeID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).Limit(1).Build()
	if err != nil {
		return launch, false, err
	}
	var queuedTaskID string
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&queuedTaskID); errors.Is(err, sql.ErrNoRows) {
		return launch, false, nil
	} else if err != nil {
		return launch, false, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		for offset := 0; ; offset += 64 {
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("runtime_id", runtimeID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).OrderBy(query.Ascending("created_at"), query.Ascending("task_id")).Limit(64).Offset(offset).Build()
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			candidates := []conversationTaskRow{}
			for rows.Next() {
				row, scanErr := scanConversationTask(rows)
				if scanErr != nil {
					_ = rows.Close()
					return scanErr
				}
				candidates = append(candidates, row)
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			for _, candidate := range candidates {
				task, authority := candidate.task, candidate.authority
				if task.ExternalExecution != nil {
					continue
				}
				if conversationAuthority(authority) != nil || authority.RuntimeID != runtimeID || task.Status != agentsdk.ConversationTaskStatusQueued || task.SourceConversationID == "" || !validStoredConversationBusinessEvent(task) || task.BusinessEvent != nil && (task.BusinessEvent.Execution.WorkspaceID != authority.WorkspaceID || task.BusinessEvent.Execution.UserID != authority.UserID || task.BusinessEvent.Execution.RoleKey != authority.RoleKey) || candidate.schedule == nil && task.BusinessEvent == nil && task.DelegationID == "" && task.SourceRunID == "" {
					continue
				}
				if candidate.schedule != nil && (!scheduledConversationStoreKey(candidate.schedule.PlanID) || !scheduledConversationStoreKey(candidate.schedule.SchedulerRunID) || candidate.schedule.ScheduledFor.IsZero()) {
					continue
				}
				sourceAuthority := authority
				if task.DelegationID != "" {
					subjects, _, err := s.delegationSubjects(ctx, tx, task.DelegationID, authority)
					if err != nil {
						return err
					}
					sourceAuthority = subjects.source
				}
				conversation, getErr := s.get(ctx, tx, task.SourceConversationID, sourceAuthority)
				if getErr != nil {
					var coded *agentsdk.Error
					if errors.As(getErr, &coded) && coded.Class == "not_found" {
						continue
					}
					return getErr
				}
				var delegation agentsdk.ConversationDelegation
				if task.DelegationID != "" {
					delegation, err = s.conversationDelegation(ctx, tx, task.DelegationID, authority)
					if err != nil {
						return err
					}
					if delegation.Status != "accepted" || delegation.TaskID != task.ID {
						continue
					}
					if task.Agent == nil || task.Agent.ID != delegation.ToAgentID || task.Brief == nil || task.Brief.Version != delegation.Brief.Version || task.ExecutionConversationID != delegation.ConversationID {
						return conversationError("conflict", "delegation_task_invalid")
					}
					conversation, err = s.get(ctx, tx, task.ExecutionConversationID, authority)
					if err != nil {
						return err
					}
				}
				if conversation.ActiveRunID != "" {
					continue
				}
				if task.SourceRunID != "" {
					if _, sourceErr := s.runRow(ctx, tx, task.SourceConversationID, task.SourceRunID, sourceAuthority); sourceErr != nil {
						return sourceErr
					}
				}
				messageText := agentsdk.ConversationTaskPrompt(task)
				now := time.Now().UTC().Truncate(time.Millisecond)
				planVersion := int64(0)
				if task.Plan != nil {
					planVersion = task.Plan.Version
				}
				generation := conversationHash([]int64{max(1, task.AgreementRevision), task.Brief.Version, int64(len(task.PreviousExecutionRuns))})[:12]
				run := conversationRunRow{Authority: authority, Run: agentsdk.ConversationRun{
					Agent:     task.Agent,
					Lifecycle: task.Lifecycle,
					ID:        conversationID("crun_"), ConversationID: conversation.ID, ClientMessageID: "background_" + task.ID + "_" + generation,
					RequestHash: conversationHash([]any{task.ID, task.Goal, task.Input, task.InputContent, task.Brief, task.AgreementRevision, task.Plan, task.ToolScope, task.Budget, task.Model, task.FollowUp}), Status: "queued", UserSeq: conversation.LastSeq + 1,
					WriteScope: &agentsdk.ConversationWriteScope{BackgroundTasks: true},
					BackgroundTask: &agentsdk.ConversationTaskExecution{
						TaskID: task.ID, Model: task.Model, BriefVersion: task.Brief.Version, AgreementRevision: max(1, task.AgreementRevision),
						Lifecycle:   task.Lifecycle,
						PlanVersion: planVersion, CompletionMode: task.CompletionMode,
						ToolScope: append([]agentsdk.ConversationTaskToolScope(nil), task.ToolScope...), Budget: task.Budget, FollowUp: task.FollowUp,
					}, CreatedAt: now, UpdatedAt: now,
				}}
				if task.DelegationID != "" {
					run.Run.BackgroundTask.Handoff = task.Handoff
					run.Run.BackgroundTask.InputSource = task.InputSource
					run.Run.BackgroundTask.Requirements = task.Requirements
					run.Run.BackgroundTask.Dependencies = task.Dependencies
					run.Run.BackgroundTask.DelegationID = task.DelegationID
					previous := delegation.Revision
					delegation.Status, delegation.Revision, delegation.UpdatedAt = "running", previous+1, now
					if err = s.saveConversationDelegation(ctx, tx, delegation, previous, authority); err != nil {
						return err
					}
				}
				contentBlocks := []agentsdk.ConversationContentBlock(nil)
				if len(task.InputContent) > 0 {
					contentBlocks = append(contentBlocks, agentsdk.ConversationContentBlock{Type: "text", Text: messageText})
					contentBlocks = append(contentBlocks, task.InputContent...)
				}
				message := agentsdk.ConversationMessage{
					ID: conversationID("msg_"), ConversationID: conversation.ID, RunID: run.Run.ID, Seq: run.Run.UserSeq,
					Role: "user", Content: messageText, ContentBlocks: contentBlocks, BackgroundTaskID: task.ID, CreatedAt: now,
				}
				// A background run joins the source history but never occupies the
				// foreground activity slot. New user messages can proceed while the
				// existing worker executes this independently.
				conversation.LastSeq = message.Seq
				if err = s.save(ctx, tx, conversation, conversation.Revision, authority); err != nil {
					return err
				}
				if err = s.insertMessage(ctx, tx, message, authority); err != nil {
					return err
				}
				if err = s.event(ctx, tx, &run, "run.queued", map[string]any{"message_id": message.ID, "message_seq": message.Seq, "background_task_id": task.ID}); err != nil {
					return err
				}
				q, args, err = query.NewInsertBuilder(s.store.Renderer(), agentRunTable).Columns("run_kind", "scope_key", "workspace_id", "run_id", "idempotency_key", "owner_key", "conversation_id", "runtime_id", "authority_json", "request_hash", "status", "lease_owner", "fencing_token", "lease_expires_at", "event_seq", "created_at", "updated_at", "payload_json").Values(agentRunKindConversation, conversationRunScopeKey(authority, conversation.ID), conversationRunWorkspaceKey(authority), run.Run.ID, run.Run.ClientMessageID, conversationOwner(authority), conversation.ID, authority.RuntimeID, conversationJSON(authority), run.Run.RequestHash, run.Run.Status, "", 0, 0, run.EventSeq, now.UnixMilli(), now.UnixMilli(), conversationJSON(run.Run)).Build()
				if err = conversationExec(ctx, tx, q, args, err); err != nil {
					return err
				}
				task.Status, task.ExecutionRunID, task.UpdatedAt = agentsdk.ConversationTaskStatusRunning, run.Run.ID, now
				setConversationTaskGoalPhase(&task, now)
				q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(authority), task.ID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).Build()
				if err = conversationCAS(ctx, tx, q, args, err); err != nil {
					return err
				}
				launch = agentpersistence.ConversationTaskLaunch{Task: task, Run: run.Run, Authority: authority}
				return nil
			}
			if len(candidates) < 64 {
				return nil
			}
		}
	})
	return launch, launch.Task.ID != "", err
}

func scheduledConversationStoreKey(value string) bool {
	if value == "" || len(value) > 191 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

func (s *ConversationStore) finishConversationTask(ctx context.Context, tx *sql.Tx, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority, resultContent string) error {
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" {
		return nil
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(authority), run.BackgroundTask.TaskID)).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	task := row.task
	executionConversationID := task.SourceConversationID
	if task.ExecutionConversationID != "" {
		executionConversationID = task.ExecutionConversationID
	}
	if executionConversationID != run.ConversationID {
		return conversationError("conflict", "task_run_changed")
	}
	if conversationOwner(row.authority) != conversationOwner(authority) || task.Status != agentsdk.ConversationTaskStatusRunning || task.ExecutionRunID != run.ID {
		return conversationError("conflict", "task_run_changed")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	task.Status, task.ResultMessageID, task.ErrorCode, task.UpdatedAt, task.CompletedAt = agentsdk.ConversationTaskStatusFailed, run.AssistantMessageID, run.ErrorCode, now, &now
	if run.Status == "completed" {
		task.Status = agentsdk.ConversationTaskStatusCompleted
		if task.DelegationID == "" && task.FollowUp == nil {
			task.Status = agentsdk.ConversationTaskStatusAwaitingReview
			task.CompletedAt = nil
			if err = s.finishConversationTaskCompletion(ctx, tx, row.authority, &task, run, resultContent); err != nil {
				return err
			}
			if task.Completion != nil && task.Completion.Verification.Ready && task.Completion.Submission.AgreementRevision == max(1, task.AgreementRevision) {
				task.Status = agentsdk.ConversationTaskStatusCompleted
				task.CompletedAt = &now
			}
		}
	} else if run.Status == "cancelled" {
		task.Status = agentsdk.ConversationTaskStatusCancelled
	}
	if err = s.closeConversationTaskPlan(ctx, tx, conversationOwner(row.authority), &task, run); err != nil {
		return err
	}
	setConversationTaskGoalPhase(&task, now)
	// Finish has already inserted run.<terminal> in this transaction. Keep its
	// stable owner-scoped event reference on the task in the same CAS write;
	// retrying Finish cannot create a second event or overwrite this receipt.
	if task.Status == agentsdk.ConversationTaskStatusAwaitingReview {
		task.CompletionEventID, task.CompletionEventSeq = "", 0
	} else {
		task.CompletionEventSeq = run.LastEventSeq
		task.CompletionEventID = "task_event_" + conversationHash([]any{task.ID, run.ID, run.LastEventSeq, run.Status})[:32]
	}
	row.task = task
	if task, err = s.recordConversationFollowUpTerminal(ctx, tx, row, run, resultContent, now); err != nil {
		return err
	}
	if err = s.finishConversationDelegation(ctx, tx, task, run, authority); err != nil {
		return err
	}
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(row.authority), task.ID), query.Equal("status", agentsdk.ConversationTaskStatusRunning))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) resumeConversationTask(ctx context.Context, tx *sql.Tx, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority) error {
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" || run.Status != "queued" {
		return nil
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(authority), run.BackgroundTask.TaskID)).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	task := row.task
	if task.Status == agentsdk.ConversationTaskStatusRunning {
		return nil
	}
	if task.ExecutionRunID != run.ID || task.Status != agentsdk.ConversationTaskStatusFailed && task.Status != agentsdk.ConversationTaskStatusCancelled {
		return conversationError("conflict", "task_resume_invalid")
	}
	from := task.Status
	task.Status, task.ErrorCode, task.CompletedAt, task.ResultMessageID = agentsdk.ConversationTaskStatusRunning, "", nil, ""
	task.CompletionEventID, task.CompletionEventSeq, task.UpdatedAt = "", 0, time.Now().UTC().Truncate(time.Millisecond)
	setConversationTaskGoalPhase(&task, task.UpdatedAt)
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", task.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(authority), task.ID), query.Equal("status", from))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

var _ agentpersistence.ConversationTaskMutationRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskWorkerRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskReadRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskControlRepository = (*ConversationStore)(nil)
