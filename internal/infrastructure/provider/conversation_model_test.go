package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationModelUsesStatelessRolesWithoutBusinessTools(t *testing.T) {
	input := agentsdk.ConversationModelRequest{Purpose: "reply", IdempotencyKey: "stable", MaxOutputBytes: 1024, Messages: []agentsdk.ConversationModelMessage{{Role: "system", Content: "instructions"}, {Role: "user", Content: "first"}, {Role: "assistant", Content: "remembered answer"}, {Role: "user", Content: "follow up"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Idempotency-Key") != "stable" || r.Header.Get("Authorization") != "Bearer model-key" {
			t.Errorf("incorrect stateless request")
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		for _, key := range []string{"tools", "tool_choice", "external_session_id", "execution_credential", "agent_id", "metadata"} {
			if payload[key] != nil {
				t.Errorf("business state leaked: %s", key)
			}
		}
		var messages []agentsdk.ConversationModelMessage
		_ = json.Unmarshal(payload["messages"], &messages)
		if !reflect.DeepEqual(messages, input.Messages) {
			t.Errorf("lost roles: %+v", messages)
		}
		_, _ = w.Write([]byte(`{"model":"model-1","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"answer"}}],"usage":{"prompt_tokens":42}}`))
	}))
	defer server.Close()
	model, err := NewConversationModel(ConversationModelConfig{URL: server.URL + "/v1/chat/completions", APIKey: "model-key", Model: "model-1", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	output, err := model.GenerateConversation(context.Background(), input)
	if err != nil || output.Content != "answer" || output.Usage["prompt_tokens"] != float64(42) {
		t.Fatalf("output=%+v err=%v", output, err)
	}
}
func TestConversationModelRejectsPartialAndToolResponses(t *testing.T) {
	for i, body := range []string{
		`{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":"partial"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"text","tool_calls":[{"function":{"name":"send_email"}}]}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"` + strings.Repeat("x", 33) + `"}}]}`,
		`{"choices":[]}`,
	} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			model, err := NewConversationModel(ConversationModelConfig{URL: server.URL, Model: "model", Client: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = model.GenerateConversation(t.Context(), agentsdk.ConversationModelRequest{Purpose: "reply", Messages: []agentsdk.ConversationModelMessage{{Role: "user", Content: "hello"}}, MaxOutputBytes: 32})
			if err == nil {
				t.Fatal("accepted incomplete or tool reply")
			}
		})
	}
}
