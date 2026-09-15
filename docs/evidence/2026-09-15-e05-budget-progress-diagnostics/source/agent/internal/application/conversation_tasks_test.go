package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationTaskCatalogHost struct {
	definitions []agentsdk.ConversationToolDefinition
	denied      map[string]bool
}

type idleConversationTaskRepository struct{ calls atomic.Int32 }

func (r *idleConversationTaskRepository) LaunchConversationTask(context.Context, string) (persistence.ConversationTaskLaunch, bool, error) {
	r.calls.Add(1)
	return persistence.ConversationTaskLaunch{}, false, nil
}
func (*idleConversationTaskRepository) ConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return agentsdk.ConversationTask{}, nil
}

func (h *conversationTaskCatalogHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return h.definitions, nil
}
func (h *conversationTaskCatalogHost) AuthorizeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: !h.denied[in.Definition.Key], Revision: "auth:" + in.Definition.Key}, nil
}
func (*conversationTaskCatalogHost) InvokeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}
func (*conversationTaskCatalogHost) ReconcileConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}

func taskDefinitions(t *testing.T, keys ...string) []agentsdk.ConversationToolDefinition {
	t.Helper()
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var out []agentsdk.ConversationToolDefinition
	for _, definition := range agentsdk.PersonalConversationTools() {
		if wanted[definition.Key] {
			out = append(out, definition)
		}
	}
	if len(out) != len(keys) {
		t.Fatalf("missing task test definitions: %v", keys)
	}
	return out
}

func taskStartRequest(t *testing.T, allowed []string, budget agentsdk.ConversationTaskBudget) agentsdk.ConversationToolRequest {
	t.Helper()
	arguments, err := json.Marshal(agentsdk.ConversationTaskStart{Goal: "核对发布", Input: "build 42", AllowedTools: allowed, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return agentsdk.ConversationToolRequest{
		Authority:      agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"},
		ConversationID: "conversation", RunID: "run", Definition: agentsdk.BackgroundTaskConversationTool(),
		Call: agentsdk.ConversationToolCall{Name: "task_start", Arguments: string(arguments)},
	}
}

func TestPrepareConversationTaskFreezesGoalScopeAuthorizationAndBudget(t *testing.T) {
	host := &conversationTaskCatalogHost{definitions: taskDefinitions(t, "task_start", "time_now", "calculate")}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, MaxInputBytes: 16 * 1024, MaxSteps: 12, MaxToolCalls: 8, MaxOutputBytes: 8192, RunTimeout: 5 * time.Minute}}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 4, MaxToolCalls: 3, MaxOutputBytes: 2048, TimeoutSeconds: 30}
	task, err := s.prepareConversationTask(t.Context(), taskStartRequest(t, []string{"time_now", "calculate"}, budget))
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceConversationID != "conversation" || task.SourceRunID != "run" || task.Goal != "核对发布" || task.Input != "build 42" || task.Budget != budget || len(task.ToolScope) != 2 {
		t.Fatalf("task fields were not frozen: %+v", task)
	}
	if task.Brief == nil || task.Brief.Version != 1 || task.AgreementRevision != 1 || task.Brief.DueAt != nil || len(task.Brief.ExplicitFields) != 0 || len(task.Brief.InferredFields) != 6 {
		t.Fatalf("legacy task did not materialize an auditable inferred agreement: %+v", task.Brief)
	}
	for index, key := range []string{"time_now", "calculate"} {
		scope := task.ToolScope[index]
		if scope.Key != key || scope.Version == "" || scope.ActionKey == "" || scope.DefinitionHash == "" || scope.AuthorizationRevision != "auth:"+key {
			t.Fatalf("scope[%d] incomplete: %+v", index, scope)
		}
	}
}

