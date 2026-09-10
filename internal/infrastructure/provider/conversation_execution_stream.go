package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type executionCall struct {
	call    agentsdk.ConversationToolCall
	started bool
}

type executionStream struct {
	in                agentsdk.ConversationStepRequest
	emit              func(agentsdk.ConversationModelEvent) error
	text              strings.Builder
	calls             map[int]*executionCall
	argumentBytes     int
	model, finish     string
	usage             map[string]any
	started, finished bool
	blocks            []map[string]any
	openBlock         int
	blockCall         map[int]int
	responseItems     map[int]*executionResponseItem
	responseID        string
	continuation      json.RawMessage
	reasoning         map[string]string
}

func newExecutionStream(in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) *executionStream {
	return &executionStream{in: in, emit: emit, calls: map[int]*executionCall{}, usage: map[string]any{}, openBlock: -1, blockCall: map[int]int{}, responseItems: map[int]*executionResponseItem{}, reasoning: map[string]string{}}
}

func (s *executionStream) mergeUsage(usage map[string]any) {
	for key, value := range usage {
		s.usage[key] = value
	}
}

func (s *executionStream) textDelta(value string) error {
	if s.finished || !validModelText(value) || len(value) > s.in.MaxOutputBytes-s.text.Len() {
		return fmt.Errorf("invalid model step text delta")
	}
	if value == "" {
		return nil
	}
	if err := s.emit(agentsdk.ConversationModelEvent{Type: "text.delta", Offset: s.text.Len(), Delta: value}); err != nil {
		return err
	}
	s.text.WriteString(value)
	return nil
}

func (s *executionStream) call(index int) (*executionCall, error) {
	if index < 0 || index >= s.in.MaxToolCalls || s.finished {
		return nil, fmt.Errorf("invalid model tool index")
	}
	call := s.calls[index]
	if call == nil {
		call = &executionCall{}
		s.calls[index] = call
	}
	return call, nil
}

func (s *executionStream) startCall(index int) error {
	call, err := s.call(index)
	if err != nil {
		return err
	}
	if call.started {
		return nil
	}
	if call.call.ID == "" || !conversationFunctionName.MatchString(call.call.Name) {
		return fmt.Errorf("tool arguments without call identity")
	}
	if err = s.emit(agentsdk.ConversationModelEvent{Type: "tool.started", Index: index, CallID: call.call.ID, Name: call.call.Name}); err != nil {
		return err
	}
	call.started = true
	return nil
}

func (s *executionStream) argumentDelta(index int, value string) error {
	call, err := s.call(index)
	if err != nil {
		return err
	}
	if !validModelText(value) || len(value) > s.in.MaxArgumentBytes-s.argumentBytes {
		return fmt.Errorf("model tool arguments too large or invalid")
	}
	if value == "" {
		return nil
	}
	if err = s.startCall(index); err != nil {
		return err
	}
	if err = s.emit(agentsdk.ConversationModelEvent{Type: "tool.arguments.delta", Index: index, CallID: call.call.ID, Name: call.call.Name, Offset: len(call.call.Arguments), Delta: value}); err != nil {
		return err
	}
	call.call.Arguments += value
	s.argumentBytes += len(value)
	return nil
}

func (s *executionStream) orderedCalls() ([]agentsdk.ConversationToolCall, error) {
	out := make([]agentsdk.ConversationToolCall, 0, len(s.calls))
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, tool := range s.in.Tools {
		allowed[tool.Key] = true
	}
	for i := 0; i < len(s.calls); i++ {
		call := s.calls[i]
		if call == nil || !validStepCall(call.call, s.in.MaxArgumentBytes) || seen[call.call.ID] || !allowed[call.call.Name] {
			return nil, fmt.Errorf("invalid, duplicate or unavailable model tool call")
		}
		seen[call.call.ID] = true
		out = append(out, call.call)
	}
	return out, nil
}

