package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func taskAgreementBrief(version int64, goal string) sdk.ConversationTaskBrief {
	return sdk.ConversationTaskBrief{
		Version: version, Goal: goal, Deliverable: goal + "的可核对结果", Audience: "项目负责人",
		Constraints: []string{"保留原始执行记录"}, CompletionConditions: []string{"结果已经生成", "结果可以复核"}, Assumptions: []string{"来源数据保持可读"},
		ExplicitFields: []string{"completion_conditions", "deliverable", "goal"},
		InferredFields: []string{"assumptions", "audience", "constraints"},
	}
}

func TestConversationTaskAgreementPersistsCurrentGoalAndStartsANewRun(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	conversation, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "goal-agreement-conversation", Title: "发布核对"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	budget := sdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	v1 := taskAgreementBrief(1, "核对初始发布")
	request := sdk.ScheduledConversationTaskRequest{
		ContractVersion: sdk.ScheduledConversationTaskContractVersion, PlanID: "goal-plan", SchedulerRunID: "goal-window-1", IdempotencyKey: "goal-window-1",
		ScheduledFor: time.Now().UTC().Truncate(time.Millisecond), Authority: authority, ConversationID: conversation.ID,
		Input: sdk.ConversationTaskStart{Goal: v1.Goal, Input: "build 42", Budget: budget, Brief: &v1},
	}
	prepared := sdk.ConversationTask{Goal: v1.Goal, Input: request.Input.Input, Budget: budget, Brief: &v1, AgreementRevision: 1, SourceConversationID: conversation.ID, ToolScope: []sdk.ConversationTaskToolScope{}}
	receipt, err := repo.AcceptScheduledConversationTask(t.Context(), request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Task.Brief == nil || receipt.Task.GoalProgress.Status != sdk.ConversationGoalStatusActive || receipt.Task.GoalProgress.Phase != "queued" || len(receipt.Task.GoalProgress.RemainingItems) != 2 {
		t.Fatalf("initial goal projection=%+v", receipt.Task)
	}

	invalid := taskAgreementBrief(2, "核对候选发布")
	invalid.InferredFields = []string{"assumptions", "audience"}
	if _, _, err = repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, sdk.ConversationTaskAgreementUpdate{ClientID: "goal-invalid", ExpectedRevision: 1, Reason: "候选版本变化", Brief: invalid}, authority); err == nil || !strings.Contains(err.Error(), "task_agreement_invalid") {
		t.Fatalf("incomplete provenance accepted: %v", err)
	}
	invalid = taskAgreementBrief(2, "核对候选发布")
	invalid.Audience = ""
	if _, _, err = repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, sdk.ConversationTaskAgreementUpdate{ClientID: "goal-no-audience", ExpectedRevision: 1, Reason: "候选版本变化", Brief: invalid}, authority); err == nil || !strings.Contains(err.Error(), "task_agreement_invalid") {
		t.Fatalf("agreement without audience accepted: %v", err)
	}

	v2 := taskAgreementBrief(2, "核对候选发布")
	update := sdk.ConversationTaskAgreementUpdate{ClientID: "goal-update-2", ExpectedRevision: 1, Reason: "候选版本从 build 42 调整为 build 43", Brief: v2}
	changed, replay, err := repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, update, authority)
	if err != nil || replay || changed.Goal != v2.Goal || changed.Brief == nil || changed.Brief.Version != 2 || changed.AgreementRevision != 2 || changed.GoalProgress.Status != sdk.ConversationGoalStatusActive || changed.GoalProgress.Phase != "queued" {
		t.Fatalf("updated task=%+v replay=%v err=%v", changed, replay, err)
	}
	replayed, replay, err := repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, update, authority)
	if err != nil || !replay || replayed.AgreementRevision != 2 {
		t.Fatalf("replayed update=%+v replay=%v err=%v", replayed, replay, err)
	}
	conflicting := update
	conflicting.Reason = "different request"
	if _, _, err = repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, conflicting, authority); err == nil || !strings.Contains(err.Error(), "task_agreement_idempotency_conflict") {
		t.Fatalf("changed idempotency request accepted: %v", err)
	}
	stale := taskAgreementBrief(3, "过期变更")
	if _, _, err = repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, sdk.ConversationTaskAgreementUpdate{ClientID: "goal-stale", ExpectedRevision: 1, Reason: "过期变更", Brief: stale}, authority); err == nil || !strings.Contains(err.Error(), "task_agreement_changed") {
		t.Fatalf("stale agreement accepted: %v", err)
	}

	launch, launched, err := repo.LaunchConversationTask(t.Context(), authority.RuntimeID)
	if err != nil || !launched || launch.Run.BackgroundTask == nil || launch.Run.BackgroundTask.BriefVersion != 2 || launch.Run.BackgroundTask.AgreementRevision != 2 || !strings.Contains(sdk.ConversationTaskPrompt(launch.Task), `"version":2`) {
		t.Fatalf("versioned launch=%+v launched=%v err=%v", launch, launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), authority.RuntimeID, "goal-worker", time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{}, "execution_limit"); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.ConversationTask(t.Context(), receipt.Task.ID, authority)
	if err != nil || failed.GoalProgress.Status != sdk.ConversationGoalStatusBudgetExhausted || failed.GoalProgress.Blocker != "execution_limit" {
		t.Fatalf("budget exhaustion projection=%+v err=%v", failed.GoalProgress, err)
	}

	v3 := taskAgreementBrief(3, "核对候选发布并补充证据")
	updatedAgain, replay, err := repo.UpdateConversationTaskAgreement(t.Context(), receipt.Task.ID, sdk.ConversationTaskAgreementUpdate{ClientID: "goal-update-3", ExpectedRevision: 2, Reason: "预算耗尽后缩小目标并重试", Brief: v3}, authority)
	if err != nil || replay || updatedAgain.Status != sdk.ConversationTaskStatusCancelled || updatedAgain.ExecutionRunID != "" || updatedAgain.AgreementRevision != 3 || len(updatedAgain.PreviousExecutionRuns) != 1 || updatedAgain.PreviousExecutionRuns[0].RunID != launch.Run.ID || updatedAgain.GoalProgress.Status != sdk.ConversationGoalStatusPaused {
		t.Fatalf("replacement after failure=%+v replay=%v err=%v", updatedAgain, replay, err)
	}
	var raw []byte
	if err = store.Database().QueryRowContext(t.Context(), `SELECT payload_json FROM _agent_conversation_task_agreement_updates WHERE task_id = ? AND client_id = ?`, receipt.Task.ID, "goal-update-3").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var mutation conversationTaskAgreementUpdateRecord
	if err = json.Unmarshal(raw, &mutation); err != nil || mutation.Update.Reason != "预算耗尽后缩小目标并重试" {
		t.Fatalf("agreement audit=%+v err=%v", mutation, err)
	}
	if _, err = repo.ResumeQueuedConversationTask(t.Context(), receipt.Task.ID, authority); err != nil {
		t.Fatal(err)
	}
	second, launched, err := repo.LaunchConversationTask(t.Context(), authority.RuntimeID)
	if err != nil || !launched || second.Run.ID == launch.Run.ID || second.Run.BackgroundTask == nil || second.Run.BackgroundTask.BriefVersion != 3 || second.Run.BackgroundTask.AgreementRevision != 3 || len(second.Task.PreviousExecutionRuns) != 1 {
		t.Fatalf("new attempt=%+v launched=%v err=%v", second, launched, err)
	}
}
