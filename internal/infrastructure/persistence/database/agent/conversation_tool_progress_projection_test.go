package agent

import (
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestConversationToolProgressProjectsDurablyAndDoesNotRegress(t *testing.T) {
	run := agentsdk.ConversationRun{
		Attempt: 0,
		Steps: []agentsdk.ConversationStepView{{
			Number:  0,
			Attempt: 0,
			Status:  "tools",
			Calls:   []agentsdk.ConversationToolView{{ID: "plan", Name: "record_lookup", Status: "running"}},
		}},
	}
	now := time.Now().UTC()
	plan := map[string]any{"step": 0, "attempt": 0, "call_id": "plan", "stages": []toolsdk.ProgressStep{{Key: "lookup_record", Label: "查询记录", Detail: "等待查询"}, {Key: "enrich", Label: "补充结果", Detail: "仅在需要时执行", Optional: true}}, "updated_at": now}
	if err := projectConversationExecutionEvent(&run, "tool.plan.created", plan); err != nil {
		t.Fatal(err)
	}
	outOfOrder := map[string]any{"step": 0, "attempt": 0, "call_id": "plan", "progress_key": "enrich", "detail": "正在补充", "status": toolsdk.ProgressStatusStarted, "updated_at": now}
	if err := projectConversationExecutionEvent(&run, "tool.progress", outOfOrder); err == nil {
		t.Fatal("accepted an out-of-order tool stage")
	}
	started := map[string]any{"step": 0, "attempt": 0, "call_id": "plan", "progress_key": "lookup_record", "detail": "正在查询", "status": toolsdk.ProgressStatusStarted, "updated_at": now}
	if err := projectConversationExecutionEvent(&run, "tool.progress", started); err != nil {
		t.Fatal(err)
	}
	completed := map[string]any{"step": 0, "attempt": 0, "call_id": "plan", "progress_key": "lookup_record", "detail": "查询完成", "status": toolsdk.ProgressStatusCompleted, "updated_at": now.Add(time.Second)}
	if err := projectConversationExecutionEvent(&run, "tool.progress", completed); err != nil {
		t.Fatal(err)
	}
	if err := projectConversationExecutionEvent(&run, "tool.progress", started); err != nil {
		t.Fatal(err)
	}
	progress := run.Steps[0].Calls[0].Progress
	skipped := map[string]any{"step": 0, "attempt": 0, "call_id": "plan", "progress_key": "enrich", "detail": "无需补充", "status": toolsdk.ProgressStatusSkipped, "updated_at": now.Add(2 * time.Second)}
	if err := projectConversationExecutionEvent(&run, "tool.progress", skipped); err != nil {
		t.Fatal(err)
	}
	progress = run.Steps[0].Calls[0].Progress
	if len(progress) != 2 || progress[0].Status != toolsdk.ProgressStatusCompleted || progress[0].Detail != "查询完成" || progress[0].Index != 1 || progress[0].Total != 2 || progress[1].Status != toolsdk.ProgressStatusSkipped || !progress[1].Optional {
		t.Fatalf("progress=%+v", progress)
	}
}

func TestConversationRunPlanExistsBeforeAndOrdersAllProgress(t *testing.T) {
	run := agentsdk.ConversationRun{Attempt: 1}
	now := time.Now().UTC()
	stages := []agentsdk.ConversationRunPlanStep{
		{Key: "understand", Label: "çè§£è¯·æ±", Binding: agentsdk.ConversationRunPlanBinding{Kind: "model"}},
		{Key: "lookup", Label: "æ¥è¯¢èµæ", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool_progress", Tools: []string{"record_lookup"}, ProgressKey: "lookup"}},
		{Key: "review", Label: "æ ¸å¯¹ä¿¡æ¯", Optional: true, Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"ask_user"}}},
		{Key: "update", Label: "æ´æ°è®°å½", Binding: agentsdk.ConversationRunPlanBinding{Kind: "tool", Tools: []string{"record_update"}}},
	}
	if err := projectConversationExecutionEvent(&run, "run.plan.created", map[string]any{"attempt": 1, "key": "request", "label": "å¤çè¯·æ±", "stages": stages, "updated_at": now}); err != nil {
		t.Fatal(err)
	}
	if run.Plan == nil || len(run.Plan.Steps) != 4 || run.Plan.Steps[0].Status != "planned" {
		t.Fatalf("plan=%+v", run.Plan)
	}
	progress := func(key, callID, status string, at time.Time) error {
		return projectConversationExecutionEvent(&run, "run.progress", map[string]any{"attempt": 1, "progress_key": key, "call_id": callID, "status": status, "detail": status, "updated_at": at})
	}
	if err := progress("lookup", "lookup-call", "started", now.Add(time.Second)); err == nil {
		t.Fatal("started a later run step before recognition completed")
	}
	if err := progress("understand", "", "started", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("understand", "", "completed", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("lookup", "lookup-call", "started", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("lookup", "lookup-call", "completed", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("review", "", "skipped", now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("update", "update-call", "started", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := progress("update", "update-call", "waiting_confirmation", now.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.Plan.Steps[2].Status != "skipped" || run.Plan.Steps[3].Status != "waiting_confirmation" {
		t.Fatalf("plan progress=%+v", run.Plan.Steps)
	}
}

func TestConversationRunCancellationSettlesEveryRemainingPlanStep(t *testing.T) {
	run := agentsdk.ConversationRun{Attempt: 1, Plan: &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "read", Status: agentsdk.ConversationRunProgressCompleted},
		{Key: "write", Status: agentsdk.ConversationRunProgressWaitingConfirmation},
		{Key: "answer", Status: agentsdk.ConversationRunProgressPlanned},
	}}}
	if err := projectConversationExecutionEvent(&run, "run.cancelled", map[string]any{"attempt": 1, "error_code": "interaction_rejected"}); err != nil {
		t.Fatal(err)
	}
	if run.Plan.Steps[0].Status != agentsdk.ConversationRunProgressCompleted || run.Plan.Steps[1].Status != agentsdk.ConversationRunProgressSkipped || run.Plan.Steps[2].Status != agentsdk.ConversationRunProgressSkipped {
		t.Fatalf("cancelled run left non-terminal plan work: %+v", run.Plan.Steps)
	}
}

func TestConversationRunFailureFailsActivePlanStepAndSkipsFutureWork(t *testing.T) {
	run := agentsdk.ConversationRun{Attempt: 1, Plan: &agentsdk.ConversationRunPlan{Steps: []agentsdk.ConversationRunPlanStep{
		{Key: "read", Status: agentsdk.ConversationRunProgressCompleted},
		{Key: "write", Status: agentsdk.ConversationRunProgressStarted},
		{Key: "answer", Status: agentsdk.ConversationRunProgressPlanned},
	}}}
	if err := projectConversationExecutionEvent(&run, "run.failed", map[string]any{"attempt": 1, "error_code": "provider_failed"}); err != nil {
		t.Fatal(err)
	}
	if run.Plan.Steps[1].Status != agentsdk.ConversationRunProgressFailed || run.Plan.Steps[2].Status != agentsdk.ConversationRunProgressSkipped {
		t.Fatalf("failed run did not settle plan according to execution state: %+v", run.Plan.Steps)
	}
}

func TestConversationToolCompletionAcceptsOnlySkippedOptionalStages(t *testing.T) {
	plan := []toolsdk.ProgressStep{{Key: "read"}, {Key: "enrich", Optional: true}}
	completed := []agentsdk.ConversationToolProgressView{
		{Key: "read", Status: toolsdk.ProgressStatusCompleted},
		{Key: "enrich", Optional: true, Status: toolsdk.ProgressStatusSkipped},
	}
	if !conversationToolProgressFinished(plan, completed) {
		t.Fatal("optional skipped stage prevented tool completion")
	}
	plan[1].Optional = false
	if conversationToolProgressFinished(plan, completed) {
		t.Fatal("required skipped stage was accepted")
	}
}
