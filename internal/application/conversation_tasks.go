package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	return s.prepareConversationTaskStart(ctx, start, in.Authority, in.ConversationID, in.RunID, nil)
}

func (s *ConversationService) prepareConversationTaskStart(ctx context.Context, start agentsdk.ConversationTaskStart, authority agentsdk.ConversationAuthority, conversationID, sourceRunID string, allowedActions map[string]struct{}) (agentsdk.ConversationTask, error) {
	if !conversationText(start.Goal, 2048, true) || !conversationText(start.Input, 8192, false) ||
		len(start.AllowedTools) > 16 || start.Budget.MaxSteps < 1 || start.Budget.MaxSteps > min(32, s.options.MaxSteps) ||
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
	task := agentsdk.ConversationTask{
		Goal: start.Goal, Input: start.Input, Budget: start.Budget, FollowUp: start.FollowUp,
		SourceConversationID: conversationID, SourceRunID: sourceRunID,
		ToolScope: make([]agentsdk.ConversationTaskToolScope, 0, len(start.AllowedTools)),
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
		if strings.HasPrefix(key, "task_") || seen[key] {
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
		auth, authErr := s.options.ToolHost.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{
			Authority: authority, ConversationID: conversationID, RunID: sourceRunID,
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
	task, err := s.prepareConversationTaskStart(ctx, in.Input, in.Authority, in.ConversationID, in.SourceRunID, actions)
	if err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, err
	}
	in.ScheduledFor = in.ScheduledFor.UTC().Truncate(time.Millisecond)
	in.Input = agentsdk.ConversationTaskStart{Goal: task.Goal, Input: task.Input, AllowedTools: conversationTaskAllowedTools(task), Budget: task.Budget, FollowUp: task.FollowUp}
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

func conversationTaskProgress(run agentsdk.ConversationRun) agentsdk.ConversationTaskProgress {
	out := agentsdk.ConversationTaskProgress{RunStatus: run.Status, Attempt: run.Attempt, Steps: len(run.Steps), LastEventSeq: run.LastEventSeq}
	for _, step := range run.Steps {
		out.ToolCalls += len(step.Calls)
	}
	return out
}

func conversationTaskWaiting(interaction *agentsdk.ConversationInteraction) *agentsdk.ConversationTaskWaiting {
	if interaction == nil || interaction.Status != "pending" {
		return nil
	}
	return &agentsdk.ConversationTaskWaiting{ID: interaction.ID, Kind: interaction.Kind, Question: interaction.Question, Tool: interaction.Tool, Revision: interaction.Revision, ExpiresAt: interaction.ExpiresAt}
}

func conversationTaskControlState(task agentsdk.ConversationTask, run *agentsdk.ConversationRun) agentsdk.ConversationTaskControlState {
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
		page, err := s.Artifacts(ctx, agentsdk.ConversationArtifactQuery{SourceConversationID: task.SourceConversationID, Cursor: cursor, Limit: 50}, a)
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
	summary := agentsdk.ConversationTaskSummary{
		ID: task.ID, Status: task.Status, Goal: task.Goal, AllowedTools: conversationTaskAllowedTools(task), Budget: task.Budget,
		SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, ExecutionRunID: task.ExecutionRunID,
		Artifacts: []agentsdk.ConversationArtifact{}, ArtifactsComplete: true, CompletionEventID: task.CompletionEventID,
		CompletionEventSeq: task.CompletionEventSeq, ErrorCode: task.ErrorCode, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, CompletedAt: task.CompletedAt,
	}
	summary.Control = conversationTaskControlState(task, nil)
	out := agentsdk.ConversationTaskDetail{ConversationTaskSummary: summary, Input: task.Input, Steps: []agentsdk.ConversationStepView{}}
	if task.ExecutionRunID == "" {
		return out, nil
	}
	run, err := s.Run(ctx, task.SourceConversationID, task.ExecutionRunID, a)
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
	out.Control = conversationTaskControlState(task, &run)
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
		message, messageErr := history.HistoryMessage(ctx, task.SourceConversationID, task.ResultMessageID, a)
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
	auth, err := s.options.PersonalAuthorizer.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: key, Arguments: string(raw)}})
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
	if task.Terminal() {
		return s.projectConversationTask(ctx, task, a, true)
	}
	if task.ExecutionRunID == "" {
		controls, ok := s.repo.(persistence.ConversationTaskControlRepository)
		if !ok {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_control_unavailable")
		}
		task, err = controls.CancelQueuedConversationTask(ctx, id, a)
	} else {
		_, err = s.Cancel(ctx, task.SourceConversationID, task.ExecutionRunID, a)
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
	if task.Status == agentsdk.ConversationTaskStatusCompleted {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "task_completed")
	}
	if task.ExecutionRunID == "" {
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
		run, runErr := s.Run(ctx, task.SourceConversationID, task.ExecutionRunID, a)
		if runErr != nil {
			return agentsdk.ConversationTaskDetail{}, runErr
		}
		if run.Status == "waiting_user" || run.Status == "waiting_confirmation" {
			return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "interaction_response_required")
		}
		if task.Status == agentsdk.ConversationTaskStatusRunning && run.Status != "needs_reconciliation" {
			return s.projectConversationTask(ctx, task, a, true)
		}
		_, err = s.Resume(ctx, task.SourceConversationID, task.ExecutionRunID, a)
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
		return s.executionCatalog(ctx, claim.Authority)
	}
	definitions, catalog, err := s.registeredExecutionCatalog(ctx, claim.Authority)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]agentsdk.ConversationTaskToolScope, len(claim.Run.BackgroundTask.ToolScope))
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
	return s.availableExecutionCatalog(ctx, claim.Authority, filteredDefinitions, filteredCatalog)
}

func (s *ConversationService) conversationRunLimits(claim persistence.ConversationClaim) (steps, calls, output int, timeout time.Duration) {
	steps, calls, output, timeout = s.options.MaxSteps, s.options.MaxToolCalls, s.options.MaxOutputBytes, s.options.RunTimeout
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
			publishCtx, cancel := context.WithTimeout(ctx, min(30*time.Second, s.options.RunTimeout))
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