func TestPrepareConversationTaskKeepsExplicitAndInferredAgreementFields(t *testing.T) {
	host := &conversationTaskCatalogHost{definitions: taskDefinitions(t, "task_start")}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, MaxInputBytes: 16 * 1024, MaxSteps: 12, MaxToolCalls: 8, MaxOutputBytes: 8192, RunTimeout: 5 * time.Minute}}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	brief := agentsdk.ConversationTaskBrief{
		Version: 1, Goal: "核对发布", Deliverable: "发布核对报告", Audience: "项目负责人",
		Constraints: []string{"只使用现有记录"}, CompletionConditions: []string{"报告可复核"}, Assumptions: []string{},
		ExplicitFields: []string{"goal", "deliverable", "completion_conditions"}, InferredFields: []string{"audience", "constraints", "assumptions"},
	}
	start := agentsdk.ConversationTaskStart{Goal: brief.Goal, Input: "build 42", Budget: budget, Brief: &brief}
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	request := taskStartRequest(t, nil, budget)
	request.Call.Arguments = string(raw)
	task, err := s.prepareConversationTask(t.Context(), request)
	if err != nil || task.Brief == nil || len(task.Brief.ExplicitFields) != 3 || len(task.Brief.InferredFields) != 3 || task.Brief.DueAt != nil {
		t.Fatalf("prepared agreement=%+v err=%v", task.Brief, err)
	}

	invalid := brief
	invalid.InferredFields = []string{"audience", "constraints"}
	start.Brief = &invalid
	raw, _ = json.Marshal(start)
	request.Call.Arguments = string(raw)
	if _, err = s.prepareConversationTask(t.Context(), request); err == nil || !strings.Contains(err.Error(), "task_brief_invalid") {
		t.Fatalf("incomplete provenance accepted: %v", err)
	}
	invalid = brief
	invalid.Audience = ""
	start.Brief = &invalid
	raw, _ = json.Marshal(start)
	request.Call.Arguments = string(raw)
	if _, err = s.prepareConversationTask(t.Context(), request); err == nil || !strings.Contains(err.Error(), "task_brief_invalid") {
		t.Fatalf("agreement without audience accepted: %v", err)
	}
}

func TestPrepareConversationTaskRejectsRecursiveUnavailableAndDeploymentExceedingScope(t *testing.T) {
	host := &conversationTaskCatalogHost{definitions: taskDefinitions(t, "task_start", "time_now"), denied: map[string]bool{"time_now": true}}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, MaxInputBytes: 16 * 1024, MaxSteps: 4, MaxToolCalls: 4, MaxOutputBytes: 2048, RunTimeout: time.Minute}}
	valid := agentsdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}
	for _, test := range []struct {
		name    string
		allowed []string
		budget  agentsdk.ConversationTaskBudget
		code    string
	}{
		{"recursive", []string{"task_start"}, valid, "agent.conversation.task_scope_invalid"},
		{"self control", []string{"task_cancel"}, valid, "agent.conversation.task_scope_invalid"},
		{"internal plan", []string{"plan_update"}, valid, "agent.conversation.task_scope_invalid"},
		{"internal completion", []string{"completion_submit"}, valid, "agent.conversation.task_scope_invalid"},
		{"denied", []string{"time_now"}, valid, "agent.conversation.tool_access_denied"},
		{"step budget", nil, agentsdk.ConversationTaskBudget{MaxSteps: 5, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 30}, "agent.conversation.task_start_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := s.prepareConversationTask(t.Context(), taskStartRequest(t, test.allowed, test.budget))
			var coded *agentsdk.Error
			if !errors.As(err, &coded) || coded.Code != test.code {
				t.Fatalf("error=%v, want %s", err, test.code)
			}
		})
	}
}

func TestConversationTaskRunCatalogAndBudgetsStayFrozen(t *testing.T) {
	definitions := taskDefinitions(t, "task_start", "time_now", "calculate", "plan_update", "completion_submit")
	host := &conversationTaskCatalogHost{definitions: definitions}
	checked := []string{}
	s := &ConversationService{options: ConversationOptions{ToolHost: host, ToolAvailability: catalogAvailabilityFunc(func(_ context.Context, _ agentsdk.ConversationAuthority, key string) (bool, error) {
		checked = append(checked, key)
		if key != "time_now" {
			return false, errors.New("unselected connection must not be inspected")
		}
		return true, nil
	}), MaxSteps: 12, MaxToolCalls: 8, MaxOutputBytes: 8192, RunTimeout: 5 * time.Minute}}
	var selected agentsdk.ConversationToolDefinition
	for _, definition := range definitions {
		if definition.Key == "time_now" {
			selected = definition
		}
	}
	budget := agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 2, MaxOutputBytes: 1024, TimeoutSeconds: 15}
	claim := persistence.ConversationClaim{Run: agentsdk.ConversationRun{BackgroundTask: &agentsdk.ConversationTaskExecution{
		TaskID: "task", CompletionMode: agentsdk.ConversationTaskCompletionModeAssessed, Budget: budget, ToolScope: []agentsdk.ConversationTaskToolScope{{Key: selected.Key, Version: selected.Version, ActionKey: selected.ActionKey, DefinitionHash: conversationDigest(selected)}},
	}}}
	visible, compiled, err := s.executionCatalogForRun(t.Context(), claim)
	if err != nil || len(visible) != 3 || visible[0].Key != "time_now" || visible[1].Key != "completion_submit" || visible[2].Key != "plan_update" || len(compiled) != 3 {
		t.Fatalf("task catalog=%+v compiled=%d err=%v", visible, len(compiled), err)
	}
	if len(checked) != 1 || checked[0] != "time_now" {
		t.Fatalf("task inspected tools outside its frozen scope: %v", checked)
	}
	steps, calls, output, timeout := s.conversationRunLimits(claim)
	if steps != 3 || calls != 2 || output != 1024 || timeout != 15*time.Second {
		t.Fatalf("task limits=%d %d %d %s", steps, calls, output, timeout)
	}
	for index := range host.definitions {
		if host.definitions[index].Key == "time_now" {
			host.definitions[index].Description += " changed without version"
		}
	}
	if _, _, err = s.executionCatalogForRun(t.Context(), claim); err == nil {
		t.Fatal("changed tool contract was accepted by the background run")
	}
}