func (s *executionStream) complete(reason string) error {
	if s.finished {
		return fmt.Errorf("duplicate model step completion")
	}
	if reason != "stop" && reason != "tool_calls" {
		return fmt.Errorf("model step truncated or unsupported finish")
	}
	calls, err := s.orderedCalls()
	if err != nil {
		return err
	}
	if (reason == "tool_calls") != (len(calls) > 0) || (len(calls) == 0 && strings.TrimSpace(s.text.String()) == "") {
		return fmt.Errorf("model step finish does not match content")
	}
	for i := range calls {
		if err := s.startCall(i); err != nil {
			return err
		}
	}
	s.finish, s.finished = reason, true
	return nil
}

func (s *executionStream) result(protocol string) (agentsdk.ConversationStepResult, error) {
	if !s.finished {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("model step incomplete")
	}
	calls, err := s.orderedCalls()
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	state := s.continuation
	if protocol == ConversationProtocolMessages {
		state, err = json.Marshal(s.blocks)
	}
	if protocol == ConversationProtocolChat && len(s.reasoning) > 0 {
		state, err = json.Marshal(s.reasoning)
	}
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: s.text.String(), ToolCalls: calls, ProviderState: state}, FinishReason: s.finish, Model: s.model, Usage: s.usage}, nil
}

