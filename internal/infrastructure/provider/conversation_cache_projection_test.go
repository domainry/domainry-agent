package provider

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestProviderProjectionHidesOnlyDeclaredServerFieldsAndPreservesReceipts(t *testing.T) {
	for _, protocol := range []string{ConversationProtocolChat, ConversationProtocolMessages, ConversationProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			m := &ConversationModel{config: ConversationModelConfig{Provider: "fixture", Protocol: protocol, Model: "fixture", MaxOutputTokens: 1024}}
			in := executionTestRequest(m)
			in.Tools[0].OutputSchema = json.RawMessage(`{"type":"object","x-agent-model-hidden":["evidence"]}`)
			in.Messages = append(in.Messages, agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "call", Name: "calculate", Arguments: `{"expression":"1+2"}`}}}, agentsdk.ConversationStepMessage{Role: "tool", ToolCallID: "call", Content: `{"status":"completed","content":{"answer":3,"evidence":{"host_proof":"server-only-secret"}},"reference":{"call_id":"call"},"citations":[{"id":"citation-1"}]}`})
			before, _ := json.Marshal(in)
			payload, err := m.stepPayload(in)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(payload)
			if strings.Contains(string(raw), "server-only-secret") || strings.Contains(string(raw), "host_proof") || !strings.Contains(string(raw), "answer") || !strings.Contains(string(raw), "citation-1") || !strings.Contains(string(raw), "reference") {
				t.Fatalf("projection lost business result/citation or leaked seal: %s", raw)
			}
			after, _ := json.Marshal(in)
			if string(before) != string(after) {
				t.Fatal("provider changed durable request/result proof")
			}
			sized, err := m.ConversationStepInputBytes(in)
			if err != nil || sized != len(raw) {
				t.Fatalf("projection sizing differs from wire: size=%d wire=%d err=%v", sized, len(raw), err)
			}
		})
	}
}

func TestMessagesCachingMarksToolsAndOnlyTheStableSystemPrefix(t *testing.T) {
	m := &ConversationModel{config: ConversationModelConfig{Provider: "fixture", Protocol: ConversationProtocolMessages, Model: "fixture", MaxOutputTokens: 1024}}
	in := executionTestRequest(m)
	in.Messages = []agentsdk.ConversationStepMessage{
		{Role: "system", Content: "stable base"},
		{Role: "system", ContextSourceKey: "instructions", Content: "stable product instructions"},
		{Role: "system", ContextSourceKey: "current_data", Content: "changing runtime evidence"},
		{Role: "user", Content: "query"},
	}
	in.Context = &agentsdk.ConversationContextManifest{Sources: []agentsdk.ConversationContextSourceReference{{Key: "instructions", StablePrefix: true}, {Key: "current_data", StablePrefix: false}}}
	payload, err := m.stepPayload(in)
	if err != nil {
		t.Fatal(err)
	}
	tools := payload["tools"].([]any)
	if tools[len(tools)-1].(map[string]any)["cache_control"] == nil {
		t.Fatal("tool prefix has no explicit cache marker")
	}
	system := payload["system"].([]any)
	if len(system) != 3 || system[0].(map[string]any)["cache_control"] != nil || system[1].(map[string]any)["cache_control"] == nil || system[2].(map[string]any)["cache_control"] != nil {
		t.Fatal("dynamic content was included in stable cache boundary", system)
	}
}