func TestConversationPlanAndCompletionToolsAreOnlyVisibleToEligibleBackgroundTaskRuns(t *testing.T) {
	definitions := taskDefinitions(t, "time_now", "plan_update", "completion_submit")
	s := &ConversationService{options: ConversationOptions{ToolHost: &conversationTaskCatalogHost{definitions: definitions}}}
	visible, compiled, err := s.executionCatalogForRun(t.Context(), persistence.ConversationClaim{})
	if err != nil || len(visible) != 1 || visible[0].Key != "time_now" || len(compiled) != 1 {
		t.Fatalf("interactive catalog=%+v compiled=%d err=%v", visible, len(compiled), err)
	}
	if _, exists := compiled["plan_update"]; exists {
		t.Fatal("interactive run received background plan mutation tool")
	}
	if _, exists := compiled["completion_submit"]; exists {
		t.Fatal("interactive run received background completion mutation tool")
	}
}

func TestConversationTaskControlStateFollowsDurableRunState(t *testing.T) {
	closedInput := &agentsdk.ConversationInteraction{Kind: "input", Status: "cancelled"}
	cancelledReconciliation := &agentsdk.ConversationInteraction{Kind: "reconciliation", Status: "cancelled"}
	tests := []struct {
		name string
		task agentsdk.ConversationTask
		run  *agentsdk.ConversationRun
		want agentsdk.ConversationTaskControlState
	}{
		{"queued before launch", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusQueued}, nil, agentsdk.ConversationTaskControlState{CanCancel: true}},
		{"cancelled before launch", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusCancelled}, nil, agentsdk.ConversationTaskControlState{CanResume: true}},
		{"waiting for user", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusRunning}, &agentsdk.ConversationRun{Status: "waiting_user"}, agentsdk.ConversationTaskControlState{CanCancel: true, ResumeBlocker: "interaction_response_required"}},
		{"failed model call", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusFailed}, &agentsdk.ConversationRun{Status: "failed"}, agentsdk.ConversationTaskControlState{CanResume: true}},
		{"closed input", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusCancelled}, &agentsdk.ConversationRun{Status: "cancelled", Interaction: closedInput}, agentsdk.ConversationTaskControlState{ResumeBlocker: "interaction_closed"}},
		{"cancelled reconciliation", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusCancelled}, &agentsdk.ConversationRun{Status: "cancelled", Interaction: cancelledReconciliation}, agentsdk.ConversationTaskControlState{CanResume: true}},
		{"pending reconciliation", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusRunning}, &agentsdk.ConversationRun{Status: "needs_reconciliation"}, agentsdk.ConversationTaskControlState{CanCancel: true, CanResume: true}},
		{"revoked source projection", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusFailed}, &agentsdk.ConversationRun{Status: "failed", AccessError: "source_access_denied"}, agentsdk.ConversationTaskControlState{ResumeBlocker: "source_access_denied"}},
		{"completed", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusCompleted}, &agentsdk.ConversationRun{Status: "completed"}, agentsdk.ConversationTaskControlState{}},
		{"awaiting review", agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusAwaitingReview}, &agentsdk.ConversationRun{Status: "completed"}, agentsdk.ConversationTaskControlState{CanResume: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationTaskControlState(test.task, test.run); got != test.want {
				t.Fatalf("control=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestConversationTaskDiagnosticChoosesWaitAdjustAndConcreteBlockers(t *testing.T) {
	task := agentsdk.ConversationTask{Status: agentsdk.ConversationTaskStatusRunning, AgreementRevision: 3, ExecutionRunID: "current", PreviousExecutionRuns: []agentsdk.ConversationRunReference{{RunID: "one"}, {RunID: "two"}}}
	progress := agentsdk.ConversationGoalProgress{Status: agentsdk.ConversationGoalStatusActive, RemainingItems: []string{"verify"}}
	allocation := agentsdk.ConversationWorkAllocation{RepeatedToolCalls: 1}
	diagnostic := conversationTaskDiagnostic(task, progress, &agentsdk.ConversationRun{Attempt: 1}, allocation, nil)
	if diagnostic.State != "watch" || diagnostic.Action != "adjust_strategy" || diagnostic.ExecutionAttempts != 3 || len(diagnostic.Reasons) != 3 {
		t.Fatalf("adjust diagnostic=%+v", diagnostic)
	}
	waiting := conversationTaskDiagnostic(task, progress, &agentsdk.ConversationRun{Attempt: 1, Status: "waiting_user"}, allocation, nil)
	if waiting.State != "waiting" || waiting.Action != "wait" || len(waiting.Reasons) != 1 {
		t.Fatalf("waiting diagnostic=%+v", waiting)
	}
	blocked := conversationTaskDiagnostic(task, progress, nil, agentsdk.ConversationWorkAllocation{}, []agentsdk.ConversationDependencyState{{State: "changed"}})
	if blocked.State != "blocked" || blocked.Action != "report_blocker" || blocked.OpenDependencies != 1 {
		t.Fatalf("dependency diagnostic=%+v", blocked)
	}
	task.Plan = &agentsdk.ConversationPlan{Steps: []agentsdk.ConversationPlanStep{
		{ID: "verified", Status: agentsdk.ConversationPlanStepCompleted, Outcome: "invoice totals verified", Evidence: []agentsdk.ConversationResultReference{{ConversationID: "source", RunID: "run"}}},
		{ID: "blocked", Title: "obtain missing quote", Status: agentsdk.ConversationPlanStepBlocked, Blocker: "supplier quote unavailable"},
	}}
	planBlocked := conversationTaskDiagnostic(task, progress, nil, agentsdk.ConversationWorkAllocation{}, nil)
	if planBlocked.State != "blocked" || planBlocked.Action != "report_blocker" || planBlocked.StageOutcomes != 1 || planBlocked.RemainingStages != 1 || len(planBlocked.Reasons) != 1 || planBlocked.Reasons[0] != "plan_blocker:supplier quote unavailable" {
		t.Fatalf("plan diagnostic=%+v", planBlocked)
	}
	delegationReview := conversationDelegationGoalProgress("delivered", agentsdk.ConversationGoalProgress{Status: agentsdk.ConversationGoalStatusCompleted})
	if delegationReview.Status != agentsdk.ConversationGoalStatusBlocked || delegationReview.Blocker != "delivery_review_required" {
		t.Fatalf("delegated delivery was treated as accepted: %+v", delegationReview)
	}
	progress.Status = agentsdk.ConversationGoalStatusCompleted
	completed := conversationTaskDiagnostic(task, progress, nil, allocation, nil)
	if completed.State != "completed" || completed.Action != "none" || len(completed.Reasons) != 0 {
		t.Fatalf("completed diagnostic=%+v", completed)
	}
}

func TestConversationTaskWorkerWaitsForStateTransitionWhenQueueIsEmpty(t *testing.T) {
	repo := &idleConversationTaskRepository{}
	s := &ConversationService{runtimeID: "runtime", options: ConversationOptions{Poll: 5 * time.Millisecond}, taskWake: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	s.wg.Add(1)
	go s.conversationTaskWorker(ctx, repo)
	deadline := time.Now().Add(time.Second)
	for repo.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if repo.calls.Load() != 1 {
		cancel()
		s.wg.Wait()
		t.Fatalf("startup recovery calls=%d", repo.calls.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if repo.calls.Load() != 1 {
		cancel()
		s.wg.Wait()
		t.Fatalf("empty queue was polled: calls=%d", repo.calls.Load())
	}
	s.signalConversationTasks()
	deadline = time.Now().Add(time.Second)
	for repo.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	s.wg.Wait()
	if repo.calls.Load() != 2 {
		t.Fatalf("state transition did not wake worker: calls=%d", repo.calls.Load())
	}
}
