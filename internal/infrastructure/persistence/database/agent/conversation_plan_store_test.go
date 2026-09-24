package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func runningScheduledPlanTask(t *testing.T, repo *ConversationStore) (sdk.ConversationAuthority, persistence.ConversationClaim, sdk.ConversationTask) {
	t.Helper()
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "plan-user"}
	conversation, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "plan-conversation", Title: "Plan"}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.DefaultConversationTaskBrief("prepare release")
	request := sdk.ScheduledConversationTaskRequest{ContractVersion: sdk.ScheduledConversationTaskContractVersion, PlanID: "schedule-plan", SchedulerRunID: "schedule-run", IdempotencyKey: "plan-window", ScheduledFor: time.Now().UTC().Truncate(time.Millisecond), Authority: a, ConversationID: conversation.ID, Input: sdk.ConversationTaskStart{Goal: brief.Goal, Input: "release input", AllowedTools: []string{}, Budget: sdk.ConversationTaskBudget{MaxSteps: 8, MaxToolCalls: 8, MaxOutputBytes: 8192, TimeoutSeconds: 120}, Brief: &brief}}
	receipt, err := repo.AcceptScheduledConversationTask(t.Context(), request, sdk.ConversationTask{Goal: brief.Goal, Input: request.Input.Input, Budget: request.Input.Budget, Brief: &brief, AgreementRevision: 1, SourceConversationID: conversation.ID, ToolScope: []sdk.ConversationTaskToolScope{}})
	if err != nil {
		t.Fatal(err)
	}
	launch, launched, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !launched || launch.Task.ID != receipt.Task.ID || launch.Run.WriteScope == nil || !launch.Run.WriteScope.Allows("plan_update") {
		t.Fatalf("launch=%+v launched=%v err=%v", launch, launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "plan-worker", time.Minute)
	if err != nil || !found || claim.Run.ID != launch.Run.ID {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	return a, claim, launch.Task
}

func applyPlanVersion(t *testing.T, repo *ConversationStore, claim persistence.ConversationClaim, number int, update sdk.ConversationPlanUpdate, previous *sdk.ConversationPlan, mutate func(*sdk.ConversationPlan)) (sdk.ConversationToolResult, error) {
	t.Helper()
	arguments, _ := json.Marshal(update)
	definition := sdk.ConversationPlanUpdateTool()
	input := executionStoreInput()
	input.IdempotencyKey = fmt.Sprintf("plan-step-%d", number)
	input.MaxArgumentBytes = 256 * 1024
	input.Tools = []sdk.ConversationToolDefinition{definition}
	input.Messages = []sdk.ConversationStepMessage{{Role: "user", Content: "maintain plan"}}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, number, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: fmt.Sprintf("plan-call-%d", number), Name: definition.Key, Arguments: string(arguments)}
	if err := repo.CompleteExecutionStep(t.Context(), claim, number, sdk.ConversationStepResult{Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := repo.BeginExecutionTool(t.Context(), claim, number, call.ID, sdk.ConversationToolAuthorization{Granted: true, Revision: "plan-auth"})
	if err != nil {
		t.Fatal(err)
	}
	agentID := "default"
	if claim.Run.Agent != nil && claim.Run.Agent.ID != "" {
		agentID = claim.Run.Agent.ID
	}
	source := sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, BeforeStep: number + 1}
	plan := sdk.ConversationPlan{TaskID: claim.Run.BackgroundTask.TaskID, Version: update.ExpectedVersion + 1, AgreementRevision: update.AgreementRevision, Reason: update.Reason, Source: &source, Steps: make([]sdk.ConversationPlanStep, 0, len(update.Steps))}
	for _, value := range update.Steps {
		fields := append([]string(nil), value.RequirementFields...)
		if len(fields) == 0 {
			fields = append([]string(nil), storedPlanAgreementFields...)
		}
		step := sdk.ConversationPlanStep{ID: value.ID, Title: value.Title, Status: value.Status, DependsOn: value.DependsOn, Input: value.Input, ExpectedOutput: value.ExpectedOutput, RequirementFields: fields, Executor: sdk.ConversationPlanExecutor{AgentID: agentID, RunID: claim.Run.ID}, Evidence: value.Evidence, Artifacts: value.Artifacts, Outcome: value.Outcome, Blocker: value.Blocker}
		if previous != nil {
			for _, old := range previous.Steps {
				if old.ID == step.ID && old.Status == sdk.ConversationPlanStepCompleted {
					step.Executor = old.Executor
				}
			}
		}
		plan.Steps = append(plan.Steps, step)
	}
	if mutate != nil {
		mutate(&plan)
	}
	request := sdk.ConversationToolRequest{Authority: claim.Authority, ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, CorrelationID: claim.Run.ID, LeaseOwner: claim.Owner, Fence: claim.Fence, Step: number, Call: call, Definition: definition, IdempotencyKey: ledger.IdempotencyKey}
	return repo.ApplyConversationTaskPlanTool(t.Context(), request, plan)
}

func TestConversationPlanVersionsPreserveCompletedWorkAndReactToFailureAndAgreementChange(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a, claim, task := runningScheduledPlanTask(t, repo)
	v1 := sdk.ConversationPlanUpdate{ClientID: "plan-v1", ExpectedVersion: 0, AgreementRevision: 1, Reason: "Initial multi-step plan", Steps: []sdk.ConversationPlanStepUpdate{{ID: "inspect", Title: "Inspect inputs", Status: sdk.ConversationPlanStepInProgress, DependsOn: []string{}, Input: "release input", ExpectedOutput: "validated inputs", RequirementFields: []string{"goal"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}}, {ID: "publish", Title: "Publish release", Status: sdk.ConversationPlanStepPending, DependsOn: []string{"inspect"}, Input: "validated inputs", ExpectedOutput: "published release", RequirementFields: []string{"deliverable"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}}}}
	result, err := applyPlanVersion(t, repo, claim, 0, v1, nil, nil)
	if err != nil || result.Status != "completed" || result.ResourceID != task.ID {
		t.Fatalf("v1 result=%+v err=%v", result, err)
	}
	current, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || current.Plan == nil || current.Plan.Version != 1 || current.Plan.Steps[0].Executor.RunID != claim.Run.ID {
		t.Fatalf("current=%+v err=%v", current.Plan, err)
	}
	v2 := v1
	v2.ClientID, v2.ExpectedVersion, v2.Reason = "plan-v2", 1, "Inspection completed"
	v2.Steps = append([]sdk.ConversationPlanStepUpdate(nil), v1.Steps...)
	v2.Steps[0].Status, v2.Steps[0].Outcome = sdk.ConversationPlanStepCompleted, "Inputs validated"
	v2.Steps[1].Status = sdk.ConversationPlanStepInProgress
	if _, err = applyPlanVersion(t, repo, claim, 1, v2, current.Plan, nil); err != nil {
		t.Fatal(err)
	}
	current, _ = repo.ConversationTask(t.Context(), task.ID, a)
	tampered := v2
	tampered.ClientID, tampered.ExpectedVersion = "plan-v3-tampered", 2
	tampered.Steps = append([]sdk.ConversationPlanStepUpdate(nil), v2.Steps...)
	tampered.Steps[0].Title = "Rewrite completed evidence"
	if rejected, applyErr := applyPlanVersion(t, repo, claim, 2, tampered, current.Plan, nil); applyErr != nil || rejected.Status != "failed" || rejected.ErrorCode != "plan_completed_step_changed" {
		t.Fatalf("completed plan step was not rejected: result=%+v err=%v", rejected, applyErr)
	}
	// The failed mutation still has a completed tool receipt. A later model step
	// starts from the unchanged current version and can keep working.
	v3 := v2
	v3.ClientID, v3.ExpectedVersion, v3.Reason = "plan-v3", 2, "Continue after rejected edit"
	if _, err = applyPlanVersion(t, repo, claim, 3, v3, current.Plan, nil); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{}, "execution_limit"); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || failed.Plan == nil || failed.Plan.Version != 4 || failed.Plan.Steps[0].Status != sdk.ConversationPlanStepCompleted || failed.Plan.Steps[1].Status != sdk.ConversationPlanStepBlocked || failed.Plan.Steps[1].Blocker != "execution_limit" {
		t.Fatalf("failed plan=%+v err=%v", failed.Plan, err)
	}
	brief := *failed.Brief
	brief.Version++
	brief.Goal = "prepare release candidate"
	updated, replay, err := repo.UpdateConversationTaskAgreement(t.Context(), task.ID, sdk.ConversationTaskAgreementUpdate{ClientID: "agreement-v2", ExpectedRevision: 1, Reason: "target changed", Brief: brief}, a)
	if err != nil || replay || updated.Plan == nil || updated.Plan.Version != 5 || updated.Plan.AgreementRevision != 2 || updated.Plan.Steps[0].Status != sdk.ConversationPlanStepCompleted || updated.Plan.Steps[1].Status != sdk.ConversationPlanStepNeedsReview {
		t.Fatalf("updated plan=%+v replay=%v err=%v", updated.Plan, replay, err)
	}
	history, err := repo.ConversationTaskPlans(t.Context(), task.ID, 0, a)
	if err != nil || len(history.Items) != 5 || history.Items[0].Version != 5 || history.Items[4].Version != 1 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if _, err = repo.ConversationTaskPlan(t.Context(), task.ID, 1, sdk.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}); err == nil {
		t.Fatal("cross-owner plan read succeeded")
	}
}

