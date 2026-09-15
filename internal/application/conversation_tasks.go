package application

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) prepareConversationTask(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationTask, error) {
	var start agentsdk.ConversationTaskStart
	if in.Call.Name != "task_start" || json.Unmarshal([]byte(in.Call.Arguments), &start) != nil {
		return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_start_invalid")
	}
	task, err := s.prepareConversationTaskStart(ctx, start, in.Authority, in.ConversationID, in.RunID, nil)
	if err != nil {
		return task, err
	}
	task.InputContent, err = s.inheritedConversationRunImages(ctx, in.ConversationID, in.RunID, 0, in.Authority)
	if err != nil {
		return task, err
	}
	if err = s.requireConversationImageModel(ctx, task.InputContent); err != nil {
		return task, err
	}
	return task, nil
}

func (s *ConversationService) prepareConversationTaskStart(ctx context.Context, start agentsdk.ConversationTaskStart, authority agentsdk.ConversationAuthority, conversationID, sourceRunID string, allowedActions map[string]struct{}) (agentsdk.ConversationTask, error) {
	if !conversationText(start.Goal, 2048, true) || !conversationText(start.Input, 8192, false) ||
		len(start.AllowedTools) > 128 || start.Budget.MaxSteps < 1 || start.Budget.MaxSteps > min(32, s.options.MaxSteps) ||
		start.Budget.MaxToolCalls < 1 || start.Budget.MaxToolCalls > min(32, s.options.MaxToolCalls) ||
		start.Budget.MaxOutputBytes < 256 || start.Budget.MaxOutputBytes > min(65536, s.options.MaxOutputBytes) ||
		start.Budget.TimeoutSeconds < 1 || time.Duration(start.Budget.TimeoutSeconds)*time.Second > min(30*time.Minute, s.options.RunTimeout) {
		return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_start_invalid")
	}
	if start.FollowUp != nil {
		if allowedActions == nil || !conversationText(start.FollowUp.CompletionCondition, 2048, true) {
			return agentsdk.ConversationTask{}, conversationFailure("bad_request", "follow_up_scope_invalid")
		}
		start.FollowUp.CompletionCondition = strings.TrimSpace(start.FollowUp.CompletionCondition)
	}
	brief := agentsdk.DefaultConversationTaskBrief(start.Goal)
	completionMode := agentsdk.ConversationTaskCompletionModeLegacyResponse
	if start.Brief != nil {
		completionMode = agentsdk.ConversationTaskCompletionModeAssessed
		brief = *start.Brief
		brief.ExplicitFields = append([]string(nil), brief.ExplicitFields...)
		brief.InferredFields = append([]string(nil), brief.InferredFields...)
		sort.Strings(brief.ExplicitFields)
		sort.Strings(brief.InferredFields)
		if brief.Version != 1 || brief.Goal != start.Goal || !validConversationBrief(brief) || !conversationText(brief.Audience, 512, true) || !validConversationBriefProvenance(brief, true) {
			return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_brief_invalid")
		}
		if brief.DueAt != nil {
			due := brief.DueAt.UTC().Truncate(time.Millisecond)
			brief.DueAt = &due
		}
	}
	task := agentsdk.ConversationTask{
		Goal: start.Goal, Input: start.Input, Budget: start.Budget, FollowUp: start.FollowUp, Brief: &brief, AgreementRevision: 1,
		Lifecycle:            cloneConversationLifecycleManifest(s.lifecycleManifest),
		CompletionMode:       completionMode,
		SourceConversationID: conversationID, SourceRunID: sourceRunID,
		ToolScope: make([]agentsdk.ConversationTaskToolScope, 0, len(start.AllowedTools)),
	}
	fallbackKey, fallbackEffort := "default", ""
	if agent := selectedConversationAgent(ctx); agent != nil {
		fallbackKey, fallbackEffort = agent.ModelKey, agent.ReasoningEffort
	}
	if start.Model != nil || s.conversationModelByKey(fallbackKey) != nil {
		selection, err := s.resolveConversationModelSelection(start.Model, fallbackKey, fallbackEffort)
		if err != nil || selection.Identity.Fingerprint == "" {
			if err != nil {
				return agentsdk.ConversationTask{}, err
			}
			return agentsdk.ConversationTask{}, conversationFailure("bad_request", "agent_model_unavailable")
		}
		task.Model = selection
	}
	if !conversationText(agentsdk.ConversationTaskPrompt(task), s.options.MaxInputBytes, true) {
		return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_input_exceeded")
	}
	var definitions []agentsdk.ConversationToolDefinition
	var catalog map[string]conversationCompiledTool
	var err error
	if allowedActions == nil {
		definitions, catalog, err = s.executionCatalog(ctx, authority)
	} else {
		// A scheduled plan is already bounded by exact Action keys. Load the
		// registered catalog first, then inspect availability only for that
		// selected scope so an unrelated disconnected account cannot block it.
		definitions, catalog, err = s.registeredExecutionCatalog(ctx, authority)
	}
	if err != nil {
		return agentsdk.ConversationTask{}, err
	}
	if allowedActions != nil && len(start.AllowedTools) == 0 {
		matchedActions := make(map[string]struct{}, len(allowedActions))
		for _, definition := range definitions {
			if _, allowed := allowedActions[definition.ActionKey]; allowed && !strings.HasPrefix(definition.Key, "task_") {
				start.AllowedTools = append(start.AllowedTools, definition.Key)
				matchedActions[definition.ActionKey] = struct{}{}
			}
		}
		if len(matchedActions) != len(allowedActions) {
			return agentsdk.ConversationTask{}, conversationFailure("forbidden", "scheduled_action_denied")
		}
		if len(start.AllowedTools) > 16 {
			return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_scope_invalid")
		}
	}
	seen := map[string]bool{}
	for _, key := range start.AllowedTools {
		if strings.HasPrefix(key, "task_") || key == "plan_update" || key == "completion_submit" || seen[key] {
			return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_scope_invalid")
		}
		seen[key] = true
		tool, exists := catalog[key]
		if !exists {
			return agentsdk.ConversationTask{}, conversationFailure("forbidden", "tool_access_denied")
		}
		if allowedActions != nil {
			if _, allowed := allowedActions[tool.definition.ActionKey]; !allowed {
				return agentsdk.ConversationTask{}, conversationFailure("forbidden", "scheduled_action_denied")
			}
			ready, availabilityErr := s.conversationToolAvailable(ctx, authority, key)
			if availabilityErr != nil {
				return agentsdk.ConversationTask{}, availabilityErr
			}
			if !ready {
				return agentsdk.ConversationTask{}, conversationFailure("forbidden", "tool_access_denied")
			}
		}
		auth, authErr := s.authorizeConversationTool(ctx, s.options.ToolHost, agentsdk.ConversationToolRequest{
			Authority: authority, ConversationID: conversationID, RunID: sourceRunID, CorrelationID: sourceRunID,
			Call: agentsdk.ConversationToolCall{Name: key}, Definition: tool.definition,
		})
		if authErr != nil {
			return agentsdk.ConversationTask{}, authErr
		}
		if !auth.Granted {
			return agentsdk.ConversationTask{}, conversationFailure("forbidden", "tool_access_denied")
		}
		task.ToolScope = append(task.ToolScope, agentsdk.ConversationTaskToolScope{
			Key: key, Version: tool.definition.Version, ActionKey: tool.definition.ActionKey,
			DefinitionHash: conversationDigest(tool.definition), AuthorizationRevision: strings.TrimSpace(auth.Revision),
		})
	}
	return task, nil
}

