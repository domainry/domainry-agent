package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type executionResponseItem struct {
	id, kind            string
	callIndex           int
	parts               map[int]string
	partKinds           map[int]string
	closedParts         map[int]bool
	done, argumentsDone bool
}

func (s *executionStream) responses(event string, raw []byte) (bool, error) {
	var v struct {
		Type         string                     `json:"type"`
		OutputIndex  *int                       `json:"output_index"`
		ContentIndex int                        `json:"content_index"`
		ItemID       string                     `json:"item_id"`
		Delta        string                     `json:"delta"`
		Arguments    string                     `json:"arguments"`
		Text         string                     `json:"text"`
		Refusal      string                     `json:"refusal"`
		Item         map[string]json.RawMessage `json:"item"`
		Part         contentBlock               `json:"part"`
		Response     struct {
			ID         string          `json:"id"`
			Status     string          `json:"status"`
			Model      string          `json:"model"`
			Output     json.RawMessage `json:"output"`
			Usage      map[string]any  `json:"usage"`
			Error      json.RawMessage `json:"error"`
			Incomplete json.RawMessage `json:"incomplete_details"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Type == "" || event != "" && event != v.Type {
		return false, fmt.Errorf("invalid Responses step event")
	}
	if s.finished {
		return false, fmt.Errorf("Responses event after finish")
	}
	if v.Type == "error" || v.Type == "response.failed" || v.Type == "response.incomplete" {
		return false, fmt.Errorf("Responses step failed or incomplete")
	}
	if v.Type == "response.created" {
		if s.started || v.Response.ID == "" {
			return false, fmt.Errorf("invalid Responses start")
		}
		s.started = true
		s.responseID = v.Response.ID
		s.model = v.Response.Model
		return false, nil
	}
	if !s.started {
		return false, fmt.Errorf("Responses step has no start")
	}
	if v.Type == "response.in_progress" {
		return false, nil
	}
	if v.Type == "response.completed" {
		if v.Response.ID != s.responseID || v.Response.Status != "completed" || presentJSON(v.Response.Error) || presentJSON(v.Response.Incomplete) {
			return false, fmt.Errorf("invalid Responses completion")
		}
		var output []json.RawMessage
		if json.Unmarshal(v.Response.Output, &output) != nil || len(output) != len(s.responseItems) {
			return false, fmt.Errorf("Responses final items differ from stream")
		}
		for _, index := range sortedResponseIndexes(s.responseItems) {
			item := s.responseItems[index]
			if !item.done || index >= len(output) {
				return false, fmt.Errorf("Responses item incomplete")
			}
			var header struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(output[index], &header) != nil || header.ID != item.id || header.Type != item.kind {
				return false, fmt.Errorf("Responses final item identity mismatch")
			}
		}
		calls, err := s.orderedCalls()
		if err != nil {
			return false, err
		}
		message := agentsdk.ConversationStepMessage{Role: "assistant", Content: s.text.String(), ToolCalls: calls, ProviderState: v.Response.Output}
		if err = validateContinuation(message, ConversationProtocolResponses); err != nil {
			return false, err
		}
		s.continuation = append(json.RawMessage(nil), v.Response.Output...)
		s.model = v.Response.Model
		s.mergeUsage(v.Response.Usage)
		reason := "stop"
		if len(calls) > 0 {
			reason = "tool_calls"
		}
		if err = s.complete(reason); err != nil {
			return false, err
		}
		return true, nil
	}
	// Future non-content metadata can be ignored. Unknown execution-bearing
	// output types are rejected below so server-side tools cannot bypass us.
	if !strings.HasPrefix(v.Type, "response.output_") && !strings.HasPrefix(v.Type, "response.content_part.") && !strings.HasPrefix(v.Type, "response.function_call_arguments.") && !strings.HasPrefix(v.Type, "response.refusal.") {
		return false, nil
	}
	if v.OutputIndex == nil || *v.OutputIndex < 0 || *v.OutputIndex > 4096 {
		return false, fmt.Errorf("Responses output index missing")
	}
	index := *v.OutputIndex
	if v.Type == "response.output_item.added" {
		if _, found := s.responseItems[index]; found || index != len(s.responseItems) {
			return false, fmt.Errorf("duplicate or unordered Responses item")
		}
		var id, kind, role string
		_ = json.Unmarshal(v.Item["id"], &id)
		_ = json.Unmarshal(v.Item["type"], &kind)
		_ = json.Unmarshal(v.Item["role"], &role)
		if id == "" || len(id) > 256 {
			return false, fmt.Errorf("invalid Responses item ID")
		}
		for _, item := range s.responseItems {
			if item.id == id {
				return false, fmt.Errorf("duplicate Responses item ID")
			}
		}
		item := &executionResponseItem{id: id, kind: kind, callIndex: -1, parts: map[int]string{}, partKinds: map[int]string{}, closedParts: map[int]bool{}}
		switch kind {
		case "message":
			if role != "assistant" {
				return false, fmt.Errorf("invalid Responses message role")
			}
		case "reasoning":
		case "function_call":
			item.callIndex = len(s.calls)
			call, err := s.call(item.callIndex)
			if err != nil {
				return false, err
			}
			_ = json.Unmarshal(v.Item["call_id"], &call.call.ID)
			_ = json.Unmarshal(v.Item["name"], &call.call.Name)
			if err = s.startCall(item.callIndex); err != nil {
				return false, err
			}
			var initial string
			_ = json.Unmarshal(v.Item["arguments"], &initial)
			if initial != "" {
				if err = s.argumentDelta(item.callIndex, initial); err != nil {
					return false, err
				}
			}
		default:
			return false, fmt.Errorf("unsupported Responses output tool or item")
		}
		s.responseItems[index] = item
		return false, nil
	}
	item := s.responseItems[index]
	if item == nil || item.done || v.ItemID != "" && v.ItemID != item.id {
		return false, fmt.Errorf("Responses event without matching open item")
	}
	switch v.Type {
	case "response.function_call_arguments.delta":
		if item.kind != "function_call" || item.argumentsDone {
			return false, fmt.Errorf("unexpected Responses arguments")
		}
		return false, s.argumentDelta(item.callIndex, v.Delta)
	case "response.function_call_arguments.done":
		if item.kind != "function_call" || item.argumentsDone || s.calls[item.callIndex].call.Arguments != v.Arguments {
			return false, fmt.Errorf("Responses final arguments differ from deltas")
		}
		item.argumentsDone = true
	case "response.content_part.added":
		if item.kind != "message" || v.ContentIndex != len(item.parts) || (v.Part.Type != "output_text" && v.Part.Type != "refusal") {
			return false, fmt.Errorf("invalid Responses part")
		}
		text := v.Part.Text
		if v.Part.Type == "refusal" {
			text = v.Part.Refusal
		}
		if err := s.textDelta(text); err != nil {
			return false, err
		}
		item.parts[v.ContentIndex], item.partKinds[v.ContentIndex] = text, v.Part.Type
	case "response.output_text.delta", "response.refusal.delta":
		kind, ok := item.partKinds[v.ContentIndex]
		if item.kind != "message" || !ok || item.closedParts[v.ContentIndex] || (v.Type == "response.output_text.delta") != (kind == "output_text") {
			return false, fmt.Errorf("Responses text outside matching part")
		}
		if err := s.textDelta(v.Delta); err != nil {
			return false, err
		}
		item.parts[v.ContentIndex] += v.Delta
	case "response.output_text.done", "response.refusal.done":
		kind, ok := item.partKinds[v.ContentIndex]
		text := v.Text
		if v.Type == "response.refusal.done" {
			text = v.Refusal
		}
		if item.kind != "message" || !ok || item.parts[v.ContentIndex] != text || (v.Type == "response.output_text.done") != (kind == "output_text") {
			return false, fmt.Errorf("Responses final text differs from deltas")
		}
	case "response.content_part.done":
		kind, ok := item.partKinds[v.ContentIndex]
		text := v.Part.Text
		if v.Part.Type == "refusal" {
			text = v.Part.Refusal
		}
		if !ok || item.closedParts[v.ContentIndex] || kind != v.Part.Type || item.parts[v.ContentIndex] != text {
			return false, fmt.Errorf("Responses closed part differs from deltas")
		}
		item.closedParts[v.ContentIndex] = true
	case "response.output_item.done":
		var id, kind, status string
		_ = json.Unmarshal(v.Item["id"], &id)
		_ = json.Unmarshal(v.Item["type"], &kind)
		_ = json.Unmarshal(v.Item["status"], &status)
		if id != item.id || kind != item.kind || status != "" && status != "completed" {
			return false, fmt.Errorf("Responses item completion mismatch")
		}
		if item.kind == "function_call" {
			var call agentsdk.ConversationToolCall
			_ = json.Unmarshal(v.Item["call_id"], &call.ID)
			_ = json.Unmarshal(v.Item["name"], &call.Name)
			_ = json.Unmarshal(v.Item["arguments"], &call.Arguments)
			if !item.argumentsDone || call != s.calls[item.callIndex].call {
				return false, fmt.Errorf("Responses completed tool differs from deltas")
			}
		}
		if item.kind == "message" {
			if len(item.closedParts) != len(item.parts) {
				return false, fmt.Errorf("Responses message has open parts")
			}
			var text strings.Builder
			for i := 0; i < len(item.parts); i++ {
				text.WriteString(item.parts[i])
			}
			rawItem, _ := json.Marshal(v.Item)
			rawList := append([]byte{'['}, rawItem...)
			rawList = append(rawList, ']')
			if err := validateContinuation(agentsdk.ConversationStepMessage{Role: "assistant", Content: text.String(), ProviderState: rawList}, ConversationProtocolResponses); err != nil {
				return false, err
			}
		}
		item.done = true
	}
	return false, nil
}
