package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func executionTestRequest(m *ConversationModel) agentsdk.ConversationStepRequest {
	return agentsdk.ConversationStepRequest{ModelIdentity: m.ConversationModelIdentity(), IdempotencyKey: "run-step-1", MaxOutputBytes: 1024, MaxArgumentBytes: 1024, MaxToolCalls: 4,
		Messages: []agentsdk.ConversationStepMessage{{Role: "system", Content: "Use tools when needed."}, {Role: "user", Content: "Calculate 1 + 2"}},
		Tools:    []agentsdk.ConversationToolDefinition{{Key: "calculate", Version: "1", Description: "Calculate exactly", InputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "agent.tools.calculate", Effect: "read", Idempotency: "natural", TimeoutMillis: 1000, MaxOutputBytes: 1024}}}
}

// These are wire-protocol fixtures, including provider-native continuation
// state. The second request proves that a tool result can actually continue
// the same stateless conversation through each supported protocol.
func executionWire(protocol string, tool bool) []map[string]any {
	if protocol == ConversationProtocolChat {
		delta := func(v any, finish string) map[string]any {
			return map[string]any{"model": "served-model", "choices": []any{map[string]any{"index": 0, "delta": v, "finish_reason": finish}}}
		}
		if tool {
			return []map[string]any{
				delta(map[string]any{"role": "assistant", "reasoning_content": "private protocol continuation"}, ""),
				delta(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "calculate", "arguments": "{\"expression\":"}}}}, ""),
				delta(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": "\"1 + 2\"}"}}}}, ""),
				delta(map[string]any{}, "tool_calls"), {"choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 4}},
			}
		}
		return []map[string]any{delta(map[string]any{"role": "assistant", "content": "结果"}, ""), delta(map[string]any{"content": "是 3。"}, ""), delta(map[string]any{}, "stop")}
	}
	if protocol == ConversationProtocolMessages {
		out := []map[string]any{{"type": "message_start", "message": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "served-model", "content": []any{}, "usage": map[string]any{"input_tokens": 10}}}}
		if tool {
			return append(out,
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}},
				map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "private protocol continuation"}},
				map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "signed-reasoning"}},
				map[string]any{"type": "content_block_stop", "index": 0},
				map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "call_1", "name": "calculate", "input": map[string]any{}}},
				map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": "{\"expression\":"}},
				map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": "\"1 + 2\"}"}},
				map[string]any{"type": "content_block_stop", "index": 1},
				map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 4}},
				map[string]any{"type": "message_stop"})
		}
		return append(out, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "结果"}},
			map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "是 3。"}},
			map[string]any{"type": "content_block_stop", "index": 0}, map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}}, map[string]any{"type": "message_stop"})
	}
	response := func(status string, output []any) map[string]any {
		return map[string]any{"id": "resp_1", "status": status, "model": "served-model", "output": output, "usage": map[string]any{"total_tokens": 14}}
	}
	out := []map[string]any{{"type": "response.created", "response": response("in_progress", []any{})}}
	if tool {
		reasoning := map[string]any{"id": "rs_1", "type": "reasoning", "summary": []any{}, "encrypted_content": "opaque-encrypted-state"}
		call := map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "calculate", "arguments": `{"expression":"1 + 2"}`, "status": "completed"}
		return append(out, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "rs_1", "type": "reasoning", "summary": []any{}}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": reasoning},
			map[string]any{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "calculate", "arguments": ""}},
			map[string]any{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_1", "delta": "{\"expression\":"},
			map[string]any{"type": "response.function_call_arguments.delta", "output_index": 1, "item_id": "fc_1", "delta": "\"1 + 2\"}"},
			map[string]any{"type": "response.function_call_arguments.done", "output_index": 1, "item_id": "fc_1", "arguments": `{"expression":"1 + 2"}`},
			map[string]any{"type": "response.output_item.done", "output_index": 1, "item": call},
			map[string]any{"type": "response.completed", "response": response("completed", []any{reasoning, call})})
	}
	part := map[string]any{"type": "output_text", "text": "结果是 3。", "annotations": []any{}}
	message := map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
	return append(out, map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}},
		map[string]any{"type": "response.content_part.added", "output_index": 0, "item_id": "msg_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}},
		map[string]any{"type": "response.output_text.delta", "output_index": 0, "item_id": "msg_1", "content_index": 0, "delta": "结果"},
		map[string]any{"type": "response.output_text.delta", "output_index": 0, "item_id": "msg_1", "content_index": 0, "delta": "是 3。"},
		map[string]any{"type": "response.output_text.done", "output_index": 0, "item_id": "msg_1", "content_index": 0, "text": "结果是 3。"},
		map[string]any{"type": "response.content_part.done", "output_index": 0, "item_id": "msg_1", "content_index": 0, "part": part},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message},
		map[string]any{"type": "response.completed", "response": response("completed", []any{message})})
}