func (s *ConversationService) StartScheduledConversationTask(ctx context.Context, in agentsdk.ScheduledConversationTaskRequest) (agentsdk.ScheduledConversationTaskReceipt, error) {
	if !agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience) {
		return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("forbidden", "scheduled_task_service_action_required")
	}
	if in.ContractVersion != agentsdk.ScheduledConversationTaskContractVersion || !scheduledConversationKey(in.PlanID) ||
		!scheduledConversationKey(in.SchedulerRunID) || !scheduledConversationKey(in.IdempotencyKey) || in.ScheduledFor.IsZero() ||
		!conversationKey(in.ConversationID) || in.SourceRunID != "" && !conversationKey(in.SourceRunID) ||
		in.Authority.RuntimeID != "" && in.Authority.RuntimeID != s.runtimeID || len(in.AllowedActions) > 64 {
		return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("bad_request", "scheduled_task_invalid")
	}
	in.Authority.RuntimeID = s.runtimeID
	if err := s.authorize(in.Authority); err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, err
	}
	actions := make(map[string]struct{}, len(in.AllowedActions))
	for _, action := range in.AllowedActions {
		if !scheduledConversationKey(action) {
			return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("bad_request", "scheduled_task_invalid")
		}
		if _, duplicate := actions[action]; duplicate {
			return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("bad_request", "scheduled_task_invalid")
		}
		actions[action] = struct{}{}
	}
	if in.Input.Budget == (agentsdk.ConversationTaskBudget{}) {
		in.Input.Budget = agentsdk.ConversationTaskBudget{
			MaxSteps: min(12, s.options.MaxSteps), MaxToolCalls: min(12, s.options.MaxToolCalls),
			MaxOutputBytes: min(8192, s.options.MaxOutputBytes),
			TimeoutSeconds: int(min(5*time.Minute, s.options.RunTimeout) / time.Second),
		}
	}
	if in.Input.FollowUp != nil && s.options.FollowUpPublisher == nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("unavailable", "follow_up_notifications_unavailable")
	}
	if err := s.authorizeConversationExecution(ctx, in.ConversationID, "", "schedule", in.Authority); err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, err
	}
	conversation, err := s.repo.Get(ctx, in.ConversationID, in.Authority)
	if err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, err
	}
	var executionAgent *agentsdk.ConversationAgentSnapshot
	if agentID := s.conversationExecutionAgentID(conversation); agentID != "" {
		executionAgent, err = s.freezeConversationAgent(ctx, agentID, in.Authority)
		if err != nil {
			return agentsdk.ScheduledConversationTaskReceipt{}, err
		}
		ctx, err = s.selectConversationAgent(ctx, executionAgent, in.Authority)
		if err != nil {
			return agentsdk.ScheduledConversationTaskReceipt{}, err
		}
	}
	task, err := s.prepareConversationTaskStart(ctx, in.Input, in.Authority, in.ConversationID, in.SourceRunID, actions)
	if err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, err
	}
	task.Agent = executionAgent
	in.ScheduledFor = in.ScheduledFor.UTC().Truncate(time.Millisecond)
	var requestModel *agentsdk.ConversationModelRequestSelection
	if task.Model != nil {
		requestModel = &agentsdk.ConversationModelRequestSelection{Key: task.Model.Key, ReasoningEffort: task.Model.ReasoningEffort}
	}
	in.Input = agentsdk.ConversationTaskStart{Goal: task.Goal, Input: task.Input, AllowedTools: conversationTaskAllowedTools(task), Budget: task.Budget, Model: requestModel, Brief: task.Brief, FollowUp: task.FollowUp}
	repo, ok := s.repo.(persistence.ScheduledConversationTaskMutationRepository)
	if !ok {
		return agentsdk.ScheduledConversationTaskReceipt{}, conversationFailure("unavailable", "scheduled_tasks_unavailable")
	}
	receipt, err := repo.AcceptScheduledConversationTask(ctx, in, task)
	if err == nil {
		s.signalConversationTasks()
	}
	return receipt, err
}

