package agent

import (
	"encoding/json"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// The run projection and its event are written in the same transaction. A
// reconnect can start at LastEventSeq without losing in-progress tool text.
func projectConversationExecutionEvent(run *agentsdk.ConversationRun, kind string, data map[string]any) error {
	if !strings.HasPrefix(kind, "step.") && !strings.HasPrefix(kind, "tool.") {
		return nil
	}
	var event struct {
		Step            int                                   `json:"step"`
		Attempt         int                                   `json:"attempt"`
		Index           int                                   `json:"index"`
		Offset          int                                   `json:"offset"`
		CallID          string                                `json:"call_id"`
		Name            string                                `json:"name"`
		Tool            string                                `json:"tool"`
		Delta           string                                `json:"delta"`
		Text            string                                `json:"text"`
		Finish          string                                `json:"finish_reason"`
		Status          string                                `json:"status"`
		ErrorCode       string                                `json:"error_code"`
		ResourceID      string                                `json:"resource_id"`
		ResultPreview   string                                `json:"result_preview"`
		ResultTruncated bool                                  `json:"result_truncated"`
		ResultReference *agentsdk.ConversationResultReference `json:"result_reference"`
		Citations       []agentsdk.ConversationCitation       `json:"citations"`
		Calls           []agentsdk.ConversationToolCall       `json:"calls"`
	}
	raw, err := json.Marshal(data)
	if err != nil || json.Unmarshal(raw, &event) != nil || event.Step < 0 || event.Step >= 256 || event.Attempt != run.Attempt {
		return conversationError("bad_request", "step_event_invalid")
	}
	index := -1
	for i := range run.Steps {
		if run.Steps[i].Number == event.Step {
			index = i
			break
		}
	}
	if index < 0 {
		if kind != "step.started" {
			return conversationError("conflict", "step_event_order")
		}
		run.Steps = append(run.Steps, agentsdk.ConversationStepView{Number: event.Step, Attempt: event.Attempt, Status: "generating", Calls: []agentsdk.ConversationToolView{}})
		return nil
	}
	step := &run.Steps[index]
	switch kind {
	case "step.started":
		return nil
	case "step.attempt.started":
		step.Attempt = event.Attempt
		step.Text = ""
		step.Calls = []agentsdk.ConversationToolView{}
		step.Status = "generating"
	case "step.text.delta":
		if step.Attempt != event.Attempt || step.Status != "generating" || event.Offset != len(step.Text) || !executionText(event.Delta, 1024*1024-len(step.Text), false) {
			return conversationError("bad_request", "step_delta_invalid")
		}
		step.Text += event.Delta
	case "step.tool.started":
		if step.Attempt != event.Attempt || step.Status != "generating" || event.Index != len(step.Calls) || len(step.Calls) >= 64 || !executionText(event.CallID, 256, true) || !executionText(event.Name, 64, true) {
			return conversationError("bad_request", "step_call_invalid")
		}
		for _, call := range step.Calls {
			if call.ID == event.CallID {
				return conversationError("bad_request", "step_call_invalid")
			}
		}
		step.Calls = append(step.Calls, agentsdk.ConversationToolView{ID: event.CallID, Name: event.Name, Status: "receiving"})
	case "step.tool.arguments.delta":
		if step.Attempt != event.Attempt || step.Status != "generating" || event.Index < 0 || event.Index >= len(step.Calls) {
			return conversationError("bad_request", "step_argument_invalid")
		}
		call := &step.Calls[event.Index]
		if call.ID != event.CallID || call.Name != event.Name || event.Offset != len(call.Arguments) || !executionText(event.Delta, 1024*1024-len(call.Arguments), false) {
			return conversationError("bad_request", "step_argument_invalid")
		}
		call.Arguments += event.Delta
	case "step.completed":
		step.Text = event.Text
		step.Status = "completed"
		if event.Finish == "tool_calls" {
			step.Status = "tools"
		}
		step.Calls = []agentsdk.ConversationToolView{}
		for _, call := range event.Calls {
			step.Calls = append(step.Calls, agentsdk.ConversationToolView{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: "queued"})
		}
	case "tool.started", "tool.completed", "tool.uncertain", "tool.waiting":
		found := false
		for i := range step.Calls {
			call := &step.Calls[i]
			if call.ID == event.CallID {
				found = true
				call.Status = "running"
				if event.Status != "" {
					call.Status = event.Status
				}
				call.ErrorCode = event.ErrorCode
				call.ResourceID = event.ResourceID
				call.ResultPreview = event.ResultPreview
				call.ResultTruncated = event.ResultTruncated
				call.ResultReference = event.ResultReference
				call.Citations = event.Citations
				break
			}
		}
		if !found {
			return conversationError("conflict", "step_call_missing")
		}
	default:
		return conversationError("bad_request", "step_event_invalid")
	}
	return nil
}
