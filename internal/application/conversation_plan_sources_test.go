package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type planReceiptRepository struct {
	*personalReceiptRepository
	persistence.ConversationTaskPlanRepository
	persistence.ConversationTaskReadRepository
	plan    sdk.ConversationPlan
	task    sdk.ConversationTask
	missing bool
}

func (r *planReceiptRepository) ConversationTaskPlan(_ context.Context, id string, version int64, a sdk.ConversationAuthority) (sdk.ConversationPlan, error) {
	if r.missing || a != r.a || id != r.plan.TaskID || version != r.plan.Version {
		return sdk.ConversationPlan{}, conversationFailure("not_found", "plan_not_found")
	}
	return r.plan, nil
}

func (r *planReceiptRepository) ConversationTask(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	if a != r.a || id != r.task.ID {
		return sdk.ConversationTask{}, conversationFailure("not_found", "task_not_found")
	}
	return r.task, nil
}

func TestPlanSourceReadsExactHistoricalVersionAndRejectsForgedReceipt(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	definition := sdk.ConversationPlanUpdateTool()
	update := sdk.ConversationPlanUpdate{ClientID: "plan-v1", ExpectedVersion: 0, AgreementRevision: 1, Reason: "initial plan", Steps: []sdk.ConversationPlanStepUpdate{{ID: "inspect", Title: "Inspect", Status: sdk.ConversationPlanStepCompleted, DependsOn: []string{}, Input: "input", ExpectedOutput: "checked", RequirementFields: []string{"goal"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}, Outcome: "checked"}}}
	args, _ := json.Marshal(update)
	source := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run", BeforeStep: 3}
	plan := sdk.ConversationPlan{TaskID: "task", Version: 1, AgreementRevision: 1, Reason: update.Reason, Source: &source, CreatedAt: time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC), Steps: []sdk.ConversationPlanStep{{ID: "inspect", Title: "Inspect", Status: sdk.ConversationPlanStepCompleted, DependsOn: []string{}, Input: "input", ExpectedOutput: "checked", RequirementFields: []string{"goal"}, Executor: sdk.ConversationPlanExecutor{AgentID: "default", RunID: "run"}, Evidence: []sdk.ConversationResultReference{}, Artifacts: []sdk.ConversationArtifactReference{}, Outcome: "checked"}}}
	content, _ := json.Marshal(map[string]any{"plan": plan})
	record := persistence.ConversationToolExecution{Step: 2, State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "plan-call", Name: definition.Key, Arguments: string(args)}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: plan.TaskID, Content: content}, IdempotencyKey: "original-plan"}
	current := plan
	current.Version = 2
	repo := &planReceiptRepository{personalReceiptRepository: &personalReceiptRepository{a: a, record: record}, plan: plan, task: sdk.ConversationTask{ID: plan.TaskID, SourceConversationID: "producer", Plan: &current}}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo}
	owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
	roots, err := s.sourceAudit(a).record(t.Context(), owner, record)
	if err != nil || len(roots) != 1 || roots[0] != (sdk.ConversationRunReference{ConversationID: "producer", RunID: "run", BeforeStep: 4}) {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
	repo.plan.Reason = "forged historical plan"
	if _, err = s.sourceAudit(a).record(t.Context(), owner, record); err == nil {
		t.Fatal("changed historical plan accepted")
	}
	repo.plan = plan
	repo.missing = true
	if _, err = s.sourceAudit(a).record(t.Context(), owner, record); err == nil {
		t.Fatal("missing historical plan accepted")
	}
	repo.missing = false
	for _, mutate := range []func(*persistence.ConversationToolExecution){
		func(value *persistence.ConversationToolExecution) { value.State = "running" },
		func(value *persistence.ConversationToolExecution) { value.Result.ResourceID = "other-task" },
		func(value *persistence.ConversationToolExecution) { value.IdempotencyKey = "forged-key" },
	} {
		changed := record
		result := *record.Result
		changed.Result = &result
		mutate(&changed)
		if _, err = s.sourceAudit(a).record(t.Context(), owner, changed); err == nil {
			t.Fatal("forged plan receipt accepted", changed)
		}
	}
}