func (s *ConversationService) AcceptBusinessEventConversationTask(ctx context.Context, in agentsdk.BusinessEventConversationTaskRequest) (agentsdk.BusinessEventConversationTaskReceipt, error) {
	if !agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentBusinessEventConversationTaskAccept, agentsdk.AgentRuntimeServiceAudience) {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("forbidden", "business_event_task_service_action_required")
	}
	if in.ContractVersion != agentsdk.BusinessEventConversationTaskContractVersion || !scheduledConversationKey(in.IdempotencyKey) ||
		!conversationKey(in.ConversationID) || !scheduledConversationKey(in.AgentID) || !scheduledConversationKey(in.Source.EventID) ||
		!scheduledConversationKey(in.Source.Provider) || !scheduledConversationKey(in.Source.EventType) || !conversationText(in.Source.ExternalID, 1024, true) || in.Source.ReceivedAt.IsZero() ||
		!scheduledConversationKey(in.Rule.Key) || !conversationSHA256(in.Rule.Revision) || in.Authority.RuntimeID != "" && in.Authority.RuntimeID != s.runtimeID {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "business_event_task_invalid")
	}
	switch in.Mode {
	case "start":
		if in.RelatedTaskID != "" {
			return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "business_event_task_invalid")
		}
	case "wake":
		if !conversationKey(in.RelatedTaskID) {
			return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "business_event_task_invalid")
		}
	default:
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "business_event_task_invalid")
	}
	in.Authority.RuntimeID = s.runtimeID
	if err := s.authorize(in.Authority); err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	if in.Input.FollowUp != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "business_event_follow_up_invalid")
	}
	if in.Input.Budget == (agentsdk.ConversationTaskBudget{}) {
		in.Input.Budget = agentsdk.ConversationTaskBudget{
			MaxSteps: min(12, s.options.MaxSteps), MaxToolCalls: min(12, s.options.MaxToolCalls),
			MaxOutputBytes: min(8192, s.options.MaxOutputBytes), TimeoutSeconds: int(min(5*time.Minute, s.options.RunTimeout) / time.Second),
		}
	}
	if err := s.authorizeConversationExecution(ctx, in.ConversationID, "", "business_event", in.Authority); err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	conversation, err := s.repo.Get(ctx, in.ConversationID, in.Authority)
	if err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	if s.conversationExecutionAgentID(conversation) != in.AgentID {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("conflict", "business_event_agent_mismatch")
	}
	executionAgent, err := s.freezeConversationAgent(ctx, in.AgentID, in.Authority)
	if err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	ctx, err = s.selectConversationAgent(ctx, executionAgent, in.Authority)
	if err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	if in.Mode == "wake" {
		related, relatedErr := s.conversationTaskRecord(ctx, in.RelatedTaskID, in.Authority)
		if relatedErr != nil {
			return agentsdk.BusinessEventConversationTaskReceipt{}, relatedErr
		}
		if related.SourceConversationID != in.ConversationID || related.Agent == nil || related.Agent.ID != in.AgentID {
			return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("conflict", "business_event_related_task_mismatch")
		}
		if !related.Terminal() {
			return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("conflict", "business_event_related_task_active")
		}
	}
	task, err := s.prepareConversationTaskStart(ctx, in.Input, in.Authority, in.ConversationID, "", nil)
	if err != nil {
		return agentsdk.BusinessEventConversationTaskReceipt{}, err
	}
	task.Agent = executionAgent
	in.Source.ReceivedAt = in.Source.ReceivedAt.UTC().Truncate(time.Millisecond)
	task.BusinessEvent = &agentsdk.ConversationTaskBusinessEvent{
		Source: in.Source, Rule: in.Rule, Execution: agentsdk.ConversationBusinessEventExecutionIdentity{WorkspaceID: in.Authority.WorkspaceID, UserID: in.Authority.UserID, RoleKey: in.Authority.RoleKey}, IdempotencyKey: in.IdempotencyKey, Mode: in.Mode,
		TargetAgentID: in.AgentID, RelatedTaskID: in.RelatedTaskID,
	}
	if !conversationText(agentsdk.ConversationTaskPrompt(task), s.options.MaxInputBytes, true) {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("bad_request", "task_input_exceeded")
	}
	var requestModel *agentsdk.ConversationModelRequestSelection
	if task.Model != nil {
		requestModel = &agentsdk.ConversationModelRequestSelection{Key: task.Model.Key, ReasoningEffort: task.Model.ReasoningEffort}
	}
	in.Input = agentsdk.ConversationTaskStart{Goal: task.Goal, Input: task.Input, AllowedTools: conversationTaskAllowedTools(task), Budget: task.Budget, Model: requestModel, Brief: task.Brief}
	repo, ok := s.repo.(persistence.BusinessEventConversationTaskMutationRepository)
	if !ok {
		return agentsdk.BusinessEventConversationTaskReceipt{}, conversationFailure("unavailable", "business_event_tasks_unavailable")
	}
	receipt, err := repo.AcceptBusinessEventConversationTask(ctx, in, task)
	if err == nil {
		s.signalConversationTasks()
	}
	return receipt, err
}

func conversationSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}

func scheduledConversationKey(value string) bool {
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

var _ agentsdk.ScheduledConversationTaskService = (*ConversationService)(nil)
var _ agentsdk.BusinessEventConversationTaskService = (*ConversationService)(nil)

func conversationTaskFailureCode(err error) string {
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		return strings.TrimPrefix(coded.Code, "agent.conversation.")
	}
	return "task_start_failed"
}

func conversationTaskAllowedTools(task agentsdk.ConversationTask) []string {
	out := make([]string, 0, len(task.ToolScope))
	for _, tool := range task.ToolScope {
		out = append(out, tool.Key)
	}
	return out
}

func conversationTaskExecutionConversation(task agentsdk.ConversationTask) string {
	if task.ExecutionConversationID != "" {
		return task.ExecutionConversationID
	}
	return task.SourceConversationID
}

func conversationTaskProgress(run agentsdk.ConversationRun) agentsdk.ConversationTaskProgress {
	out := agentsdk.ConversationTaskProgress{RunStatus: run.Status, Attempt: run.Attempt, Steps: run.Metrics.Steps, ModelCalls: run.Metrics.ModelCalls, ToolCalls: run.Metrics.ToolCalls, DurationMilliseconds: run.DurationMilliseconds, LastEventSeq: run.LastEventSeq}
	if out.Steps == 0 {
		out.Steps = len(run.Steps)
	}
	if !run.AuditComplete && out.ToolCalls == 0 {
		for _, step := range run.Steps {
			out.ToolCalls += len(step.Calls)
		}
	}
	if input, output, known := usageTokenCounts(run.Usage); known {
		out.InputTokens, out.OutputTokens = input, output
	}
	return out
}

