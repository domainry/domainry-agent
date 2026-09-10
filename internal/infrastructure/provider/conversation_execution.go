package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

var conversationFunctionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func (m *ConversationModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	// The credential and HTTP client are deliberately excluded. Credential
	// rotation does not change the meaning of an already frozen model step.
	raw, _ := json.Marshal([]any{m.config.Provider, m.config.Protocol, m.config.URL, m.config.Model, m.config.MaxOutputTokens, "conversation-step-v1"})
	digest := sha256.Sum256(raw)
	return agentsdk.ConversationModelIdentity{Provider: m.config.Provider, Protocol: m.config.Protocol, Model: m.config.Model, Fingerprint: hex.EncodeToString(digest[:])}
}

func (m *ConversationModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	if emit == nil {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("model step callback required")
	}
	if in.ModelIdentity != m.ConversationModelIdentity() {
		return agentsdk.ConversationStepResult{}, &agentsdk.Error{Class: "conflict", Code: "agent.conversation.model_changed"}
	}
	payload, err := m.stepPayload(in)
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	resp, err := m.sendConversationPayload(ctx, payload, in.IdempotencyKey, true)
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	defer resp.Body.Close()
	kind, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || kind != "text/event-stream" {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("model step did not return SSE")
	}
	state := newExecutionStream(in, func(event agentsdk.ConversationModelEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return emit(event)
	})
	err = readConversationSSE(ctx, resp.Body, func(event string, raw []byte) (bool, error) {
		switch m.config.Protocol {
		case ConversationProtocolChat:
			return state.chat(event, raw)
		case ConversationProtocolMessages:
			return state.messages(event, raw)
		default:
			return state.responses(event, raw)
		}
	})
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return state.result(m.config.Protocol)
}

func (m *ConversationModel) stepPayload(in agentsdk.ConversationStepRequest) (map[string]any, error) {
	if err := validateStepRequest(in); err != nil {
		return nil, err
	}
	payload := map[string]any{"model": m.config.Model, "stream": true}
	tools := make([]any, 0, len(in.Tools))
	for _, tool := range in.Tools {
		fn := map[string]any{"name": tool.Key, "description": tool.Description, "parameters": tool.InputSchema}
		switch m.config.Protocol {
		case ConversationProtocolChat:
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		case ConversationProtocolMessages:
			tools = append(tools, map[string]any{"name": tool.Key, "description": tool.Description, "input_schema": tool.InputSchema})
		case ConversationProtocolResponses:
			fn["type"], fn["strict"] = "function", false
			tools = append(tools, fn)
		}
	}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	items := make([]any, 0, len(in.Messages))
	system := []string{}
	for _, message := range in.Messages {
		switch m.config.Protocol {
		case ConversationProtocolChat:
			item := map[string]any{"role": message.Role, "content": message.Content}
			if message.ToolCallID != "" {
				item["tool_call_id"] = message.ToolCallID
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}})
				}
				item["tool_calls"] = calls
			}
			if len(message.ProviderState) > 0 {
				var continuation map[string]json.RawMessage
				if json.Unmarshal(message.ProviderState, &continuation) != nil {
					return nil, fmt.Errorf("invalid chat continuation")
				}
				for key, value := range continuation {
					if key != "reasoning_content" && key != "reasoning" {
						return nil, fmt.Errorf("unsupported chat continuation field")
					}
					item[key] = value
				}
			}
			items = append(items, item)
		case ConversationProtocolMessages:
			if message.Role == "system" {
				if len(items) != 0 {
					return nil, fmt.Errorf("Messages requires leading system instructions")
				}
				system = append(system, message.Content)
				continue
			}
			role := message.Role
			blocks := []any{}
			if message.Role == "tool" {
				role = "user"
				blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content, "is_error": message.IsError})
			} else if len(message.ProviderState) > 0 {
				if err := validateContinuation(message, ConversationProtocolMessages); err != nil {
					return nil, err
				}
				_ = json.Unmarshal(message.ProviderState, &blocks)
			} else {
				if message.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
				}
				for _, call := range message.ToolCalls {
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": json.RawMessage(call.Arguments)})
				}
			}
			// Adjacent tool results must be in one user message immediately
			// following the assistant's tool_use blocks.
			if len(items) > 0 && items[len(items)-1].(map[string]any)["role"] == role {
				last := items[len(items)-1].(map[string]any)
				last["content"] = append(last["content"].([]any), blocks...)
			} else {
				items = append(items, map[string]any{"role": role, "content": blocks})
			}
		case ConversationProtocolResponses:
			if message.Role == "tool" {
				items = append(items, map[string]any{"type": "function_call_output", "call_id": message.ToolCallID, "output": message.Content})
			} else if len(message.ProviderState) > 0 {
				if err := validateContinuation(message, ConversationProtocolResponses); err != nil {
					return nil, err
				}
				var output []any
				_ = json.Unmarshal(message.ProviderState, &output)
				items = append(items, output...)
			} else {
				if message.Content != "" {
					items = append(items, map[string]any{"role": message.Role, "content": message.Content})
				}
				for _, call := range message.ToolCalls {
					items = append(items, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments})
				}
			}
		}
	}
	switch m.config.Protocol {
	case ConversationProtocolChat:
		payload["messages"], payload["stream_options"] = items, map[string]any{"include_usage": true}
		key := "max_tokens"
		if m.config.Provider == ConversationProviderGateway {
			key = "max_completion_tokens"
		}
		payload[key] = m.config.MaxOutputTokens
	case ConversationProtocolMessages:
		payload["messages"], payload["max_tokens"] = items, m.config.MaxOutputTokens
		if len(system) > 0 {
			payload["system"] = strings.Join(system, "\n\n")
		}
	case ConversationProtocolResponses:
		payload["input"], payload["max_output_tokens"], payload["store"] = items, m.config.MaxOutputTokens, false
		payload["include"] = []string{"reasoning.encrypted_content"}
	}
	return payload, nil
}

