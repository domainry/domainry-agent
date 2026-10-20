package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/composition/conversation"
)

type separatePlanningModel struct {
	calls atomic.Int32
	delay time.Duration
	input chan agentsdk.ConversationModelRequest
}

func (m *separatePlanningModel) GenerateConversation(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.calls.Add(1)
	if m.input != nil {
		m.input <- in
	}
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return agentsdk.ConversationModelResult{}, ctx.Err()
		case <-time.After(m.delay):
		}
	}
	if in.ModelIdentity != m.ConversationModelIdentity() || in.ModelCapabilities.ImageInput || in.ReasoningEffort != "" || !strings.Contains(in.IdempotencyKey, ":plan:") {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("planner received the execution model's selection")
	}
	return agentsdk.ConversationModelResult{Model: "fast-planner", Content: `{"label":"Create item","steps":[{"label":"Understand","kind":"model"},{"label":"Create confirmed item","kind":"tool","tool":"create_item"},{"label":"Report result","kind":"model"}]}`}, nil
}

func (*separatePlanningModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "messages", Model: "fast-planner", Fingerprint: "planning-model-v1"}
}
func (*separatePlanningModel) StreamConversationStep(context.Context, agentsdk.ConversationStepRequest, func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	return agentsdk.ConversationStepResult{}, fmt.Errorf("planning model must never execute the task")
}
func (*separatePlanningModel) ConversationModelCapabilities() agentsdk.ConversationModelCapabilities {
	return agentsdk.ConversationModelCapabilities{ContextTokenLimit: 16000}
}
func (*separatePlanningModel) ConversationModelDefaultReasoningEffort() string { return "" }

type executionOnlyModel struct{ *executionModel }

func (*executionOnlyModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("execution model must not generate the plan")
}

func TestIndependentPlanningModelRunsOnceAndLeavesExecutionSelectionUntouched(t *testing.T) {
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true}, confirm: true, respondAllowed: true}
	planner := &separatePlanningModel{input: make(chan agentsdk.ConversationModelRequest, 1)}
	base := &executionModel{}
	base.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if in.ModelIdentity != base.ConversationModelIdentity() {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("planning selection leaked into execution")
		}
		if number == 1 {
			return base.callResult(`{"title":"one"}`), nil
		}
		return base.answerResult(in), nil
	}
	service, err := conversationassembly.NewService(repo, &executionOnlyModel{base}, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, RunPlanningEnabled: true, RunPlanningModel: planner})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "independent-planning-model"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "create one"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, conversation.ID, run.ID, "waiting_confirmation")
	host.mu.Lock()
	if host.invokes != 0 {
		t.Error("planning bypassed business confirmation")
	}
	host.mu.Unlock()
	if _, err = service.Respond(t.Context(), conversation.ID, run.ID, agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "confirm-once", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}, conversationAuthority()); err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "completed" || final.Model != "tool-model" || final.Plan == nil || len(final.Plan.Steps) != 3 || planner.calls.Load() != 1 || len(final.Steps) != 2 {
		t.Fatalf("independent planner broke execution: status=%s code=%s model=%s planner_calls=%d", final.Status, final.ErrorCode, final.Model, planner.calls.Load())
	}
	for _, step := range final.Plan.Steps {
		if step.Status != "completed" {
			t.Fatalf("completed run lost plan progress: %+v", step)
		}
	}
	input := <-planner.input
	if input.Purpose != "summary" || input.ModelIdentity.Model != "fast-planner" {
		t.Fatal("planning did not receive the separate text-model request")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 1 {
		t.Fatalf("business effect executed %d times", host.invokes)
	}
}

func TestPlanningTimeoutFailsBeforeAnyExecutionOrBusinessEffect(t *testing.T) {
	repo := conversationRepository(t)
	host := &executionHost{allowed: true}
	planner := &separatePlanningModel{delay: time.Second}
	base := &executionModel{step: func(int, agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("execution must not start after planning timeout")
	}}
	service, err := conversationassembly.NewService(repo, &executionOnlyModel{base}, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, RunPlanningEnabled: true, RunPlanningModel: planner, RunPlanningTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bounded-planning"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "create one"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "failed" || final.ErrorCode != "run_plan_timeout" || len(final.Steps) != 0 || final.Plan != nil || planner.calls.Load() != 1 || base.calls != 0 || host.invokes != 0 {
		t.Fatalf("planning timeout leaked into execution: status=%s code=%s planner_calls=%d steps=%d", final.Status, final.ErrorCode, planner.calls.Load(), len(final.Steps))
	}
}