func (s *executionStream) chat(event string, raw []byte) (bool, error) {
	if event == "error" {
		return false, fmt.Errorf("model chat stream error")
	}
	if string(raw) == "[DONE]" {
		if !s.finished {
			return false, fmt.Errorf("model chat stream missing finish")
		}
		return true, nil
	}
	var v struct {
		Model   string          `json:"model"`
		Usage   map[string]any  `json:"usage"`
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index  int    `json:"index"`
			Finish string `json:"finish_reason"`
			Delta  struct {
				Role             string          `json:"role"`
				Content          *string         `json:"content"`
				Refusal          string          `json:"refusal"`
				ReasoningContent string          `json:"reasoning_content"`
				Reasoning        string          `json:"reasoning"`
				FunctionCall     json.RawMessage `json:"function_call"`
				Calls            []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &v) != nil || presentJSON(v.Error) {
		return false, fmt.Errorf("invalid model chat event")
	}
	s.mergeUsage(v.Usage)
	if v.Model != "" {
		s.model = v.Model
	}
	if len(v.Choices) == 0 && v.Usage != nil {
		return false, nil
	}
	if len(v.Choices) != 1 || v.Choices[0].Index != 0 || s.finished {
		return false, fmt.Errorf("unexpected model chat choice")
	}
	c := v.Choices[0]
	if c.Delta.Role != "" && c.Delta.Role != "assistant" || presentJSON(c.Delta.FunctionCall) {
		return false, fmt.Errorf("unsupported chat delta")
	}
	if c.Delta.Content != nil {
		if err := s.textDelta(*c.Delta.Content); err != nil {
			return false, err
		}
	}
	if err := s.textDelta(c.Delta.Refusal); err != nil {
		return false, err
	}
	if c.Delta.ReasoningContent != "" {
		s.reasoning["reasoning_content"] += c.Delta.ReasoningContent
	}
	if c.Delta.Reasoning != "" {
		s.reasoning["reasoning"] += c.Delta.Reasoning
	}
	for _, delta := range c.Delta.Calls {
		call, err := s.call(delta.Index)
		if err != nil {
			return false, err
		}
		if delta.Type != "" && delta.Type != "function" {
			return false, fmt.Errorf("unsupported chat tool type")
		}
		if call.started && (delta.ID != "" || delta.Function.Name != "") {
			return false, fmt.Errorf("tool identity changed during arguments")
		}
		call.call.ID += delta.ID
		call.call.Name += delta.Function.Name
		if len(call.call.ID) > 256 || len(call.call.Name) > 64 {
			return false, fmt.Errorf("tool identity too large")
		}
		if err = s.argumentDelta(delta.Index, delta.Function.Arguments); err != nil {
			return false, err
		}
	}
	if c.Finish != "" {
		return false, s.complete(c.Finish)
	}
	return false, nil
}

func (s *executionStream) messages(event string, raw []byte) (bool, error) {
	var v struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			Type    string         `json:"type"`
			Role    string         `json:"role"`
			Model   string         `json:"model"`
			Content []any          `json:"content"`
			Usage   map[string]any `json:"usage"`
		} `json:"message"`
		Block map[string]any `json:"content_block"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			JSON       string `json:"partial_json"`
			Thinking   string `json:"thinking"`
			Signature  string `json:"signature"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage map[string]any `json:"usage"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) || decoder.Decode(&v) != nil || v.Type == "" || (event != "" && event != v.Type) {
		return false, fmt.Errorf("invalid Messages step event")
	}
	switch v.Type {
	case "error":
		return false, fmt.Errorf("Messages step error")
	case "ping":
		return false, nil
	case "message_start":
		if s.started || v.Message.Type != "message" || v.Message.Role != "assistant" || len(v.Message.Content) != 0 {
			return false, fmt.Errorf("invalid Messages step start")
		}
		s.started = true
		s.model = v.Message.Model
		s.mergeUsage(v.Message.Usage)
	case "content_block_start":
		if !s.started || s.finished || s.openBlock != -1 || v.Index != len(s.blocks) {
			return false, fmt.Errorf("invalid Messages block order")
		}
		switch v.Block["type"] {
		case "text":
			if err := s.textDelta(blockString(v.Block, "text")); err != nil {
				return false, err
			}
		case "thinking", "redacted_thinking":
		case "tool_use":
			index := len(s.calls)
			call, err := s.call(index)
			if err != nil {
				return false, err
			}
			call.call.ID, call.call.Name = blockString(v.Block, "id"), blockString(v.Block, "name")
			s.blockCall[v.Index] = index
			if err = s.startCall(index); err != nil {
				return false, err
			}
		default:
			return false, fmt.Errorf("unsupported Messages block type")
		}
		s.blocks = append(s.blocks, v.Block)
		s.openBlock = v.Index
	case "content_block_delta":
		if s.openBlock != v.Index || s.finished || v.Index < 0 || v.Index >= len(s.blocks) {
			return false, fmt.Errorf("Messages delta without open block")
		}
		block := s.blocks[v.Index]
		switch v.Delta.Type {
		case "text_delta":
			if block["type"] != "text" {
				return false, fmt.Errorf("Messages text type mismatch")
			}
			if err := s.textDelta(v.Delta.Text); err != nil {
				return false, err
			}
			block["text"] = blockString(block, "text") + v.Delta.Text
		case "input_json_delta":
			if block["type"] != "tool_use" {
				return false, fmt.Errorf("Messages arguments type mismatch")
			}
			if err := s.argumentDelta(s.blockCall[v.Index], v.Delta.JSON); err != nil {
				return false, err
			}
		case "thinking_delta":
			if block["type"] != "thinking" {
				return false, fmt.Errorf("Messages thinking type mismatch")
			}
			block["thinking"] = blockString(block, "thinking") + v.Delta.Thinking
		case "signature_delta":
			if block["type"] != "thinking" {
				return false, fmt.Errorf("Messages signature type mismatch")
			}
			block["signature"] = blockString(block, "signature") + v.Delta.Signature
		default:
			return false, fmt.Errorf("unsupported Messages delta type")
		}
	case "content_block_stop":
		if s.openBlock != v.Index || s.finished || v.Index < 0 || v.Index >= len(s.blocks) {
			return false, fmt.Errorf("Messages block closed out of order")
		}
		block := s.blocks[v.Index]
		if block["type"] == "tool_use" {
			index := s.blockCall[v.Index]
			call := s.calls[index]
			if call.call.Arguments == "" {
				raw, _ := json.Marshal(block["input"])
				if err := s.argumentDelta(index, string(raw)); err != nil {
					return false, err
				}
			}
			if !validStepCall(call.call, s.in.MaxArgumentBytes) {
				return false, fmt.Errorf("invalid completed Messages tool arguments")
			}
			block["input"] = json.RawMessage(call.call.Arguments)
		}
		s.openBlock = -1
	case "message_delta":
		if !s.started || s.openBlock != -1 || s.finished {
			return false, fmt.Errorf("invalid Messages finish order")
		}
		s.mergeUsage(v.Usage)
		if v.Delta.StopReason != "" {
			reason := v.Delta.StopReason
			if reason == "end_turn" {
				reason = "stop"
			}
			if reason == "tool_use" {
				reason = "tool_calls"
			}
			return false, s.complete(reason)
		}
	case "message_stop":
		if !s.finished || s.openBlock != -1 {
			return false, fmt.Errorf("Messages stopped without completed step")
		}
		return true, nil
	}
	return false, nil
}

func blockString(block map[string]any, key string) string {
	value, _ := block[key].(string)
	return value
}

// The native continuation must describe the same text and calls as the
// normalized history. This prevents stale or mismatched state from silently
// changing a frozen step when translating protocols.
func validateContinuation(message agentsdk.ConversationStepMessage, protocol string) error {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(message.ProviderState, &blocks) != nil || blocks == nil {
		return fmt.Errorf("invalid provider continuation")
	}
	var content strings.Builder
	calls := []agentsdk.ConversationToolCall{}
	for _, block := range blocks {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		switch {
		case protocol == ConversationProtocolMessages && kind == "text":
			var text string
			if json.Unmarshal(block["text"], &text) != nil {
				return fmt.Errorf("invalid continuation text")
			}
			content.WriteString(text)
		case protocol == ConversationProtocolMessages && kind == "tool_use":
			var call agentsdk.ConversationToolCall
			_ = json.Unmarshal(block["id"], &call.ID)
			_ = json.Unmarshal(block["name"], &call.Name)
			call.Arguments = string(block["input"])
			calls = append(calls, call)
		case protocol == ConversationProtocolMessages && (kind == "thinking" || kind == "redacted_thinking"):
		case protocol == ConversationProtocolResponses && kind == "message":
			var role string
			_ = json.Unmarshal(block["role"], &role)
			if role != "assistant" {
				return fmt.Errorf("invalid continuation role")
			}
			var parts []contentBlock
			if json.Unmarshal(block["content"], &parts) != nil {
				return fmt.Errorf("invalid response continuation content")
			}
			for _, part := range parts {
				if part.Type == "output_text" {
					content.WriteString(part.Text)
				} else if part.Type == "refusal" {
					content.WriteString(part.Refusal)
				} else {
					return fmt.Errorf("unsupported response continuation content")
				}
			}
		case protocol == ConversationProtocolResponses && kind == "function_call":
			var call agentsdk.ConversationToolCall
			_ = json.Unmarshal(block["call_id"], &call.ID)
			_ = json.Unmarshal(block["name"], &call.Name)
			_ = json.Unmarshal(block["arguments"], &call.Arguments)
			calls = append(calls, call)
		case protocol == ConversationProtocolResponses && kind == "reasoning":
		default:
			return fmt.Errorf("unsupported continuation item")
		}
	}
	if content.String() != message.Content || len(calls) != len(message.ToolCalls) {
		return fmt.Errorf("continuation differs from normalized message")
	}
	for i, call := range calls {
		other := message.ToolCalls[i]
		if call.ID != other.ID || call.Name != other.Name || !equalArgumentJSON(call.Arguments, other.Arguments) {
			return fmt.Errorf("continuation tool differs from normalized call")
		}
	}
	return nil
}

func equalArgumentJSON(left, right string) bool {
	// Decode with UseNumber so large integers never lose precision.
	decode := func(s string) string {
		var value any
		d := json.NewDecoder(strings.NewReader(s))
		d.UseNumber()
		if d.Decode(&value) != nil || !json.Valid([]byte(s)) {
			return ""
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(raw)
	}
	l, r := decode(left), decode(right)
	return l != "" && l == r
}

func sortedResponseIndexes(items map[int]*executionResponseItem) []int {
	keys := make([]int, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}