func conversationTaskDiagnostic(task agentsdk.ConversationTask, progress agentsdk.ConversationGoalProgress, run *agentsdk.ConversationRun, allocation agentsdk.ConversationWorkAllocation, dependencies []agentsdk.ConversationDependencyState) agentsdk.ConversationProgressDiagnostic {
	attempts := len(task.PreviousExecutionRuns)
	if task.ExecutionRunID != "" {
		attempts++
	}
	if run != nil {
		attempts += max(0, run.Attempt-1)
	}
	out := agentsdk.ConversationProgressDiagnostic{
		State: "progressing", Action: "continue", Reasons: []string{}, ExecutionAttempts: attempts,
		AgreementRevisions: max(1, task.AgreementRevision), RepeatedToolCalls: allocation.RepeatedToolCalls,
		VerifiedItems: len(progress.CompletedItems), RemainingItems: len(progress.RemainingItems),
		Basis: "Server-derived from current task agreement, program verification, saved run attempts, exact tool-call fingerprints, dependency revisions and the shared work ledger.",
	}
	planBlocked := ""
	if task.Plan != nil {
		for _, step := range task.Plan.Steps {
			switch step.Status {
			case agentsdk.ConversationPlanStepCompleted:
				if len(step.Evidence) > 0 || len(step.Artifacts) > 0 {
					out.StageOutcomes++
				}
			case agentsdk.ConversationPlanStepSkipped:
			default:
				out.RemainingStages++
			}
			if step.Status == agentsdk.ConversationPlanStepBlocked && planBlocked == "" {
				planBlocked = step.Blocker
				if planBlocked == "" {
					planBlocked = step.Title
				}
			}
		}
	}
	for _, dependency := range dependencies {
		if dependency.State != "current" {
			out.OpenDependencies++
		}
	}
	if progress.Status == agentsdk.ConversationGoalStatusCompleted {
		out.State, out.Action = "completed", "none"
		return out
	}
	if run != nil && run.Waiting() {
		out.State, out.Action = "waiting", "wait"
		out.Reasons = append(out.Reasons, "waiting_for_input_or_confirmation")
		return out
	}
	if out.OpenDependencies > 0 {
		out.State, out.Action = "blocked", "report_blocker"
		out.Reasons = append(out.Reasons, "dependency_revision_requires_review")
		return out
	}
	if planBlocked != "" {
		out.State, out.Action = "blocked", "report_blocker"
		out.Reasons = append(out.Reasons, "plan_blocker:"+planBlocked)
		return out
	}
	if progress.Blocker != "" {
		out.State, out.Action = "blocked", "report_blocker"
		out.Reasons = append(out.Reasons, "blocker:"+progress.Blocker)
		return out
	}
	if progress.Status == agentsdk.ConversationGoalStatusPaused {
		out.State, out.Action = "waiting", "wait"
		out.Reasons = append(out.Reasons, "task_paused")
		return out
	}
	if allocation.RepeatedToolCalls > 0 {
		out.Reasons = append(out.Reasons, "repeated_tool_calls")
	}
	if out.AgreementRevisions >= 3 {
		out.Reasons = append(out.Reasons, "repeated_agreement_revisions")
	}
	if attempts >= 3 && out.VerifiedItems == 0 && out.StageOutcomes == 0 && out.RemainingItems > 0 {
		out.Reasons = append(out.Reasons, "no_verified_progress_across_attempts")
	}
	if len(out.Reasons) > 0 {
		out.State, out.Action = "watch", "adjust_strategy"
	}
	return out
}

func conversationTaskGoalProgress(task agentsdk.ConversationTask, run *agentsdk.ConversationRun) agentsdk.ConversationGoalProgress {
	brief := task.Brief
	if brief == nil {
		value := agentsdk.DefaultConversationTaskBrief(task.Goal)
		brief = &value
	}
	out := task.GoalProgress
	if out.Revision < 1 {
		out = agentsdk.ConversationGoalProgress{Revision: 1, CompletedItems: []string{}, RemainingItems: append([]string(nil), brief.CompletionConditions...), UpdatedAt: task.UpdatedAt}
	}
	if out.CompletedItems == nil {
		out.CompletedItems = []string{}
	}
	if out.RemainingItems == nil {
		out.RemainingItems = append([]string(nil), brief.CompletionConditions...)
	}
	out.Status, out.Phase, out.Blocker = agentsdk.ConversationGoalStatusActive, "queued", ""
	switch task.Status {
	case agentsdk.ConversationTaskStatusRunning:
		out.Phase = "executing"
	case agentsdk.ConversationTaskStatusAwaitingReview:
		out.Status, out.Phase, out.Blocker = agentsdk.ConversationGoalStatusBlocked, "awaiting_review", "completion_review_required"
		if task.Completion != nil {
			out.CompletedItems, out.RemainingItems = []string{}, []string{}
			for _, check := range task.Completion.Verification.Checks {
				if check.Verdict == "met" && check.Method != "recipient" && check.Method != "pending" {
					out.CompletedItems = append(out.CompletedItems, check.Requirement)
				} else {
					out.RemainingItems = append(out.RemainingItems, check.Requirement)
				}
			}
		}
	case agentsdk.ConversationTaskStatusCompleted:
		out.Status, out.Phase = agentsdk.ConversationGoalStatusCompleted, "completed"
		out.CompletedItems, out.RemainingItems = append([]string(nil), brief.CompletionConditions...), []string{}
	case agentsdk.ConversationTaskStatusCancelled:
		out.Status, out.Phase = agentsdk.ConversationGoalStatusPaused, "paused"
	case agentsdk.ConversationTaskStatusFailed:
		out.Status, out.Phase, out.Blocker = agentsdk.ConversationGoalStatusBlocked, "failed", task.ErrorCode
		if conversationTaskBudgetError(task.ErrorCode) {
			out.Status, out.Phase = agentsdk.ConversationGoalStatusBudgetExhausted, "budget_exhausted"
		}
	}
	if run != nil && run.Waiting() && run.Interaction != nil {
		out.Status, out.Phase, out.Blocker = agentsdk.ConversationGoalStatusBlocked, "waiting_"+run.Interaction.Kind, run.Interaction.Question
		out.UpdatedAt = run.UpdatedAt
	}
	return out
}

func conversationDelegationGoalProgress(status string, progress agentsdk.ConversationGoalProgress) agentsdk.ConversationGoalProgress {
	switch status {
	case "awaiting_delivery":
		progress.Status, progress.Phase, progress.Blocker = agentsdk.ConversationGoalStatusBlocked, "awaiting_delivery", "delivery_submission_required"
	case "delivered":
		progress.Status, progress.Phase, progress.Blocker = agentsdk.ConversationGoalStatusBlocked, "awaiting_review", "delivery_review_required"
	case "needs_changes":
		progress.Status, progress.Phase, progress.Blocker = agentsdk.ConversationGoalStatusBlocked, "needs_changes", "delivery_changes_required"
	case "failed":
		progress.Status, progress.Phase, progress.Blocker = agentsdk.ConversationGoalStatusBlocked, "failed", "delegation_failed"
	case "paused", "cancelled":
		progress.Status, progress.Phase, progress.Blocker = agentsdk.ConversationGoalStatusPaused, "paused", ""
	}
	return progress
}

func conversationTaskBudgetError(code string) bool {
	switch code {
	case "execution_limit", "work_budget_exhausted", "work_budget_provider_usage_exceeded", "agent.task.cost_budget_exceeded", "agent.task.tool_call_limit":
		return true
	default:
		return false
	}
}

func conversationTaskWaiting(interaction *agentsdk.ConversationInteraction) *agentsdk.ConversationTaskWaiting {
	if interaction == nil || interaction.Status != "pending" {
		return nil
	}
	return &agentsdk.ConversationTaskWaiting{ID: interaction.ID, Kind: interaction.Kind, Question: interaction.Question, Tool: interaction.Tool, Revision: interaction.Revision, ExpiresAt: interaction.ExpiresAt}
}

