package agent

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func runningAssessedTask(t *testing.T, repo *ConversationStore, suffix string, brief sdk.ConversationTaskBrief) (sdk.ConversationAuthority, persistence.ConversationClaim, sdk.ConversationTask) {
	t.Helper()
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "completion-user"}
	conversation, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "completion-conversation-" + suffix, Title: "Completion"}, a)
	if err != nil {
		t.Fatal(err)
	}
	budget := sdk.ConversationTaskBudget{MaxSteps: 6, MaxToolCalls: 4, MaxOutputBytes: 8192, TimeoutSeconds: 120}
	request := sdk.ScheduledConversationTaskRequest{
		ContractVersion: sdk.ScheduledConversationTaskContractVersion,
		PlanID:          "completion-plan-" + suffix, SchedulerRunID: "completion-schedule-" + suffix,
		IdempotencyKey: "completion-window-" + suffix, ScheduledFor: time.Now().UTC().Truncate(time.Millisecond),
		Authority: a, ConversationID: conversation.ID,
		Input: sdk.ConversationTaskStart{Goal: brief.Goal, Input: "completion input", AllowedTools: []string{}, Budget: budget, Brief: &brief},
	}
	receipt, err := repo.AcceptScheduledConversationTask(t.Context(), request, sdk.ConversationTask{Goal: brief.Goal, Input: request.Input.Input, Budget: budget, Brief: &brief, AgreementRevision: 1, CompletionMode: sdk.ConversationTaskCompletionModeAssessed, SourceConversationID: conversation.ID, ToolScope: []sdk.ConversationTaskToolScope{}})
	if err != nil {
		t.Fatal(err)
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !launched || launch.Task.ID != receipt.Task.ID || launch.Run.BackgroundTask == nil || !launch.Run.WriteScope.Allows("completion_submit") {
		t.Fatalf("launch=%+v launched=%v err=%v", launch, launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "completion-worker-"+suffix, time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	return a, claim, launch.Task
}

func applyTaskCompletion(t *testing.T, repo *ConversationStore, claim persistence.ConversationClaim, step int, submit sdk.ConversationTaskCompletionSubmit) sdk.ConversationToolResult {
	t.Helper()
	definition := sdk.ConversationTaskCompletionSubmitTool()
	arguments, _ := marshalDurableJSON(submit)
	input := executionStoreInput()
	input.IdempotencyKey = fmt.Sprintf("completion-step-%d", step)
	input.MaxArgumentBytes = 256 * 1024
	input.Tools = []sdk.ConversationToolDefinition{definition}
	input.Messages = []sdk.ConversationStepMessage{{Role: "user", Content: "finish the assessed task"}}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, step, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: fmt.Sprintf("completion-call-%d", step), Name: definition.Key, Arguments: string(arguments)}
	if err := repo.CompleteExecutionStep(t.Context(), claim, step, sdk.ConversationStepResult{Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := repo.BeginExecutionTool(t.Context(), claim, step, call.ID, sdk.ConversationToolAuthorization{Granted: true, Revision: "completion-auth"})
	if err != nil {
		t.Fatal(err)
	}
	source := sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, BeforeStep: step + 1}
	prepared := sdk.ConversationTaskCompletionRecord{Revision: submit.ExpectedRevision + 1, Kind: "agent_assessment", Submission: sdk.ConversationTaskCompletionSubmission{AgreementRevision: submit.AgreementRevision, Summary: submit.Summary, Data: submit.Data, Conditions: submit.Conditions, Artifacts: submit.Artifacts, Source: &source}}
	request := sdk.ConversationToolRequest{Authority: claim.Authority, ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, CorrelationID: claim.Run.ID, LeaseOwner: claim.Owner, Fence: claim.Fence, Step: step, Call: call, Definition: definition, IdempotencyKey: ledger.IdempotencyKey}
	result, err := repo.ApplyConversationTaskCompletionTool(t.Context(), request, prepared)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAssessedTaskRequiresSubmissionAndCanResumeWithImmutableCompletionHistory(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	brief := sdk.DefaultConversationTaskBrief("verify release")
	brief.CompletionConditions = []string{"The release result is verified."}
	a, firstClaim, task := runningAssessedTask(t, repo, "resume", brief)

	if err := repo.Finish(t.Context(), firstClaim, sdk.ConversationModelResult{Content: "I stopped without a completion assessment."}, ""); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || waiting.Status != sdk.ConversationTaskStatusAwaitingReview || waiting.CompletedAt != nil || waiting.Completion == nil || waiting.Completion.Kind != "execution_end" || waiting.Completion.Verification.Ready || waiting.CompletionEventID != "" {
		t.Fatalf("awaiting task=%+v err=%v", waiting, err)
	}
	history, err := repo.ConversationTaskCompletionHistory(t.Context(), task.ID, 0, a)
	if err != nil || len(history.Items) != 1 || history.Items[0].Revision != 1 {
		t.Fatalf("initial history=%+v err=%v", history, err)
	}

	queued, err := repo.ResumeQueuedConversationTask(t.Context(), task.ID, a)
	if err != nil || queued.Status != sdk.ConversationTaskStatusQueued || queued.Completion == nil || queued.Completion.Revision != 1 || len(queued.PreviousExecutionRuns) != 1 || queued.PreviousExecutionRuns[0].RunID != firstClaim.Run.ID {
		t.Fatalf("queued retry=%+v err=%v", queued, err)
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !launched || launch.Task.ID != task.ID {
		t.Fatalf("retry launch=%+v launched=%v err=%v", launch, launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "completion-worker-retry", time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("retry claim=%+v found=%v err=%v", claim, found, err)
	}
	submit := sdk.ConversationTaskCompletionSubmit{ClientID: "completion-retry", ExpectedRevision: 1, AgreementRevision: 1, Summary: "Release result checked", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Verified against the release output", Receipts: []sdk.ConversationResultReference{}}}, Artifacts: []sdk.ConversationArtifactReference{}}
	result := applyTaskCompletion(t, repo, claim, 0, submit)
	if result.Status != "completed" {
		t.Fatalf("completion result=%+v", result)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Release verified."}, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || completed.Status != sdk.ConversationTaskStatusCompleted || completed.CompletedAt == nil || completed.Completion == nil || completed.Completion.Revision != 2 || !completed.Completion.Verification.Ready || completed.CompletionEventID == "" || completed.CompletionEventSeq < 1 {
		t.Fatalf("completed task=%+v err=%v", completed, err)
	}
	history, err = repo.ConversationTaskCompletionHistory(t.Context(), task.ID, 0, a)
	if err != nil || len(history.Items) != 2 || history.Items[0].Revision != 2 || history.Items[1].Revision != 1 {
		t.Fatalf("final history=%+v err=%v", history, err)
	}
}

func TestProgramCompletionCannotBeOverriddenByAgentOrUserReview(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	brief := sdk.DefaultConversationTaskBrief("verify structured result")
	brief.CompletionConditions = []string{"Structured result has a string status.", "Summary is useful."}
	brief.VerificationRules = []sdk.ConversationCompletionRule{{Condition: 0, Kind: "data", Schema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string"}},"required":["status"]}`)}}
	a, claim, task := runningAssessedTask(t, repo, "program", brief)
	submit := sdk.ConversationTaskCompletionSubmit{ClientID: "completion-program", ExpectedRevision: 0, AgreementRevision: 1, Summary: "Structured result checked", Data: json.RawMessage(`{"status":false}`), Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Agent claim", Receipts: []sdk.ConversationResultReference{}}, {Condition: 1, Verdict: "met", Basis: "Summary checked", Receipts: []sdk.ConversationResultReference{}}}, Artifacts: []sdk.ConversationArtifactReference{}}
	applyTaskCompletion(t, repo, claim, 0, submit)
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Done"}, ""); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || waiting.Status != sdk.ConversationTaskStatusAwaitingReview || waiting.Completion == nil || waiting.Completion.Verification.Checks[0].Method != "program" || waiting.Completion.Verification.Checks[0].Verdict != "unmet" || waiting.Completion.Verification.Checks[1].Method != "agent" {
		t.Fatalf("program verification=%+v err=%v", waiting.Completion, err)
	}
	review := sdk.ConversationTaskCompletionReviewRequest{ClientID: "program-review", ExpectedRevision: waiting.Completion.Revision, Reason: "User reviewed every condition", Review: sdk.ConversationDeliveryReview{DeliveryDigest: waiting.Completion.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "User claim cannot replace program check", Receipts: []sdk.ConversationResultReference{}}, {Condition: 1, Verdict: "met", Basis: "Useful summary", Receipts: []sdk.ConversationResultReference{}}}}}
	reviewed, replay, err := repo.ReviewConversationTaskCompletion(t.Context(), task.ID, review, a)
	if err != nil || replay || reviewed.Status != sdk.ConversationTaskStatusAwaitingReview || reviewed.Completion == nil || reviewed.Completion.Revision != 2 || reviewed.Completion.Verification.Ready || reviewed.Completion.Verification.Checks[0].Verdict != "unmet" || reviewed.CompletedAt != nil {
		t.Fatalf("reviewed task=%+v replay=%v err=%v", reviewed, replay, err)
	}
	replayed, replay, err := repo.ReviewConversationTaskCompletion(t.Context(), task.ID, review, a)
	if err != nil || !replay || replayed.Completion == nil || replayed.Completion.Revision != 2 {
		t.Fatalf("review replay=%+v replay=%v err=%v", replayed, replay, err)
	}
	other := a
	other.UserID = "other"
	if _, _, err = repo.ReviewConversationTaskCompletion(t.Context(), task.ID, review, other); err == nil {
		t.Fatal("cross-owner review succeeded")
	}
}
