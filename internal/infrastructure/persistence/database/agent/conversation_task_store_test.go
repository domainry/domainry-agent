package agent

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func TestScheduledConversationTaskAcceptanceIsIdempotentOwnerScopedAndUsesExistingWorkerQueue(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "scheduled-conversation", Title: "每周整理"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	request := agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion,
		PlanID:          "plan-weekly", SchedulerRunID: "scheduler-run-1", IdempotencyKey: "window-2026-w38", ScheduledFor: time.Now().UTC().Truncate(time.Millisecond),
		Authority: authority, ConversationID: conversation.ID,
		Input: agentsdk.ConversationTaskStart{Goal: "整理本周待办", Input: `{"status":"open"}`, AllowedTools: []string{}, Budget: budget}, AllowedActions: []string{},
	}
	prepared := agentsdk.ConversationTask{Goal: request.Input.Goal, Input: request.Input.Input, Budget: budget, SourceConversationID: conversation.ID, ToolScope: []agentsdk.ConversationTaskToolScope{}}
	first, err := repo.AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil || first.Replay || first.Task.ID == "" || first.Task.Status != agentsdk.ConversationTaskStatusQueued || first.Task.SourceRunID != "" {
		t.Fatalf("first receipt=%+v err=%v", first, err)
	}
	replayed, err := NewConversationStore(store).AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil || !replayed.Replay || replayed.Task.ID != first.Task.ID {
		t.Fatalf("replayed receipt=%+v err=%v", replayed, err)
	}
	changed := request
	changed.Input.Goal = "changed"
	changedPrepared := prepared
	changedPrepared.Goal = changed.Input.Goal
	if _, err = repo.AcceptScheduledConversationTask(t.Context(), changed, changedPrepared); err == nil {
		t.Fatal("changed scheduled replay was accepted")
	}
	var planID, schedulerRunID string
	var scheduledFor int64
	if err = store.Database().QueryRowContext(t.Context(), `SELECT scheduled_plan_id, scheduler_run_id, scheduled_for FROM _agent_conversation_tasks WHERE owner_key = ? AND task_id = ?`, conversationOwner(authority), first.Task.ID).Scan(&planID, &schedulerRunID, &scheduledFor); err != nil || planID != request.PlanID || schedulerRunID != request.SchedulerRunID || scheduledFor != request.ScheduledFor.UnixMilli() {
		t.Fatalf("scheduled metadata=%q/%q/%d err=%v", planID, schedulerRunID, scheduledFor, err)
	}
	other := request
	other.Authority.UserID = "other"
	if _, err = repo.AcceptScheduledConversationTask(t.Context(), other, prepared); err == nil {
		t.Fatal("cross-owner scheduled acceptance succeeded")
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), authority.RuntimeID)
	if err != nil || !launched || launch.Task.ID != first.Task.ID || launch.Run.BackgroundTask == nil || launch.Run.BackgroundTask.TaskID != first.Task.ID {
		t.Fatalf("scheduled launch=%+v launched=%v err=%v", launch, launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), authority.RuntimeID, "scheduled-worker", time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("scheduled claim=%+v found=%v err=%v", claim, found, err)
	}
}