func conversationTaskControlState(task agentsdk.ConversationTask, run *agentsdk.ConversationRun) agentsdk.ConversationTaskControlState {
	if task.ExternalExecution != nil {
		external := task.ExternalExecution
		control := agentsdk.ConversationTaskControlState{}
		if task.Status == agentsdk.ConversationTaskStatusQueued || task.Status == agentsdk.ConversationTaskStatusRunning {
			control.CanCancel = external.Capabilities.Cancellation
			if !external.Capabilities.Cancellation {
				control.ResumeBlocker = "external_agent_cancellation_unsupported"
			}
		}
		if task.Status == agentsdk.ConversationTaskStatusCancelled {
			switch {
			case !external.Capabilities.Resume:
				control.ResumeBlocker = "external_agent_resume_unsupported"
			case !external.StopAcknowledged:
				control.ResumeBlocker = "external_agent_stop_unconfirmed"
			case external.EffectState == "unknown":
				control.ResumeBlocker = "delegation_reconciliation_required"
			default:
				control.CanResume = true
			}
		}
		return control
	}
	if run == nil {
		return agentsdk.ConversationTaskControlState{
			CanCancel: task.Status == agentsdk.ConversationTaskStatusQueued,
			CanResume: task.Status == agentsdk.ConversationTaskStatusCancelled,
		}
	}
	if run.AccessError != "" {
		return agentsdk.ConversationTaskControlState{ResumeBlocker: run.AccessError}
	}
	control := agentsdk.ConversationTaskControlState{}
	switch run.Status {
	case "queued", "running":
		control.CanCancel = !task.Terminal()
	case "waiting_user", "waiting_confirmation":
		control.CanCancel = !task.Terminal()
		control.ResumeBlocker = "interaction_response_required"
	case "needs_reconciliation":
		control.CanCancel = !task.Terminal()
		control.CanResume = true
	case "failed", "cancelled":
		if interaction := run.Interaction; interaction != nil && interaction.Kind != "reconciliation" && (interaction.Status == "cancelled" || interaction.Status == "expired" || interaction.Status == "rejected") {
			control.ResumeBlocker = "interaction_closed"
		} else {
			control.CanResume = true
		}
	case "completed":
		control.CanResume = task.Status == agentsdk.ConversationTaskStatusAwaitingReview
	}
	return control
}

func (s *ConversationService) conversationTaskArtifacts(ctx context.Context, task agentsdk.ConversationTask, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationArtifact, bool, bool, error) {
	items := []agentsdk.ConversationArtifact{}
	if task.ExecutionRunID == "" {
		return items, true, false, nil
	}
	cursor := ""
	for len(items) < 50 {
		page, err := s.Artifacts(ctx, agentsdk.ConversationArtifactQuery{SourceConversationID: conversationTaskExecutionConversation(task), Cursor: cursor, Limit: 50}, a)
		if err != nil {
			var coded *agentsdk.Error
			if errors.As(err, &coded) && (coded.Class == "forbidden" || coded.Class == "unavailable") {
				return items, false, true, nil
			}
			return nil, false, false, err
		}
		for _, artifact := range page.Items {
			if artifact.SourceRunID == task.ExecutionRunID {
				items = append(items, artifact)
				if len(items) == 50 {
					return items, page.Complete, page.Omitted, nil
				}
			}
		}
		if page.Complete {
			return items, true, page.Omitted, nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return nil, false, false, conversationFailure("unavailable", "artifact_page_invalid")
		}
		cursor = page.NextCursor
	}
	return items, false, false, nil
}

