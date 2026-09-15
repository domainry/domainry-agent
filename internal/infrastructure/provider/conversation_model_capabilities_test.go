package provider

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationModelDeclaresCapabilitiesAndEncodesReasoningForEveryProtocol(t *testing.T) {
	for _, protocol := range []string{ConversationProtocolChat, ConversationProtocolMessages, ConversationProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			model, err := NewConversationModel(ConversationModelConfig{
				Protocol: protocol, URL: "https://models.example.test/v1", Model: "reasoner",
				ContextTokenLimit: 128000, ImageInput: true, StructuredOutput: true,
				ReasoningEfforts: []string{"high", "low"}, DefaultReasoningEffort: "low",
			})
			if err != nil {
				t.Fatal(err)
			}
			capabilities := model.ConversationModelCapabilities()
			if capabilities.ContextTokenLimit != 128000 || !capabilities.ImageInput || !capabilities.StructuredOutput || !capabilities.ProtocolContinuation || len(capabilities.ReasoningEfforts) != 2 || capabilities.ReasoningEfforts[0] != "high" || capabilities.ReasoningEfforts[1] != "low" {
				t.Fatalf("unexpected capabilities: %+v", capabilities)
			}
			request := agentsdk.ConversationStepRequest{
				Messages: []agentsdk.ConversationStepMessage{{Role: "user", Content: "solve"}}, ModelIdentity: model.ConversationModelIdentity(), ModelCapabilities: capabilities,
				ReasoningEffort: "high", IdempotencyKey: "capability-test", MaxOutputBytes: 1024, MaxArgumentBytes: 1024, MaxToolCalls: 1,
			}
			payload, err := model.stepPayload(request)
			if err != nil {
				t.Fatal(err)
			}
			switch protocol {
			case ConversationProtocolChat:
				if payload["reasoning_effort"] != "high" {
					t.Fatalf("chat reasoning missing: %#v", payload)
				}
			case ConversationProtocolMessages:
				if payload["output_config"].(map[string]any)["effort"] != "high" {
					t.Fatalf("messages reasoning missing: %#v", payload)
				}
			case ConversationProtocolResponses:
				if payload["reasoning"].(map[string]any)["effort"] != "high" {
					t.Fatalf("responses reasoning missing: %#v", payload)
				}
			}
			request.ReasoningEffort = "unsupported"
			if _, err = model.ConversationStepInputBytes(request); err == nil {
				t.Fatal("unsupported reasoning effort was accepted")
			}
		})
	}
}

func TestConversationModelIdentityChangesWithCapabilityContract(t *testing.T) {
	first, _ := NewConversationModel(ConversationModelConfig{URL: "https://models.example.test/v1", Model: "same", ContextTokenLimit: 64000})
	second, _ := NewConversationModel(ConversationModelConfig{URL: "https://models.example.test/v1", Model: "same", ContextTokenLimit: 128000})
	if first.ConversationModelIdentity() == second.ConversationModelIdentity() {
		t.Fatal("capability contract did not change the frozen model identity")
	}
}

func TestConversationModelCapabilityEnvironment(t *testing.T) {
	t.Setenv("AGENT_CONVERSATION_CONTEXT_TOKENS", "128000")
	t.Setenv("AGENT_CONVERSATION_IMAGE_INPUT", "true")
	t.Setenv("AGENT_CONVERSATION_STRUCTURED_OUTPUT", "true")
	t.Setenv("AGENT_CONVERSATION_PROTOCOL_CONTINUATION", "false")
	t.Setenv("AGENT_CONVERSATION_REASONING_EFFORTS", "high, low")
	t.Setenv("AGENT_CONVERSATION_REASONING_EFFORT", "high")
	config := ConversationModelConfigFromEnvironment()
	if config.ContextTokenLimit != 128000 || !config.ImageInput || !config.StructuredOutput || !config.DisableProtocolContinuation || len(config.ReasoningEfforts) != 2 || config.DefaultReasoningEffort != "high" {
		t.Fatalf("capability environment was not retained: %+v", config)
	}
}