func executionFrame(v map[string]any) string {
	raw, _ := json.Marshal(v)
	return "data: " + string(raw) + "\n\n"
}
func executionFrames(protocol string, tool bool) string {
	var out strings.Builder
	for _, v := range executionWire(protocol, tool) {
		out.WriteString(executionFrame(v))
	}
	if protocol == ConversationProtocolChat {
		out.WriteString("data: [DONE]\n\n")
	}
	return out.String()
}

func TestConversationExecutionProtocolsContinueToolsWithoutProviderSession(t *testing.T) {
	for _, protocol := range []string{ConversationProtocolChat, ConversationProtocolMessages, ConversationProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			release := make(chan struct{})
			defer close(release)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&payload) != nil {
					t.Error("invalid request")
					return
				}
				n := requests.Add(1)
				if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("Idempotency-Key") == "" || string(payload["stream"]) != "true" || payload["tools"] == nil {
					t.Error("missing authentication, idempotency or tools")
				}
				for _, key := range []string{"authority", "confirmation", "previous_response_id", "conversation", "execution_credential", "compaction", "result_reference"} {
					if payload[key] != nil {
						t.Errorf("unexpected request field %s", key)
					}
				}
				if strings.Contains(string(payload["tools"]), "action_key") || strings.Contains(string(payload["tools"]), "idempotency") {
					t.Error("host-only tool policy leaked to model")
				}
				if protocol == ConversationProtocolResponses && (string(payload["store"]) != "false" || !strings.Contains(string(payload["include"]), "reasoning.encrypted_content")) {
					t.Error("missing stateless reasoning continuation")
				}
				if n == 2 {
					key := "messages"
					if protocol == ConversationProtocolResponses {
						key = "input"
					}
					body := string(payload[key])
					if strings.Contains(body, "server-only-reference") || strings.Contains(body, "result_reference") {
						t.Error("server-side compaction metadata leaked through protocol translation")
					}
					if !strings.Contains(body, "call_1") || !strings.Contains(body, `\"value\":3`) {
						t.Error("lost tool call or result")
					}
					switch protocol {
					case ConversationProtocolChat:
						if !strings.Contains(body, "reasoning_content") {
							t.Error("lost chat continuation")
						}
					case ConversationProtocolMessages:
						if !strings.Contains(body, "signed-reasoning") || !strings.Contains(body, "tool_result") {
							t.Error("lost signed blocks or tool result")
						}
					case ConversationProtocolResponses:
						if !strings.Contains(body, "opaque-encrypted-state") || !strings.Contains(body, "function_call_output") {
							t.Error("lost encrypted reasoning or output")
						}
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, frame := range executionWire(protocol, n == 1) {
					fmt.Fprint(w, executionFrame(frame))
					w.(http.Flusher).Flush()
					if strings.Contains(executionFrame(frame), `\"expression\":`) && n == 1 && !strings.Contains(executionFrame(frame), `arguments.done`) && (frame["type"] == "response.function_call_arguments.delta" || frame["type"] == "content_block_delta" || frame["choices"] != nil) {
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
				}
				if protocol == ConversationProtocolChat {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer upstream.Close()
			m, err := NewConversationModel(ConversationModelConfig{Provider: "gateway", Protocol: protocol, URL: upstream.URL, APIKey: "test-key", Model: "selected", Client: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			in := executionTestRequest(m)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			argumentText := ""
			gotEarly := false
			first, err := m.StreamConversationStep(ctx, in, func(event agentsdk.ConversationModelEvent) error {
				if strings.Contains(event.Delta, "private protocol") || strings.Contains(event.Delta, "opaque-encrypted") {
					t.Error("reasoning leaked to preview")
				}
				if event.Type == "tool.arguments.delta" {
					if event.Offset != len(argumentText) {
						t.Error("argument offset mismatch")
					}
					argumentText += event.Delta
					if !gotEarly {
						gotEarly = true
						release <- struct{}{}
					}
				}
				return nil
			})
			if err != nil || first.FinishReason != "tool_calls" || len(first.Message.ToolCalls) != 1 || argumentText != `{"expression":"1 + 2"}` || !gotEarly {
				t.Fatalf("tool step: %+v %v", first, err)
			}
			in.Messages = append(in.Messages, first.Message, agentsdk.ConversationStepMessage{Role: "tool", ToolCallID: "call_1", Content: `{"value":3}`})
			in.Messages[len(in.Messages)-1].ResultReference = &agentsdk.ConversationResultReference{ConversationID: "server-only-reference", RunID: "run", CallID: "call_1", SHA256: strings.Repeat("f", 64)}
			in.Compaction = &agentsdk.ConversationContextCompaction{Version: 1, Results: 1, BeforeBytes: 20000, AfterBytes: 2000}
			in.IdempotencyKey = "run-step-2"
			text := ""
			second, err := m.StreamConversationStep(ctx, in, func(event agentsdk.ConversationModelEvent) error {
				if event.Type == "text.delta" {
					if event.Offset != len(text) {
						t.Error("text byte offset mismatch")
					}
					text += event.Delta
				}
				return nil
			})
			if err != nil || second.FinishReason != "stop" || text != "结果是 3。" || second.Message.Content != text || requests.Load() != 2 {
				t.Fatalf("reply step: %+v %v", second, err)
			}
		})
	}
}

func TestConversationExecutionRejectsIncompleteAndInconsistentStreams(t *testing.T) {
	for _, protocol := range []string{ConversationProtocolChat, ConversationProtocolMessages, ConversationProtocolResponses} {
		wire := executionFrames(protocol, true)
		cases := map[string]string{
			"eof":           strings.Join(strings.Split(wire, "\n\n")[:len(strings.Split(wire, "\n\n"))-2], "\n\n") + "\n\n",
			"unknown-tool":  strings.ReplaceAll(wire, "calculate", "unregistered"),
			"bad-arguments": strings.ReplaceAll(wire, `\"1 + 2\"}`, `\"1 + 2\"`),
		}
		switch protocol {
		case ConversationProtocolChat:
			cases["truncated"] = strings.ReplaceAll(wire, `"finish_reason":"tool_calls"`, `"finish_reason":"length"`)
			cases["wrong-stop"] = strings.ReplaceAll(wire, `"finish_reason":"tool_calls"`, `"finish_reason":"stop"`)
		case ConversationProtocolMessages:
			cases["truncated"] = strings.ReplaceAll(wire, `"stop_reason":"tool_use"`, `"stop_reason":"max_tokens"`)
			cases["wrong-stop"] = strings.ReplaceAll(wire, `"stop_reason":"tool_use"`, `"stop_reason":"end_turn"`)
		case ConversationProtocolResponses:
			cases["truncated"] = strings.ReplaceAll(wire, `"type":"response.completed"`, `"type":"response.incomplete"`)
			cases["different-final"] = strings.ReplaceAll(wire, `"arguments":"{\"expression\":\"1 + 2\"}"`, `"arguments":"{\"expression\":\"2 + 2\"}"`)
		}
		for name, body := range cases {
			t.Run(protocol+"/"+name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, body)
				}))
				defer upstream.Close()
				m, _ := NewConversationModel(ConversationModelConfig{Protocol: protocol, URL: upstream.URL, Model: "test"})
				out, err := m.StreamConversationStep(t.Context(), executionTestRequest(m), func(agentsdk.ConversationModelEvent) error { return nil })
				if err == nil || len(out.Message.ToolCalls) != 0 {
					t.Fatalf("accepted invalid stream: %+v %v", out, err)
				}
			})
		}
	}
}