func (s *ConversationService) projectConversationTask(ctx context.Context, task agentsdk.ConversationTask, a agentsdk.ConversationAuthority, detail bool) (agentsdk.ConversationTaskDetail, error) {
	brief := task.Brief
	if brief == nil {
		value := agentsdk.DefaultConversationTaskBrief(task.Goal)
		brief = &value
	}
	summary := agentsdk.ConversationTaskSummary{
		ID: task.ID, Model: task.Model, ExternalExecution: task.ExternalExecution, Status: task.Status, Goal: task.Goal, Brief: brief, AgreementRevision: max(1, task.AgreementRevision), GoalProgress: conversationTaskGoalProgress(task, nil), Plan: task.Plan, CompletionMode: task.CompletionMode, Completion: task.Completion, BusinessEvent: task.BusinessEvent, AllowedTools: conversationTaskAllowedTools(task), Budget: task.Budget,
		SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, ExecutionRunID: task.ExecutionRunID, PreviousExecutionRuns: append([]agentsdk.ConversationRunReference(nil), task.PreviousExecutionRuns...),
		DelegationID: task.DelegationID, ExecutionConversationID: task.ExecutionConversationID,
		Artifacts: []agentsdk.ConversationArtifact{}, ArtifactsComplete: true, CompletionEventID: task.CompletionEventID,
		CompletionEventSeq: task.CompletionEventSeq, ErrorCode: task.ErrorCode, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, CompletedAt: task.CompletedAt,
	}
	summary.Control = conversationTaskControlState(task, nil)
	if task.ExternalExecution != nil {
		summary.Progress.Attempt = task.ExternalExecution.Attempt
		summary.Progress.RunStatus = task.ExternalExecution.Status
		summary.Progress.LastEventSeq = task.ExternalExecution.LastEventSeq
		if task.ExternalExecution.ClaimedAt != nil {
			summary.Progress.DurationMilliseconds = task.ExternalExecution.UpdatedAt.Sub(*task.ExternalExecution.ClaimedAt).Milliseconds()
		}
	}
	if task.Agent != nil {
		summary.AgentID = task.Agent.ID
		summary.AgentRevision = task.Agent.Revision
		summary.AgentPromptVersion = conversationAgentPromptVersion(task.Agent)
		if len(task.Agent.Skills) > 0 {
			summary.SkillVersions = make(map[string]string, len(task.Agent.Skills))
			for _, skill := range task.Agent.Skills {
				summary.SkillVersions[skill.Key] = skill.Version
			}
		}
	}
	out := agentsdk.ConversationTaskDetail{ConversationTaskSummary: summary, Input: task.Input, Steps: []agentsdk.ConversationStepView{}}
	if detail && out.Completion != nil {
		if completionErr := s.checkConversationTaskCompletionSources(ctx, *out.Completion, a); completionErr != nil {
			out.Completion = nil
			redacted := task
			redacted.Completion = nil
			out.GoalProgress = conversationTaskGoalProgress(redacted, nil)
			out.AccessError = sourceAccessCode(completionErr)
		}
	}
	superseded := false
	delegationStatus := ""
	var allocation agentsdk.ConversationWorkAllocation
	var dependencies []agentsdk.ConversationDependencyState
	if task.DelegationID != "" {
		repo, err := s.collaborationRepository()
		if err != nil {
			return out, err
		}
		d, err := repo.ConversationDelegation(ctx, task.DelegationID, a)
		if err != nil {
			return out, err
		}
		delegationStatus = d.Status
		superseded = d.TaskID != task.ID || d.ConversationID != conversationTaskExecutionConversation(task) || max(1, task.AgreementRevision) != d.AgreementRevision || len(d.PendingChanges) > 0
		if work, ok := s.repo.(persistence.ConversationWorkAccountingRepository); ok {
			budget, total, workErr := work.ConversationWorkBudget(ctx, d.ID, a)
			if workErr != nil {
				return out, workErr
			}
			allocation, workErr = work.ConversationWorkAllocation(ctx, d.ID, a)
			if workErr != nil {
				return out, workErr
			}
			out.Work = &agentsdk.ConversationTaskWorkSummary{Budget: budget, TotalUsage: total, Allocation: allocation}
		}
		dependencies, err = s.dependencyStates(ctx, d, a)
		if err != nil {
			return out, err
		}
		if superseded {
			out.Control = agentsdk.ConversationTaskControlState{ResumeBlocker: "delegation_superseded"}
		}
	}
	if task.ExecutionRunID == "" {
		out.GoalProgress = conversationDelegationGoalProgress(delegationStatus, out.GoalProgress)
		out.Diagnostic = conversationTaskDiagnostic(task, out.GoalProgress, nil, allocation, dependencies)
		return out, nil
	}
	out.Diagnostic = conversationTaskDiagnostic(task, out.GoalProgress, nil, allocation, dependencies)
	run, err := s.Run(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, a)
	if err != nil {
		var coded *agentsdk.Error
		if errors.As(err, &coded) && (coded.Class == "not_found" || coded.Class == "forbidden") {
			out.AccessError = sourceAccessCode(err)
			return out, nil
		}
		return out, err
	}
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID != task.ID {
		return out, conversationFailure("conflict", "task_run_invalid")
	}
	out.Progress = conversationTaskProgress(run)
	out.GoalProgress = conversationTaskGoalProgress(task, &run)
	out.GoalProgress = conversationDelegationGoalProgress(delegationStatus, out.GoalProgress)
	out.Diagnostic = conversationTaskDiagnostic(task, out.GoalProgress, &run, allocation, dependencies)
	out.Control = conversationTaskControlState(task, &run)
	if superseded {
		out.Control = agentsdk.ConversationTaskControlState{ResumeBlocker: "delegation_superseded"}
	}
	if run.AccessError != "" {
		out.AccessError = run.AccessError
		return out, nil
	}
	if run.Waiting() {
		out.Waiting = conversationTaskWaiting(run.Interaction)
	}
	if detail {
		out.Steps = append(out.Steps, run.Steps...)
		if run.Waiting() {
			out.Interaction = run.Interaction
		}
	}
	if task.ResultMessageID != "" {
		history, ok := s.repo.(persistence.ConversationHistoryRepository)
		if !ok {
			return out, conversationFailure("unavailable", "task_result_unavailable")
		}
		message, messageErr := history.HistoryMessage(ctx, conversationTaskExecutionConversation(task), task.ResultMessageID, a)
		if messageErr != nil {
			return out, messageErr
		}
		if message.RunID != task.ExecutionRunID || message.Role != "assistant" || message.BackgroundTaskID != task.ID {
			return out, conversationFailure("conflict", "task_result_invalid")
		}
		preview := truncateUTF8(message.Content, 4096)
		out.Result = &agentsdk.ConversationTaskResult{MessageID: message.ID, Preview: preview, Bytes: len(message.Content), Complete: len(preview) == len(message.Content)}
	}
	out.Artifacts, out.ArtifactsComplete, out.ArtifactsOmitted, err = s.conversationTaskArtifacts(ctx, task, a)
	return out, err
}