func TestConversationPlanFollowsConversationLifecycle(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a, claim, task := runningScheduledPlanTask(t, repo)
	update := sdk.ConversationPlanUpdate{ClientID: "lifecycle-plan", ExpectedVersion: 0, AgreementRevision: 1, Reason: "Persist the work graph", Steps: []sdk.ConversationPlanStepUpdate{{ID: "work", Title: "Do the work", Status: sdk.ConversationPlanStepInProgress, DependsOn: []string{}, Input: task.Input, ExpectedOutput: "verified output", RequirementFields: []string{"goal"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}}}}
	if result, err := applyPlanVersion(t, repo, claim, 0, update, nil, nil); err != nil || result.Status != "completed" {
		t.Fatalf("plan result=%+v err=%v", result, err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	closed, err := repo.ConversationTask(t.Context(), task.ID, a)
	if err != nil || closed.Plan == nil || closed.Plan.Version != 2 || closed.Plan.Steps[0].Status != sdk.ConversationPlanStepNeedsReview || closed.Plan.Steps[0].Blocker != "execution completed with unfinished plan steps" {
		t.Fatalf("closed plan=%+v err=%v", closed.Plan, err)
	}
	conversation, err := repo.Get(t.Context(), task.SourceConversationID, a)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := NewLifecycleStore(store).conversationLifecyclePayload(t.Context(), conversationOwner(a), conversation.ID, conversationJSON(conversation))
	if err != nil || !json.Valid(payload) || !containsJSONText(payload, "Persist the work graph") || !containsJSONText(payload, "execution completed with unfinished plan steps") {
		t.Fatalf("lifecycle payload=%s err=%v", payload, err)
	}
	if _, err = repo.DeleteForRequest(t.Context(), "delete-planned-conversation", conversation.ID, conversation.Revision, a); err != nil {
		t.Fatal(err)
	}
	var plans, tasks int
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_items WHERE owner_key = ? AND item_kind = 'task_plan' AND subject_id = ?`, conversationOwner(a), task.ID).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_tasks WHERE record_kind = 'task' AND owner_key = ? AND task_id = ?`, conversationOwner(a), task.ID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if plans != 0 || tasks != 0 {
		t.Fatalf("orphaned lifecycle rows: plans=%d tasks=%d", plans, tasks)
	}
}

func containsJSONText(raw []byte, value string) bool {
	return strings.Contains(string(raw), value)
}