func validateStepRequest(in agentsdk.ConversationStepRequest) error {
	if len(in.Messages) == 0 || len(in.Messages) > 4096 || len(in.Tools) > 128 || in.MaxOutputBytes < 1 || in.MaxOutputBytes > maxResponseBytes || in.MaxArgumentBytes < 1 || in.MaxArgumentBytes > maxResponseBytes || in.MaxToolCalls < 1 || in.MaxToolCalls > 64 || len(in.IdempotencyKey) > 512 {
		return fmt.Errorf("invalid model step limits")
	}
	names := map[string]bool{}
	for _, tool := range in.Tools {
		var schema map[string]any
		if !conversationFunctionName.MatchString(tool.Key) || names[tool.Key] || strings.TrimSpace(tool.Version) == "" || !validModelText(tool.Description) || json.Unmarshal(tool.InputSchema, &schema) != nil || schema["type"] != "object" {
			return fmt.Errorf("invalid model step tool definition")
		}
		names[tool.Key] = true
	}
	pending, seen := map[string]bool{}, map[string]bool{}
	for _, message := range in.Messages {
		if !validModelText(message.Content) || (len(message.ProviderState) > 0 && (message.Role != "assistant" || !json.Valid(message.ProviderState))) {
			return fmt.Errorf("invalid model step content")
		}
		if message.Role == "tool" {
			if !pending[message.ToolCallID] || len(message.ToolCalls) != 0 {
				return fmt.Errorf("tool result without pending call")
			}
			delete(pending, message.ToolCallID)
			continue
		}
		if len(pending) > 0 || (message.Role != "system" && message.Role != "user" && message.Role != "assistant") || message.ToolCallID != "" || message.IsError || (len(message.ToolCalls) > 0 && message.Role != "assistant") {
			return fmt.Errorf("invalid model step message order")
		}
		for _, call := range message.ToolCalls {
			if !validStepCall(call, in.MaxArgumentBytes) || seen[call.ID] {
				return fmt.Errorf("invalid or duplicate prior tool call")
			}
			pending[call.ID], seen[call.ID] = true, true
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("tool results missing")
	}
	return nil
}

func validStepCall(call agentsdk.ConversationToolCall, maxBytes int) bool {
	var args map[string]json.RawMessage
	return call.ID != "" && len(call.ID) <= 256 && validModelText(call.ID) && conversationFunctionName.MatchString(call.Name) && len(call.Arguments) <= maxBytes && json.Unmarshal([]byte(call.Arguments), &args) == nil && args != nil
}

var _ agentsdk.ConversationAgentModel = (*ConversationModel)(nil)