func (s *ConversationService) ConversationTask(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskDetail, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	if !conversationKey(id) {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("bad_request", "task_id_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "tasks_unavailable")
	}
	task, err := repo.ConversationTask(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	if err = s.authorizeCollaborationTask(ctx, task, "execution_read", a); err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	return s.projectConversationTask(ctx, task, a, true)
}

func (s *ConversationService) ConversationTasks(ctx context.Context, in agentsdk.ConversationTaskQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskPage, error) {
	out := agentsdk.ConversationTaskPage{Items: []agentsdk.ConversationTaskSummary{}, Complete: true}
	if err := s.authorize(a); err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return out, conversationFailure("unavailable", "tasks_unavailable")
	}
	page, err := repo.ConversationTasks(ctx, in, a)
	if err != nil {
		return out, err
	}
	for _, task := range page.Items {
		if err := s.authorizeCollaborationTask(ctx, task, "execution_read", a); err != nil {
			if collaborationDenied(err) {
				continue
			}
			return agentsdk.ConversationTaskPage{}, err
		}
		view, projectErr := s.projectConversationTask(ctx, task, a, false)
		if projectErr != nil {
			return out, projectErr
		}
		out.Items = append(out.Items, view.ConversationTaskSummary)
	}
	out.NextCursor, out.Complete = page.NextCursor, page.Complete
	return out, nil
}

func (s *ConversationService) conversationTaskRecord(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationTask{}, err
	}
	if !conversationKey(id) {
		return agentsdk.ConversationTask{}, conversationFailure("bad_request", "task_id_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return agentsdk.ConversationTask{}, conversationFailure("unavailable", "tasks_unavailable")
	}
	return repo.ConversationTask(ctx, id, a)
}

func (s *ConversationService) authorizeConversationTaskControl(ctx context.Context, id, key string, a agentsdk.ConversationAuthority) error {
	if s.options.PersonalAuthorizer == nil {
		return conversationFailure("unavailable", "task_control_unavailable")
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.BackgroundTaskControlConversationTools() {
		if tool.Key == key {
			definition = tool
		}
	}
	raw, _ := json.Marshal(map[string]string{"id": id})
	auth, err := s.authorizeConversationTool(ctx, s.options.PersonalAuthorizer, agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: key, Arguments: string(raw)}})
	if err != nil {
		return err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	return nil
}

func (s *ConversationService) CancelConversationTask(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskDetail, error) {
	return s.cancelConversationTask(ctx, id, a, true)
}

func (s *ConversationService) cancelConversationTask(ctx context.Context, id string, a agentsdk.ConversationAuthority, authorize bool) (agentsdk.ConversationTaskDetail, error) {
	if authorize {
		if err := s.authorizeConversationTaskControl(ctx, id, "task_cancel", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	task, err := s.conversationTaskRecord(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	for _, operation := range []string{"manage", "execution_read"} {
		if err = s.authorizeCollaborationTask(ctx, task, operation, a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}

	if task.Terminal() {
		return s.projectConversationTask(ctx, task, a, true)
	}
	if task.Status == agentsdk.ConversationTaskStatusAwaitingReview {
		if err = s.authorizeConversationExecution(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, "resume", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
		controls, ok := s.repo.(persistence.ConversationTaskControlRepository)
		if !ok {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_control_unavailable")
		}
		task, err = controls.ResumeQueuedConversationTask(ctx, id, a)
		if err == nil {
			s.signalConversationTasks()
		}
	} else if task.ExecutionRunID == "" {
		controls, ok := s.repo.(persistence.ConversationTaskControlRepository)
		if !ok {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_control_unavailable")
		}
		task, err = controls.CancelQueuedConversationTask(ctx, id, a)
		if err == nil && task.Status == agentsdk.ConversationTaskStatusCancelled {
			s.dispatchConversationQueuedTaskFinishedLifecycle(ctx, task, a)
		}
	} else {
		_, err = s.Cancel(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, a)
		if err == nil {
			task, err = s.conversationTaskRecord(ctx, id, a)
		}
	}
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	return s.projectConversationTask(ctx, task, a, true)
}

func (s *ConversationService) ResumeConversationTask(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskDetail, error) {
	return s.resumeConversationTask(ctx, id, a, true)
}

func (s *ConversationService) resumeConversationTask(ctx context.Context, id string, a agentsdk.ConversationAuthority, authorize bool) (agentsdk.ConversationTaskDetail, error) {
	if authorize {
		if err := s.authorizeConversationTaskControl(ctx, id, "task_resume", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	task, err := s.conversationTaskRecord(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	for _, operation := range []string{"manage", "execution_read"} {
		if err = s.authorizeCollaborationTask(ctx, task, operation, a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}

	if task.Status == agentsdk.ConversationTaskStatusCompleted {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "task_completed")
	}
	if task.Status == agentsdk.ConversationTaskStatusAwaitingReview {
		if err = s.authorizeConversationExecution(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, "resume", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
		controls, ok := s.repo.(persistence.ConversationTaskControlRepository)
		if !ok {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_control_unavailable")
		}
		task, err = controls.ResumeQueuedConversationTask(ctx, id, a)
		if err == nil {
			s.signalConversationTasks()
		}
	} else if task.ExecutionRunID == "" {
		if err = s.authorizeConversationExecution(ctx, task.SourceConversationID, "", "resume", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
		controls, ok := s.repo.(persistence.ConversationTaskControlRepository)
		if !ok {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_control_unavailable")
		}
		task, err = controls.ResumeQueuedConversationTask(ctx, id, a)
		if err == nil {
			s.signalConversationTasks()
		}
	} else {
		run, runErr := s.Run(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, a)
		if runErr != nil {
			return agentsdk.ConversationTaskDetail{}, runErr
		}
		if run.Status == "waiting_user" || run.Status == "waiting_confirmation" {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "interaction_response_required")
		}
		if task.Status == agentsdk.ConversationTaskStatusRunning && run.Status != "needs_reconciliation" {
			return s.projectConversationTask(ctx, task, a, true)
		}
		_, err = s.Resume(ctx, conversationTaskExecutionConversation(task), task.ExecutionRunID, a)
		if err == nil {
			task, err = s.conversationTaskRecord(ctx, id, a)
		}
	}
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	return s.projectConversationTask(ctx, task, a, true)
}

var _ agentsdk.ConversationTaskControlService = (*ConversationService)(nil)

func (s *ConversationService) executionCatalogForRun(ctx context.Context, claim persistence.ConversationClaim) ([]agentsdk.ConversationToolDefinition, map[string]conversationCompiledTool, error) {
	if claim.Run.BackgroundTask == nil {
		definitions, catalog, err := s.executionCatalog(ctx, claim.Authority)
		if err != nil {
			return nil, nil, err
		}
		definitions = slices.DeleteFunc(definitions, func(definition agentsdk.ConversationToolDefinition) bool {
			return definition.Key == "plan_update" || definition.Key == "completion_submit"
		})
		delete(catalog, "plan_update")
		delete(catalog, "completion_submit")
		return definitions, catalog, nil
	}
	if len(claim.Run.BackgroundTask.Requirements.Sources) > 0 {
		var err error
		ctx, err = s.delegationRunSourceContext(ctx, claim.Run, claim.Authority)
		if err != nil {
			return nil, nil, err
		}
		audit := s.sourceAudit(claim.Authority, claim.Run.ConversationID)
		for _, ref := range claim.Run.BackgroundTask.Requirements.Sources {
			if _, err := audit.run(ctx, ref); err != nil {
				return nil, nil, err
			}
		}
	}
	definitions, catalog, err := s.registeredExecutionCatalog(ctx, claim.Authority)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]agentsdk.ConversationTaskToolScope, len(claim.Run.BackgroundTask.ToolScope)+1)
	for _, scope := range claim.Run.BackgroundTask.ToolScope {
		if strings.HasPrefix(scope.Key, "task_") || scope.Key == "" || scope.Version == "" || scope.ActionKey == "" || scope.DefinitionHash == "" {
			return nil, nil, conversationFailure("conflict", "task_scope_invalid")
		}
		if _, duplicate := allowed[scope.Key]; duplicate {
			return nil, nil, conversationFailure("conflict", "task_scope_invalid")
		}
		tool, exists := catalog[scope.Key]
		if !exists {
			return nil, nil, conversationFailure("forbidden", "tool_access_denied")
		}
		if tool.definition.Version != scope.Version || tool.definition.ActionKey != scope.ActionKey || conversationDigest(tool.definition) != scope.DefinitionHash {
			return nil, nil, conversationFailure("conflict", "tool_changed")
		}
		allowed[scope.Key] = scope
	}
	if tool, exists := catalog["plan_update"]; exists {
		allowed["plan_update"] = agentsdk.ConversationTaskToolScope{Key: tool.definition.Key, Version: tool.definition.Version, ActionKey: tool.definition.ActionKey, DefinitionHash: conversationDigest(tool.definition)}
	}
	if tool, exists := catalog["skill_load"]; exists {
		allowed["skill_load"] = agentsdk.ConversationTaskToolScope{Key: tool.definition.Key, Version: tool.definition.Version, ActionKey: tool.definition.ActionKey, DefinitionHash: conversationDigest(tool.definition)}
	}
	if claim.Run.BackgroundTask.CompletionMode == agentsdk.ConversationTaskCompletionModeAssessed && claim.Run.BackgroundTask.DelegationID == "" && claim.Run.BackgroundTask.FollowUp == nil {
		if tool, exists := catalog["completion_submit"]; exists {
			allowed["completion_submit"] = agentsdk.ConversationTaskToolScope{Key: tool.definition.Key, Version: tool.definition.Version, ActionKey: tool.definition.ActionKey, DefinitionHash: conversationDigest(tool.definition)}
		}
	}
	filteredDefinitions := make([]agentsdk.ConversationToolDefinition, 0, len(allowed))
	filteredCatalog := make(map[string]conversationCompiledTool, len(allowed))
	for _, definition := range definitions {
		if _, ok := allowed[definition.Key]; ok {
			filteredDefinitions = append(filteredDefinitions, definition)
			filteredCatalog[definition.Key] = catalog[definition.Key]
		}
	}
	if len(filteredDefinitions) != len(allowed) {
		return nil, nil, conversationFailure("conflict", "task_scope_invalid")
	}
	// Plan, completion and Skill loading are Agent-owned run metadata/configuration and have no external connection.
	// Availability checks apply only to the task's selected business tools.
	internalDefinitions := []agentsdk.ConversationToolDefinition{}
	external := make([]agentsdk.ConversationToolDefinition, 0, len(filteredDefinitions))
	for index := range filteredDefinitions {
		if filteredDefinitions[index].Key == "plan_update" || filteredDefinitions[index].Key == "completion_submit" || filteredDefinitions[index].Key == "skill_load" {
			internalDefinitions = append(internalDefinitions, filteredDefinitions[index])
			continue
		}
		external = append(external, filteredDefinitions[index])
	}
	visible, available, err := s.availableExecutionCatalog(ctx, claim.Authority, external, filteredCatalog)
	if err != nil {
		return nil, nil, err
	}
	for _, definition := range internalDefinitions {
		visible = append(visible, definition)
		available[definition.Key] = catalog[definition.Key]
	}
	return visible, available, nil
}

func (s *ConversationService) conversationRunLimits(claim persistence.ConversationClaim) (steps, calls, output int, timeout time.Duration) {
	steps, calls, output, timeout = s.options.MaxSteps, s.options.MaxToolCalls, s.options.MaxOutputBytes, s.options.RunTimeout
	if agent := claim.Run.Agent; agent != nil {
		limits := agent.Profile.ExecutionLimits
		if limits.MaxSteps > 0 {
			steps = min(steps, limits.MaxSteps)
		}
		if limits.MaxToolCalls > 0 {
			calls = min(calls, limits.MaxToolCalls)
		}
		if limits.MaxOutputBytes > 0 {
			output = min(output, limits.MaxOutputBytes)
		}
		if limits.TimeoutSeconds > 0 {
			timeout = min(timeout, time.Duration(limits.TimeoutSeconds)*time.Second)
		}
	}
	if task := claim.Run.BackgroundTask; task != nil {
		steps = min(steps, task.Budget.MaxSteps)
		calls = min(calls, task.Budget.MaxToolCalls)
		output = min(output, task.Budget.MaxOutputBytes)
		timeout = min(timeout, time.Duration(task.Budget.TimeoutSeconds)*time.Second)
	}
	return
}

func (s *ConversationService) signalConversationTasks() {
	select {
	case s.taskWake <- struct{}{}:
	default:
	}
}

func (s *ConversationService) signalConversationFollowUps() {
	select {
	case s.followUpWake <- struct{}{}:
	default:
	}
}

func (s *ConversationService) conversationFollowUpWorker(ctx context.Context, repo persistence.ConversationFollowUpEventRepository) {
	defer s.wg.Done()
	retry := max(s.options.Poll, time.Second)
	for ctx.Err() == nil {
		claim, found, err := repo.ClaimConversationFollowUpEvent(ctx, s.runtimeID, s.owner, s.options.Lease)
		if err == nil && found {
			publishCtx, cancel := s.externalCallContext(ctx, min(30*time.Second, s.options.RunTimeout))
			err = s.options.FollowUpPublisher.PublishConversationFollowUp(publishCtx, claim.Event)
			cancel()
			if err == nil {
				err = repo.CompleteConversationFollowUpEvent(ctx, claim)
			} else {
				_ = repo.ReleaseConversationFollowUpEvent(context.WithoutCancel(ctx), claim)
			}
			if err == nil {
				continue
			}
		}
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			slog.Warn("conversation follow-up notification failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.followUpWake:
		case <-time.After(retry):
		}
	}
}

func (s *ConversationService) conversationTaskWorker(ctx context.Context, repo persistence.ConversationTaskWorkerRepository) {
	defer s.wg.Done()
	// Startup performs an immediate recovery attempt. After that, task_start and
	// foreground completion are the durable state transitions that can make a
	// queued task launchable, and both signal this worker. An empty queue waits
	// without touching storage so it cannot compete with foreground SQLite
	// writes. Only a storage failure needs a timed retry.
	retry := max(s.options.Poll, time.Second)
	for ctx.Err() == nil {
		_, launched, err := repo.LaunchConversationTask(ctx, s.runtimeID)
		if err == nil && !launched {
			if peers, ok := s.repo.(persistence.ConversationCollaborationRepository); ok {
				if lifecycle, ok := s.repo.(persistence.ConversationPeerLifecycleRepository); ok {
					_, launched, err = lifecycle.LaunchConversationPeerMessageWithLifecycle(ctx, s.runtimeID, cloneConversationLifecycleManifest(s.lifecycleManifest))
				} else {
					_, launched, err = peers.LaunchConversationPeerMessage(ctx, s.runtimeID)
				}
			}
		}
		if err == nil && launched {
			s.signal()
			continue
		}
		if err != nil && !errors.Is(err, context.Canceled) && ctx.Err() == nil {
			slog.Warn("conversation task launch failed", "error", err)
		}
		if err == nil {
			select {
			case <-ctx.Done():
				return
			case <-s.taskWake:
			case <-time.After(2 * time.Second):
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-s.taskWake:
			case <-time.After(retry):
			}
		}
	}
}
