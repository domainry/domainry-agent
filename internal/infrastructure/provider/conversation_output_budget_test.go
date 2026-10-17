package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConfiguredOutputBudgetReachesTextAndToolRequests(t *testing.T) {
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_PROTOCOL", "chat_completions")
	t.Setenv("AGENT_CONVERSATION_MODEL", "reasoning-model")
	t.Setenv("AGENT_CONVERSATION_MODEL_API_KEY", "test-key")
	t.Setenv("AGENT_CONVERSATION_MAX_OUTPUT_TOKENS", "16384")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.MaxCompletionTokens != 16384 {
			t.Errorf("configured text output budget missing: %+v, %v", payload, err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"complete"}}]}`))
	}))
	defer server.Close()
	config := ConversationModelConfigFromEnvironment()
	config.URL = server.URL
	config.BaseURL = ""
	model, err := NewConversationModel(config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.GenerateConversation(t.Context(), agentsdk.ConversationModelRequest{Purpose: "summary", MaxOutputBytes: 1024, Messages: []agentsdk.ConversationModelMessage{{Role: "user", Content: "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	input := executionTestRequest(model)
	for _, budget := range []int{0, 8192, 32768} {
		input.MaxOutputTokens = budget
		payload, err := model.stepPayload(input)
		want := 16384
		if budget > 0 {
			want = min(want, budget)
		}
		if err != nil || payload["max_completion_tokens"] != want {
			t.Fatalf("step budget=%d, payload=%v, err=%v", budget, payload["max_completion_tokens"], err)
		}
	}
	for _, value := range []string{"invalid", "0", "-1"} {
		t.Setenv("AGENT_CONVERSATION_MAX_OUTPUT_TOKENS", value)
		config.MaxOutputTokens = ConversationModelConfigFromEnvironment().MaxOutputTokens
		if _, err = NewConversationModel(config); err == nil {
			t.Fatalf("invalid output budget %q was silently defaulted", value)
		}
	}
}

func TestTruncationIsExplicitAndNeverCompletesPartialToolCalls(t *testing.T) {
	state := newExecutionStream(agentsdk.ConversationStepRequest{}, func(agentsdk.ConversationModelEvent) error { return nil })
	state.usage = map[string]any{"completion_tokens": 4096}
	state.calls[0] = &executionCall{call: agentsdk.ConversationToolCall{ID: "partial", Name: "write", Arguments: `{"incomplete":`}}
	err := state.complete("length")
	var details agentsdk.ConversationModelFailureProvider
	if !errors.As(err, &details) || details.ConversationModelFailureDetails().ErrorCode != "model_output_truncated" || details.ConversationModelFailureDetails().Usage["completion_tokens"] != 4096 || state.finished {
		t.Fatalf("truncated tool request was not rejected with usage: %v", err)
	}
	_, err = decodeChat([]byte(`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}],"usage":{"completion_tokens":4096}}`))
	if !errors.As(err, &details) || details.ConversationModelFailureDetails().ErrorCode != "model_output_truncated" {
		t.Fatalf("text truncation was not classified: %v", err)
	}
}
