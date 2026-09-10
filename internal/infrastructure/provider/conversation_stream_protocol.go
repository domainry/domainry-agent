package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

type conversationStreamState struct {
	text              strings.Builder
	model             string
	usage             map[string]any
	started, finished bool
	blocks            map[int]string
	nextBlock         int
}

func (s *conversationStreamState) mergeUsage(v map[string]any) {
	for key, value := range v {
		s.usage[key] = value
	}
}
func (s *conversationStreamState) chat(event string, raw []byte, emit func(string) error) (bool, error) {
	if event == "error" {
		return false, fmt.Errorf("conversation stream error")
	}
	if string(raw) == "[DONE]" {
		if !s.finished {
			return false, fmt.Errorf("conversation stream has no normal finish")
		}
		return true, nil
	}
	var v chatEnvelope
	if json.Unmarshal(raw, &v) != nil || presentJSON(v.Error) {
		return false, fmt.Errorf("invalid conversation chat event")
	}
	if v.Model != "" {
		s.model = v.Model
	}
	s.mergeUsage(v.Usage)
	if len(v.Choices) == 0 && v.Usage != nil {
		return false, nil
	}
	if len(v.Choices) != 1 || v.Choices[0].Index != 0 || s.finished {
		return false, fmt.Errorf("unexpected conversation chat choice")
	}
	choice := v.Choices[0]
	if choice.FinishReason != "" && choice.FinishReason != "stop" {
		return false, fmt.Errorf("conversation chat stream truncated or unsupported")
	}
	text, err := choice.Delta.text()
	if err != nil {
		return false, err
	}
	if err = emit(text); err != nil {
		return false, err
	}
	s.finished = choice.FinishReason == "stop"
	return false, nil
}
func (s *conversationStreamState) messages(event string, raw []byte, emit func(string) error) (bool, error) {
	var v struct {
		Type    string           `json:"type"`
		Index   int              `json:"index"`
		Message messagesEnvelope `json:"message"`
		Block   contentBlock     `json:"content_block"`
		Delta   struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Type == "" || (event != "" && event != v.Type) {
		return false, fmt.Errorf("invalid conversation Messages event")
	}
	switch v.Type {
	case "error":
		return false, fmt.Errorf("conversation Messages stream error")
	case "message_start":
		if s.started || v.Message.Type != "message" || v.Message.Role != "assistant" || len(v.Message.Content) > 0 {
			return false, fmt.Errorf("invalid conversation message start")
		}
		s.started = true
		s.model = v.Message.Model
		s.mergeUsage(v.Message.Usage)
	case "content_block_start":
		if !s.started || s.finished || len(s.blocks) > 0 || v.Index != s.nextBlock {
			return false, fmt.Errorf("unexpected conversation block start")
		}
		switch v.Block.Type {
		case "text", "thinking", "redacted_thinking":
		default:
			return false, fmt.Errorf("unsupported conversation stream block")
		}
		s.blocks[v.Index] = v.Block.Type
		s.nextBlock++
		if v.Block.Type == "text" {
			return false, emit(v.Block.Text)
		}
	case "content_block_delta":
		kind, ok := s.blocks[v.Index]
		if !ok || s.finished {
			return false, fmt.Errorf("conversation delta without open block")
		}
		switch v.Delta.Type {
		case "text_delta":
			if kind != "text" {
				return false, fmt.Errorf("conversation text delta block mismatch")
			}
			return false, emit(v.Delta.Text)
		case "thinking_delta", "signature_delta":
			if kind != "thinking" {
				return false, fmt.Errorf("conversation thinking block mismatch")
			}
		default:
			return false, fmt.Errorf("unsupported conversation stream delta")
		}
	case "content_block_stop":
		if _, ok := s.blocks[v.Index]; !ok {
			return false, fmt.Errorf("conversation block stop without start")
		}
		delete(s.blocks, v.Index)
	case "message_delta":
		if !s.started || len(s.blocks) > 0 || s.finished || v.Delta.StopReason != "end_turn" {
			return false, fmt.Errorf("conversation Messages stream truncated or unsupported")
		}
		s.finished = true
		s.mergeUsage(v.Usage)
	case "message_stop":
		if !s.finished || len(s.blocks) > 0 {
			return false, fmt.Errorf("incomplete conversation Messages stream")
		}
		return true, nil
		// Ping and future non-content metadata events do not affect completion.
	}
	return false, nil
}
func (s *conversationStreamState) responses(event string, raw []byte, emit func(string) error) (bool, error) {
	var v struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Item     responseItem    `json:"item"`
		Part     contentBlock    `json:"part"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Type == "" || (event != "" && event != v.Type) {
		return false, fmt.Errorf("invalid conversation Responses event")
	}
	switch v.Type {
	case "error", "response.failed", "response.incomplete":
		return false, fmt.Errorf("conversation Responses stream failed or incomplete")
	case "response.output_text.delta", "response.refusal.delta":
		return false, emit(v.Delta)
	case "response.output_item.added", "response.output_item.done":
		if v.Item.Type != "reasoning" && (v.Item.Type != "message" || v.Item.Role != "assistant") {
			return false, fmt.Errorf("unsupported conversation Responses item")
		}
	case "response.content_part.added", "response.content_part.done":
		if v.Part.Type != "output_text" && v.Part.Type != "refusal" {
			return false, fmt.Errorf("unsupported conversation Responses content")
		}
	case "response.completed":
		result, err := decodeResponse(v.Response)
		if err != nil {
			return false, err
		}
		if result.Content != s.text.String() {
			return false, fmt.Errorf("conversation Responses final text differs from deltas")
		}
		s.model = result.Model
		s.mergeUsage(result.Usage)
		return true, nil
	}
	return false, nil
}