func TestConversationTaskEffectReceiptLaunchAndCompletionAreDurable(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	start := agentsdk.ConversationTaskStart{Goal: "核对发布", Input: "build 42", AllowedTools: []string{"time_now"}, Budget: budget}
	arguments, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	claim, request := personalMutationFixture(t, repo, "task-atomic", "task_start", string(arguments), true)
	var timeDefinition agentsdk.ConversationToolDefinition
	for _, definition := range agentsdk.PersonalConversationTools() {
		if definition.Key == "time_now" {
			timeDefinition = definition
		}
	}
	prepared := agentsdk.ConversationTask{
		Goal: start.Goal, Input: start.Input, Budget: budget, SourceConversationID: request.ConversationID, SourceRunID: request.RunID,
		ToolScope: []agentsdk.ConversationTaskToolScope{{Key: timeDefinition.Key, Version: timeDefinition.Version, ActionKey: timeDefinition.ActionKey, DefinitionHash: conversationHash(timeDefinition), AuthorizationRevision: "auth-v1"}},
	}
	if _, err = store.Database().ExecContext(t.Context(), `CREATE TRIGGER fail_task_receipt BEFORE INSERT ON _agent_conversation_events WHEN CAST(NEW.payload_json AS TEXT) LIKE '%tool.completed%' BEGIN SELECT RAISE(ABORT, 'injected task receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyConversationTaskTool(t.Context(), request, prepared); err == nil {
		t.Fatal("injected transaction failure was ignored")
	}
	var count int
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_tasks`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("task survived receipt rollback: count=%d err=%v", count, err)
	}
	ledger, err := repo.ExecutionTools(t.Context(), claim, 0)
	if err != nil || len(ledger) != 1 || ledger[0].State != "started" {
		t.Fatalf("tool receipt survived rollback: %+v %v", ledger, err)
	}
	if _, err = store.Database().ExecContext(t.Context(), `DROP TRIGGER fail_task_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ApplyConversationTaskTool(t.Context(), request, prepared)
	if err != nil || result.Status != "completed" || result.Completion != "accepted" || result.ResourceID == "" {
		t.Fatalf("task_start result=%+v err=%v", result, err)
	}
	replayed, err := NewConversationStore(store).ApplyConversationTaskTool(t.Context(), request, prepared)
	if err != nil || conversationHash(replayed) != conversationHash(result) {
		t.Fatalf("durable task receipt changed: %+v %v", replayed, err)
	}
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_tasks`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("task replay duplicated work: count=%d err=%v", count, err)
	}
	queued, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || queued.Status != agentsdk.ConversationTaskStatusQueued || queued.SourceConversationID != request.ConversationID || queued.SourceRunID != request.RunID || len(queued.ToolScope) != 1 || queued.ToolScope[0].DefinitionHash == "" || queued.Budget != budget {
		t.Fatalf("queued task=%+v err=%v", queued, err)
	}
	cancelledQueued, err := repo.CancelQueuedConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || cancelledQueued.Status != agentsdk.ConversationTaskStatusCancelled || cancelledQueued.CompletedAt == nil {
		t.Fatalf("cancelled queued task=%+v err=%v", cancelledQueued, err)
	}
	if again, replayErr := repo.CancelQueuedConversationTask(t.Context(), result.ResourceID, request.Authority); replayErr != nil || again.Status != agentsdk.ConversationTaskStatusCancelled {
		t.Fatalf("queued cancellation was not idempotent: %+v %v", again, replayErr)
	}
	queued, err = repo.ResumeQueuedConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || queued.Status != agentsdk.ConversationTaskStatusQueued || queued.CompletedAt != nil {
		t.Fatalf("resumed queued task=%+v err=%v", queued, err)
	}
	other := request.Authority
	other.UserID = "other"
	if _, err = repo.ConversationTask(t.Context(), result.ResourceID, other); err == nil {
		t.Fatal("cross-owner task read succeeded")
	}
	if _, launched, err := repo.LaunchConversationTask(t.Context(), request.Authority.RuntimeID); err != nil || launched {
		t.Fatalf("task launched while source conversation was active: launched=%v err=%v", launched, err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, request.Call.ID, result); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "已创建后台任务"}, ""); err != nil {
		t.Fatal(err)
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), request.Authority.RuntimeID)
	if err != nil || !launched || launch.Task.ID != result.ResourceID || launch.Run.BackgroundTask == nil || launch.Run.BackgroundTask.TaskID != result.ResourceID || launch.Run.BackgroundTask.Budget != budget || len(launch.Run.BackgroundTask.ToolScope) != 1 || launch.Run.BackgroundTask.ToolScope[0].Key != "time_now" {
		t.Fatalf("launch=%+v launched=%v err=%v", launch, launched, err)
	}
	running, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || running.Status != agentsdk.ConversationTaskStatusRunning || running.ExecutionRunID != launch.Run.ID {
		t.Fatalf("running task=%+v err=%v", running, err)
	}
	page, err := repo.Messages(t.Context(), request.ConversationID, agentsdk.ConversationMessageQuery{Limit: 20}, request.Authority)
	if err != nil || len(page.Items) != 3 || page.Items[2].RunID != launch.Run.ID || page.Items[2].BackgroundTaskID != result.ResourceID || page.Items[2].Content != agentsdk.ConversationTaskPrompt(running) {
		t.Fatalf("background input provenance=%+v err=%v", page.Items, err)
	}
	child, found, err := repo.Claim(t.Context(), request.Authority.RuntimeID, "task-worker", time.Minute)
	if err != nil || !found || child.Run.ID != launch.Run.ID {
		t.Fatalf("child claim=%+v found=%v err=%v", child, found, err)
	}
	if err = repo.Finish(t.Context(), child, agentsdk.ConversationModelResult{Content: "build 42 已核对"}, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || completed.Status != agentsdk.ConversationTaskStatusCompleted || completed.ResultMessageID == "" || completed.CompletedAt == nil || completed.CompletionEventID == "" || completed.CompletionEventSeq < 1 {
		t.Fatalf("completed task=%+v err=%v", completed, err)
	}
	var terminalEvents int
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_events WHERE conversation_id = ? AND run_id = ? AND CAST(payload_json AS TEXT) LIKE '%"type":"run.completed"%'`, request.ConversationID, launch.Run.ID).Scan(&terminalEvents); err != nil || terminalEvents != 1 {
		t.Fatalf("terminal events=%d err=%v", terminalEvents, err)
	}
	if err = repo.Finish(t.Context(), child, agentsdk.ConversationModelResult{Content: "build 42 已核对"}, ""); err == nil {
		t.Fatal("completed claim replay unexpectedly succeeded")
	}
	again, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || again.CompletionEventID != completed.CompletionEventID || again.CompletionEventSeq != completed.CompletionEventSeq {
		t.Fatalf("completion receipt changed after replay: before=%+v after=%+v err=%v", completed, again, err)
	}
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_events WHERE conversation_id = ? AND run_id = ? AND CAST(payload_json AS TEXT) LIKE '%"type":"run.completed"%'`, request.ConversationID, launch.Run.ID).Scan(&terminalEvents); err != nil || terminalEvents != 1 {
		t.Fatalf("terminal event replayed: count=%d err=%v", terminalEvents, err)
	}
	page, err = repo.Messages(t.Context(), request.ConversationID, agentsdk.ConversationMessageQuery{Limit: 20}, request.Authority)
	if err != nil || len(page.Items) != 4 || page.Items[3].BackgroundTaskID != result.ResourceID || page.Items[3].ID != completed.ResultMessageID {
		t.Fatalf("background result provenance=%+v err=%v", page.Items, err)
	}
}