func TestConversationExecutionFencesModelChangesAndHonorsCallbackFailure(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, executionFrames(ConversationProtocolChat, true))
	}))
	defer upstream.Close()
	m, _ := NewConversationModel(ConversationModelConfig{URL: upstream.URL, Model: "one"})
	in := executionTestRequest(m)
	in.ModelIdentity.Model = "two"
	if _, err := m.StreamConversationStep(t.Context(), in, func(agentsdk.ConversationModelEvent) error { return nil }); err == nil || count.Load() != 0 {
		t.Fatal("changed model was called")
	}
	in = executionTestRequest(m)
	stop := errors.New("event persistence failed")
	if out, err := m.StreamConversationStep(t.Context(), in, func(agentsdk.ConversationModelEvent) error { return stop }); !errors.Is(err, stop) || out.FinishReason != "" {
		t.Fatalf("callback failure lost: %+v %v", out, err)
	}
	in = executionTestRequest(m)
	in.MaxArgumentBytes = 5
	if _, err := m.StreamConversationStep(t.Context(), in, func(agentsdk.ConversationModelEvent) error { return nil }); err == nil {
		t.Fatal("accepted oversized arguments")
	}
}

func TestConversationExecutionValidatesToolResultPairingAndContinuation(t *testing.T) {
	m, _ := NewConversationModel(ConversationModelConfig{Protocol: ConversationProtocolMessages, URL: "https://example.test/messages", Model: "test"})
	in := executionTestRequest(m)
	in.Messages = append(in.Messages, agentsdk.ConversationStepMessage{Role: "tool", ToolCallID: "foreign", Content: "{}"})
	if _, err := m.stepPayload(in); err == nil {
		t.Fatal("accepted unrelated tool result")
	}
	in = executionTestRequest(m)
	call := agentsdk.ConversationToolCall{ID: "call", Name: "calculate", Arguments: `{"expression":"1 + 2"}`}
	in.Messages = append(in.Messages, agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call}})
	if _, err := m.stepPayload(in); err == nil {
		t.Fatal("accepted missing tool result")
	}
	in.Messages = append(in.Messages, agentsdk.ConversationStepMessage{Role: "tool", ToolCallID: "call", Content: "{}"})
	if _, err := m.stepPayload(in); err != nil {
		t.Fatal(err)
	}
	in.Messages[2].ProviderState = json.RawMessage(`[{"type":"tool_use","id":"call","name":"calculate","input":{"expression":"different"}}]`)
	if _, err := m.stepPayload(in); err == nil {
		t.Fatal("accepted different native continuation")
	}
}
