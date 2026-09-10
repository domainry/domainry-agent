package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationProtocolFixture struct{ protocol, path, tokenKey, prelude, delta, tail, blocking string }

func conversationProtocolFixtures() []conversationProtocolFixture {
	return []conversationProtocolFixture{
		{ConversationProtocolChat, "/v1/chat/completions", "max_completion_tokens", "", `{"model":"served","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"}}]}`,
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":12}}\n\ndata: [DONE]\n\n",
			`{"model":"served","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"你好"}}],"usage":{"total_tokens":12}}`},
		{ConversationProtocolMessages, "/v1/messages", "max_tokens",
			"data: {\"type\":\"message_start\",\"message\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"served\",\"usage\":{\"input_tokens\":5}}}\n\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好"}}`,
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
			`{"type":"message","role":"assistant","model":"served","content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":7}}`},
		{ConversationProtocolResponses, "/v1/responses", "max_output_tokens", "", `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"你好"}`,
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"served\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"你好\"}]}],\"usage\":{\"total_tokens\":12}}}\n\n",
			`{"status":"completed","model":"served","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"你好"}]}],"usage":{"total_tokens":12}}`},
	}
}
func conversationStreamInput() agentsdk.ConversationModelRequest {
	return agentsdk.ConversationModelRequest{Purpose: "reply", MaxOutputBytes: 1024, IdempotencyKey: "frozen-input", Messages: []agentsdk.ConversationModelMessage{{Role: "system", Content: "instructions"}, {Role: "system", Content: "summary and memory"}, {Role: "user", Content: "question"}, {Role: "assistant", Content: "answer"}, {Role: "user", Content: "follow up"}}}
}
func TestGatewayProtocolsStreamBeforeCompletionAndSummarize(t *testing.T) {
	for _, fixture := range conversationProtocolFixtures() {
		t.Run(fixture.protocol, func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != fixture.path || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Idempotency-Key") != "frozen-input" {
					t.Error("incorrect Gateway endpoint or authentication")
				}
				var payload map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid payload")
					return
				}
				if string(payload[fixture.tokenKey]) != "128" {
					t.Errorf("token parameter %s missing", fixture.tokenKey)
				}
				for _, key := range []string{"tools", "tool_choice", "previous_response_id", "conversation", "execution_credential"} {
					if _, ok := payload[key]; ok {
						t.Errorf("unexpected provider state or tool: %s", key)
					}
				}
				var messages []agentsdk.ConversationModelMessage
				switch fixture.protocol {
				case ConversationProtocolMessages:
					if !strings.Contains(string(payload["system"]), "summary and memory") || r.Header.Get("anthropic-version") == "" {
						t.Error("missing Messages system instructions or version")
					}
					_ = json.Unmarshal(payload["messages"], &messages)
					if len(messages) != 3 || messages[0].Role != "user" {
						t.Error("system role leaked into Messages messages")
					}
				case ConversationProtocolResponses:
					_ = json.Unmarshal(payload["input"], &messages)
					if len(messages) != 5 || string(payload["store"]) != "false" {
						t.Error("lost stateless Responses history")
					}
				default:
					_ = json.Unmarshal(payload["messages"], &messages)
					if len(messages) != 5 || payload["max_tokens"] != nil {
						t.Error("incorrect Gateway chat payload")
					}
				}
				if string(payload["stream"]) == "false" {
					fmt.Fprint(w, fixture.blocking)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				fmt.Fprint(w, fixture.prelude)
				// CRLF, comments, and UTF-8 split across transport writes.
				frame := []byte(": heartbeat\r\ndata: " + fixture.delta + "\r\n\r\n")
				for _, b := range frame {
					_, _ = w.Write([]byte{b})
				}
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, fixture.tail)
			}))
			defer upstream.Close()
			model, err := NewConversationModel(ConversationModelConfig{Provider: "gateway", Protocol: fixture.protocol, URL: upstream.URL + fixture.path, APIKey: "test-key", Model: "selected", MaxOutputTokens: 128, Client: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			input := conversationStreamInput()
			input.Purpose = "summary"
			result, err := model.GenerateConversation(t.Context(), input)
			if err != nil || result.Content != "你好" || result.Model != "served" {
				t.Fatalf("summary: %+v %v", result, err)
			}
			input.Purpose = "reply"
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			gotDelta := false
			result, err = model.StreamConversation(ctx, input, func(text string) error {
				if text != "你好" {
					t.Errorf("incorrect delta %q", text)
				}
				if gotDelta {
					t.Error("duplicate delta")
				}
				gotDelta = true
				release <- struct{}{} // The server cannot finish until the callback is called.
				return nil
			})
			if err != nil || !gotDelta || result.Content != "你好" || result.Model != "served" || len(result.Usage) == 0 {
				t.Fatalf("stream: %+v %v", result, err)
			}
		})
	}
}
func TestConversationStreamRejectsTruncationToolsAndMismatchedFinal(t *testing.T) {
	for _, fixture := range conversationProtocolFixtures() {
		cases := map[string]string{"disconnect": fixture.prelude + "data: " + fixture.delta + "\n\n", "error": fixture.prelude + "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"private diagnostic\"}}\n\n"}
		switch fixture.protocol {
		case ConversationProtocolChat:
			cases["length"] = "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n"
			cases["tool"] = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{}]},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
			cases["no-stop"] = "data: [DONE]\n\n"
		case ConversationProtocolMessages:
			cases["length"] = fixture.prelude + "data: " + fixture.delta + "\n\n" + strings.Replace(fixture.tail, "end_turn", "max_tokens", 1)
			cases["tool"] = strings.Replace(fixture.prelude, `"type":"text"`, `"type":"tool_use"`, 1) + fixture.tail
		case ConversationProtocolResponses:
			cases["mismatch"] = fixture.tail
			cases["incomplete"] = "data: {\"type\":\"response.incomplete\"}\n\n"
			cases["tool"] = "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n"
		}
		for name, body := range cases {
			t.Run(fixture.protocol+"/"+name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, body)
				}))
				defer upstream.Close()
				model, err := NewConversationModel(ConversationModelConfig{Provider: "gateway", Protocol: fixture.protocol, URL: upstream.URL, APIKey: "test-key", Model: "model", Client: upstream.Client()})
				if err != nil {
					t.Fatal(err)
				}
				result, err := model.StreamConversation(t.Context(), conversationStreamInput(), func(string) error { return nil })
				if err == nil || result.Content != "" {
					t.Fatalf("accepted invalid stream %+v %v", result, err)
				}
				if strings.Contains(err.Error(), "private diagnostic") {
					t.Fatal("provider error body leaked")
				}
			})
		}
	}
}
func TestConversationStreamCancelsUpstreamOnContextOrCallback(t *testing.T) {
	for _, mode := range []string{"context", "callback", "limit"} {
		t.Run(mode, func(t *testing.T) {
			disconnected := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(disconnected)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer upstream.Close()
			model, err := NewConversationModel(ConversationModelConfig{URL: upstream.URL, Model: "model", Client: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			input := conversationStreamInput()
			if mode == "limit" {
				input.MaxOutputBytes = 2
			}
			callbackErr := errors.New("draft storage unavailable")
			_, err = model.StreamConversation(ctx, input, func(string) error {
				if mode == "context" {
					cancel()
					return nil
				}
				return callbackErr
			})
			if err == nil || (mode == "callback" && !errors.Is(err, callbackErr)) {
				t.Fatalf("cancellation not propagated: %v", err)
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("provider connection stayed open")
			}
		})
	}
}
func TestConversationProviderConfiguration(t *testing.T) {
	for _, fixture := range conversationProtocolFixtures() {
		model, err := NewConversationModel(ConversationModelConfig{Provider: "gateway", BaseURL: "https://models.example.com", Protocol: fixture.protocol, APIKey: "key", Model: "exact-model-name"})
		if err != nil || model.config.URL != "https://models.example.com"+fixture.path || model.config.Model != "exact-model-name" {
			t.Fatalf("wrong default %v", err)
		}
	}
	for _, config := range []ConversationModelConfig{{Provider: "unknown", Model: "m"}, {Provider: "gateway", Model: "m"}, {Provider: "gateway", APIKey: "key"}, {Provider: "gateway", APIKey: "key", Model: "m"}, {Provider: "gateway", BaseURL: "https://models.example.com?query=value", APIKey: "key", Model: "m"}, {Provider: "gateway", Protocol: "guess", APIKey: "key", Model: "m"}} {
		if _, err := NewConversationModel(config); err == nil {
			t.Fatal("invalid provider configuration accepted")
		}
	}
	t.Setenv("AGENT_CONVERSATION_PROVIDER", "gateway")
	t.Setenv("AGENT_CONVERSATION_MODEL_API_KEY", "")
	t.Setenv("AGENT_PROVIDER_API_KEY", "environment-key")
	if ConversationModelConfigFromEnvironment().APIKey != "environment-key" {
		t.Fatal("missing Gateway environment key")
	}
	t.Setenv("AGENT_CONVERSATION_MODEL_API_KEY", "dedicated-key")
	t.Setenv("AGENT_CONVERSATION_BASE_URL", "https://models.example.com")
	c := ConversationModelConfigFromEnvironment()
	if c.APIKey != "dedicated-key" || c.BaseURL != "https://models.example.com" {
		t.Fatal("dedicated key or configured service origin was lost")
	}
	c.Provider, c.Protocol, c.Model = "gateway", "messages", "chosen-model"
	c.URL = "https://override.example.com/custom/messages"
	m, err := NewConversationModel(c)
	if err != nil || m.config.URL != c.URL {
		t.Fatal("complete endpoint must take precedence over service origin", err)
	}
}