func TestConversationTasksOwnerScopedFiltersAndStableCursor(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	insert := func(index int, status, goal, conversation string, owner agentsdk.ConversationAuthority) {
		t.Helper()
		task := agentsdk.ConversationTask{ID: fmt.Sprintf("task_%02d", index), Status: status, Goal: goal, Input: "input", SourceConversationID: conversation, SourceRunID: fmt.Sprintf("run_%02d", index), CreatedAt: now.Add(time.Duration(index) * time.Millisecond), UpdatedAt: now.Add(time.Duration(index) * time.Millisecond)}
		_, err := store.Database().ExecContext(t.Context(), `INSERT INTO _agent_conversation_tasks(owner_key, task_id, runtime_id, source_conversation_id, source_run_id, status, authority_json, request_hash, created_at, updated_at, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, conversationOwner(owner), task.ID, owner.RuntimeID, task.SourceConversationID, task.SourceRunID, task.Status, conversationJSON(owner), conversationHash(task.ID), task.CreatedAt.UnixMilli(), task.UpdatedAt.UnixMilli(), conversationJSON(task))
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(1, agentsdk.ConversationTaskStatusCompleted, "核对 Alpha", "conv_a", authority)
	insert(2, agentsdk.ConversationTaskStatusRunning, "核对 Beta", "conv_a", authority)
	insert(3, agentsdk.ConversationTaskStatusCompleted, "整理 Alpha", "conv_b", authority)
	other := authority
	other.UserID = "other"
	insert(4, agentsdk.ConversationTaskStatusCompleted, "核对 Alpha", "conv_a", other)

	first, err := repo.ConversationTasks(t.Context(), agentsdk.ConversationTaskQuery{Query: "alpha", Status: agentsdk.ConversationTaskStatusCompleted, Limit: 1}, authority)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "task_03" || first.Complete || first.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	second, err := repo.ConversationTasks(t.Context(), agentsdk.ConversationTaskQuery{Query: "alpha", Status: agentsdk.ConversationTaskStatusCompleted, Limit: 1, Cursor: first.NextCursor}, authority)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != "task_01" || !second.Complete || second.NextCursor != "" {
		t.Fatalf("second page=%+v err=%v", second, err)
	}
	current, err := repo.ConversationTasks(t.Context(), agentsdk.ConversationTaskQuery{SourceConversationID: "conv_a", Limit: 20}, authority)
	if err != nil || len(current.Items) != 2 {
		t.Fatalf("conversation scope=%+v err=%v", current, err)
	}
	if _, err = repo.ConversationTasks(t.Context(), agentsdk.ConversationTaskQuery{Status: "unknown"}, authority); err == nil {
		t.Fatal("invalid status accepted")
	}
	if _, err = repo.ConversationTasks(t.Context(), agentsdk.ConversationTaskQuery{Query: "alpha", Status: agentsdk.ConversationTaskStatusCompleted, Limit: 2, Cursor: first.NextCursor}, authority); err == nil {
		t.Fatal("cursor was accepted with changed query contract")
	}
}

func TestConversationTaskChildCountIsBoundedPerSourceRun(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 1, MaxToolCalls: 1, MaxOutputBytes: 256, TimeoutSeconds: 1}
	start := agentsdk.ConversationTaskStart{Goal: "第五个子任务", Input: "", AllowedTools: []string{}, Budget: budget}
	arguments, _ := json.Marshal(start)
	_, request := personalMutationFixture(t, repo, "task-child-limit", "task_start", string(arguments), true)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index := 0; index < agentsdk.ConversationTaskMaxChildrenPerRun; index++ {
		task := agentsdk.ConversationTask{ID: fmt.Sprintf("task_child_%d", index), Status: agentsdk.ConversationTaskStatusQueued, Goal: "child", Input: "", SourceConversationID: request.ConversationID, SourceRunID: request.RunID, CreatedAt: now, UpdatedAt: now}
		if _, err := store.Database().ExecContext(t.Context(), `INSERT INTO _agent_conversation_tasks(owner_key, task_id, runtime_id, source_conversation_id, source_run_id, status, authority_json, request_hash, created_at, updated_at, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, conversationOwner(request.Authority), task.ID, request.Authority.RuntimeID, task.SourceConversationID, task.SourceRunID, task.Status, conversationJSON(request.Authority), conversationHash(task.ID), now.UnixMilli(), now.UnixMilli(), conversationJSON(task)); err != nil {
			t.Fatal(err)
		}
	}
	prepared := agentsdk.ConversationTask{Goal: start.Goal, Input: start.Input, Budget: budget, SourceConversationID: request.ConversationID, SourceRunID: request.RunID, ToolScope: []agentsdk.ConversationTaskToolScope{}}
	result, err := repo.ApplyConversationTaskTool(t.Context(), request, prepared)
	if err != nil || result.Status != "failed" || result.ErrorCode != "task_child_limit" || result.ResourceID != "" {
		t.Fatalf("child limit result=%+v err=%v", result, err)
	}
	var count int
	if err := store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_tasks WHERE owner_key = ? AND source_run_id = ?`, conversationOwner(request.Authority), request.RunID).Scan(&count); err != nil || count != agentsdk.ConversationTaskMaxChildrenPerRun {
		t.Fatalf("child count=%d err=%v", count, err)
	}
}

func TestConversationTaskFailureTracksTerminalRun(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 1, MaxToolCalls: 1, MaxOutputBytes: 256, TimeoutSeconds: 1}
	start := agentsdk.ConversationTaskStart{Goal: "失败验证", Input: "", AllowedTools: []string{}, Budget: budget}
	arguments, _ := json.Marshal(start)
	claim, request := personalMutationFixture(t, repo, "task-failure", "task_start", string(arguments), true)
	prepared := agentsdk.ConversationTask{Goal: start.Goal, Input: start.Input, Budget: budget, SourceConversationID: request.ConversationID, SourceRunID: request.RunID, ToolScope: []agentsdk.ConversationTaskToolScope{}}
	result, err := repo.ApplyConversationTaskTool(t.Context(), request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, request.Call.ID, result); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "queued"}, ""); err != nil {
		t.Fatal(err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), request.Authority.RuntimeID)
	if err != nil || !ok {
		t.Fatal("launch", err)
	}
	child, found, err := repo.Claim(t.Context(), request.Authority.RuntimeID, "task-worker", time.Minute)
	if err != nil || !found || child.Run.ID != launch.Run.ID {
		t.Fatal("claim", err)
	}
	if err = repo.Finish(t.Context(), child, agentsdk.ConversationModelResult{}, "execution_limit"); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || failed.Status != agentsdk.ConversationTaskStatusFailed || failed.ErrorCode != "execution_limit" || failed.CompletedAt == nil {
		t.Fatalf("failed task=%+v err=%v", failed, err)
	}
	resumed, err := repo.Resume(t.Context(), request.ConversationID, launch.Run.ID, request.Authority)
	if err != nil || resumed.Status != "queued" {
		t.Fatalf("resumed background run=%+v err=%v", resumed, err)
	}
	running, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || running.Status != agentsdk.ConversationTaskStatusRunning || running.CompletedAt != nil || running.ErrorCode != "" || running.CompletionEventID != "" {
		t.Fatalf("resumed task=%+v err=%v", running, err)
	}
	cancelled, err := repo.Cancel(t.Context(), request.ConversationID, launch.Run.ID, request.Authority)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled background run=%+v err=%v", cancelled, err)
	}
	cancelledTask, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || cancelledTask.Status != agentsdk.ConversationTaskStatusCancelled || cancelledTask.CompletedAt == nil || cancelledTask.CompletionEventID == "" || cancelledTask.CompletionEventSeq != cancelled.LastEventSeq {
		t.Fatalf("cancelled task=%+v err=%v", cancelledTask, err)
	}
}

func TestConversationTaskReconciliationResumeKeepsTaskRunning(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	input := executionStoreInput()
	definition := input.Tools[0]
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	start := agentsdk.ConversationTaskStart{Goal: "核查外部结果", Input: "结果未知时先核查", AllowedTools: []string{definition.Key}, Budget: budget}
	arguments, _ := json.Marshal(start)
	parent, request := personalMutationFixture(t, repo, "task-reconciliation", "task_start", string(arguments), true)
	prepared := agentsdk.ConversationTask{Goal: start.Goal, Input: start.Input, Budget: budget, SourceConversationID: request.ConversationID, SourceRunID: request.RunID, ToolScope: []agentsdk.ConversationTaskToolScope{{Key: definition.Key, Version: definition.Version, ActionKey: definition.ActionKey, DefinitionHash: conversationHash(definition)}}}
	result, err := repo.ApplyConversationTaskTool(t.Context(), request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), parent, 0, request.Call.ID, result); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), parent, agentsdk.ConversationModelResult{Content: "queued"}, ""); err != nil {
		t.Fatal(err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), request.Authority.RuntimeID)
	if err != nil || !ok {
		t.Fatal("launch", err)
	}
	claim, found, err := repo.Claim(t.Context(), request.Authority.RuntimeID, "worker", time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatal("claim", err)
	}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "external-write", Name: definition.Key, Arguments: `{"name":"one"}`}}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "external-write", agentsdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "external-write", agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.WaitExecution(t.Context(), claim, persistence.ConversationWait{Step: 0, CallID: "external-write", Kind: "reconciliation", Question: "核查实际外部结果"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || waiting.Status != agentsdk.ConversationTaskStatusRunning || waiting.CompletionEventID != "" {
		t.Fatalf("reconciliation task=%+v err=%v", waiting, err)
	}
	resumed, err := repo.Resume(t.Context(), request.ConversationID, launch.Run.ID, request.Authority)
	if err != nil || resumed.Status != "queued" {
		t.Fatalf("resumed reconciliation run=%+v err=%v", resumed, err)
	}
	stillRunning, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || stillRunning.Status != agentsdk.ConversationTaskStatusRunning || stillRunning.ExecutionRunID != launch.Run.ID {
		t.Fatalf("resumed reconciliation task=%+v err=%v", stillRunning, err)
	}
	recovered, found, err := repo.Claim(t.Context(), request.Authority.RuntimeID, "reconciliation-worker", time.Minute)
	if err != nil || !found || recovered.Run.ID != launch.Run.ID || recovered.Fence == claim.Fence {
		t.Fatalf("reconciliation claim=%+v found=%v err=%v", recovered, found, err)
	}
	step, found, err := repo.ExecutionStep(t.Context(), recovered, 0, nil)
	if err != nil || !found || step.Result == nil {
		t.Fatalf("frozen reconciliation step=%+v found=%v err=%v", step, found, err)
	}
	unknown, replayed, err := repo.BeginExecutionTool(t.Context(), recovered, 0, "external-write", agentsdk.ConversationToolAuthorization{Granted: true, Revision: "reauthorized"})
	if err != nil || !replayed || unknown.State != "uncertain" || unknown.IdempotencyKey == "" {
		t.Fatalf("uncertain receipt=%+v replayed=%v err=%v", unknown, replayed, err)
	}
	if err = repo.FinishExecutionTool(t.Context(), recovered, 0, "external-write", agentsdk.ConversationToolResult{Status: "completed", ResourceID: "external-one", Content: json.RawMessage(`{"id":"external-one"}`)}); err != nil {
		t.Fatal(err)
	}
	next := input
	next.IdempotencyKey = "reconciliation-finished"
	if _, _, err = repo.ExecutionStep(t.Context(), recovered, 1, &next); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), recovered, 1, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已核查外部结果，任务完成。"}, FinishReason: "stop"}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), recovered, agentsdk.ConversationModelResult{Content: "已核查外部结果，任务完成。"}, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.ConversationTask(t.Context(), result.ResourceID, request.Authority)
	if err != nil || completed.Status != agentsdk.ConversationTaskStatusCompleted || completed.CompletionEventID == "" || completed.CompletionEventSeq < 1 {
		t.Fatalf("reconciled task=%+v err=%v", completed, err)
	}
}
