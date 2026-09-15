package application

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type capabilityTestModel struct {
	identity     agentsdk.ConversationModelIdentity
	capabilities agentsdk.ConversationModelCapabilities
	defaultLevel string
}

func (m *capabilityTestModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return m.identity
}
func (m *capabilityTestModel) ConversationModelCapabilities() agentsdk.ConversationModelCapabilities {
	return m.capabilities
}
func (m *capabilityTestModel) ConversationModelDefaultReasoningEffort() string { return m.defaultLevel }
func (m *capabilityTestModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, nil
}
func (m *capabilityTestModel) StreamConversationStep(context.Context, agentsdk.ConversationStepRequest, func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	return agentsdk.ConversationStepResult{}, nil
}

func TestConversationModelSelectionFreezesCapabilitiesAndRejectsRegistryChanges(t *testing.T) {
	model := &capabilityTestModel{
		identity:     agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "reasoner", Fingerprint: "v1"},
		capabilities: agentsdk.ConversationModelCapabilities{ContextTokenLimit: 64000, StructuredOutput: true, ProtocolContinuation: true, ReasoningEfforts: []string{"low", "high"}},
		defaultLevel: "low",
	}
	service := &ConversationService{model: model, options: ConversationOptions{ContextBytes: 100000}}
	selection, err := service.resolveConversationModelSelection(&agentsdk.ConversationModelRequestSelection{Key: "default", ReasoningEffort: "high"}, "default", "")
	if err != nil || selection.Identity != model.identity || selection.ReasoningEffort != "high" || selection.Capabilities.ContextTokenLimit != 64000 {
		t.Fatalf("selection was not frozen: %+v %v", selection, err)
	}
	ctx, err := service.selectConversationTaskModel(t.Context(), selection)
	if err != nil || service.conversationContextLimit(ctx) != 64000 || service.conversationModel(ctx) != model {
		t.Fatalf("selection was not applied: %v", err)
	}
	model.capabilities.ContextTokenLimit = 128000
	if _, err = service.selectConversationTaskModel(t.Context(), selection); conversationModelFailureCode(err, "") != "model_changed" {
		t.Fatalf("changed capabilities were accepted: %v", err)
	}
}

func TestConversationModelSelectionRejectsUnsupportedReasoning(t *testing.T) {
	model := &capabilityTestModel{identity: agentsdk.ConversationModelIdentity{Fingerprint: "v1"}, capabilities: agentsdk.ConversationModelCapabilities{ReasoningEfforts: []string{"low"}}}
	service := &ConversationService{model: model}
	if _, err := service.resolveConversationModelSelection(&agentsdk.ConversationModelRequestSelection{Key: "default", ReasoningEffort: "high"}, "default", ""); conversationModelFailureCode(err, "") != "reasoning_effort_unsupported" {
		t.Fatalf("unsupported effort was accepted: %v", err)
	}
}

func TestConversationAgentRecheckPreservesIndependentTaskModelSelection(t *testing.T) {
	service, repo, _, authority := discoveryFixture()
	agentModel := &capabilityTestModel{identity: agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "agent-model", Fingerprint: "agent-v1"}}
	taskModel := &capabilityTestModel{identity: agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "task-model", Fingerprint: "task-v1"}}
	service.options.AgentModels = map[string]agentsdk.ConversationModel{"agent-model": agentModel, "task-model": taskModel}
	repo.agents = append(repo.agents, agentsdk.ConversationAgent{ID: "specialist", Name: "Specialist", Instructions: "Work", ModelKey: "agent-model", Enabled: true, Revision: 1, MaxConcurrent: 1})

	snapshot, err := service.freezeConversationAgent(t.Context(), "specialist", authority)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := service.resolveConversationModelSelection(&agentsdk.ConversationModelRequestSelection{Key: "task-model"}, "agent-model", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.selectConversationTaskModel(t.Context(), selection)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = service.selectConversationAgent(ctx, snapshot, authority)
	if err != nil || selectedConversationAgent(ctx) != snapshot || selectedConversationModel(ctx).Key != "task-model" || service.conversationModel(ctx) != taskModel {
		t.Fatalf("Agent recheck replaced or rejected the task model: %v", err)
	}
}
