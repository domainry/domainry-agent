package agent

import (
	"encoding/json"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func conversationExecutionTool(calls []agentsdk.ConversationToolView, callID string) *agentsdk.ConversationToolView {
	for index := range calls {
		if calls[index].ID == callID {
			return &calls[index]
		}
		if child := conversationExecutionTool(calls[index].Subcalls, callID); child != nil {
			return child
		}
	}
	return nil
}

func interruptConversationTools(calls []agentsdk.ConversationToolView) {
	for index := range calls {
		call := &calls[index]
		switch call.Status {
		case "receiving", "queued", "waiting_user", "waiting_confirmation":
			call.Status = "not_started"
		case "running":
			call.Status = "interrupted"
		}
		interruptConversationTools(call.Subcalls)
	}
}

// The run projection and its event are written in the same transaction. A
// reconnect can start at LastEventSeq without losing in-progress tool text.
func projectConversationExecutionEvent(run *agentsdk.ConversationRun, kind string, data map[string]any) error {
	if kind == "model.attempt.started" || kind == "model.attempt.failed" || kind == "model.retry.scheduled" || kind == "model.attempt.completed" {
		var event struct {
			Step                   int            `json:"step"`
			Attempt                int            `json:"attempt"`
			ModelAttempt           int            `json:"model_attempt"`
			ErrorCode              string         `json:"error_code"`
			RetryAt                *time.Time     `json:"retry_at"`
			RetryDelayMilliseconds int64          `json:"retry_delay_ms"`
			Usage                  map[string]any `json:"usage"`
			StartedAt              time.Time      `json:"started_at"`
			CompletedAt            *time.Time     `json:"completed_at"`
		}
		raw, err := json.Marshal(data)
		if err != nil || json.Unmarshal(raw, &event) != nil || !validConversationModelAttemptStep(event.Step) || event.Attempt != run.Attempt || event.ModelAttempt < 1 {
			return conversationError("bad_request", "model_attempt_event_invalid")
		}
		index := conversationModelAttemptIndex(*run, event.Step, event.Attempt)
		if kind == "model.attempt.started" {
			if event.StartedAt.IsZero() || index >= 0 && run.ModelAttempts[index].Number >= event.ModelAttempt {
				return conversationError("conflict", "model_attempt_event_order")
			}
			run.ModelAttempts = append(run.ModelAttempts, agentsdk.ConversationModelAttempt{Step: event.Step, RunAttempt: event.Attempt, Number: event.ModelAttempt, Status: "started", StartedAt: event.StartedAt})
			if event.Step == -1 {
				run.DraftText, run.DraftBytes = "", 0
			} else if event.Step >= 0 {
				if step := conversationRunStep(run, event.Step); step != nil {
					step.Attempt, step.ModelAttempts, step.Status = event.Attempt, event.ModelAttempt, "generating"
					step.Text, step.Calls, step.ToolExecution, step.ParallelToolCalls = "", []agentsdk.ConversationToolView{}, "", 0
					step.RetryAt, step.RetryDelayMilliseconds, step.LastModelError = nil, 0, ""
				} else {
					return conversationError("conflict", "step_event_order")
				}
			}
			return nil
		}
		if index < 0 || run.ModelAttempts[index].Number != event.ModelAttempt {
			return conversationError("conflict", "model_attempt_event_order")
		}
		attempt := &run.ModelAttempts[index]
		switch kind {
		case "model.attempt.failed":
			if attempt.Status != "started" || event.CompletedAt == nil {
				return conversationError("conflict", "model_attempt_event_order")
			}
			attempt.Status, attempt.ErrorCode, attempt.Usage, attempt.CompletedAt = "failed", event.ErrorCode, event.Usage, event.CompletedAt
			if event.Step == -1 {
				run.DraftText, run.DraftBytes = "", 0
			} else if step := conversationRunStep(run, event.Step); step != nil {
				step.Text, step.Calls, step.Status, step.LastModelError = "", []agentsdk.ConversationToolView{}, "failed", event.ErrorCode
			}
		case "model.retry.scheduled":
			if attempt.Status != "failed" || event.RetryAt == nil || event.RetryDelayMilliseconds < 0 {
				return conversationError("conflict", "model_attempt_event_order")
			}
			attempt.Status, attempt.RetryAt, attempt.RetryDelayMilliseconds = "retry_scheduled", event.RetryAt, event.RetryDelayMilliseconds
			if event.Step >= 0 {
				if step := conversationRunStep(run, event.Step); step != nil {
					step.Status, step.RetryAt, step.RetryDelayMilliseconds = "retry_wait", event.RetryAt, event.RetryDelayMilliseconds
				}
			}
		case "model.attempt.completed":
			if attempt.Status != "started" || event.CompletedAt == nil {
				return conversationError("conflict", "model_attempt_event_order")
			}
			attempt.Status, attempt.Usage, attempt.CompletedAt = "completed", event.Usage, event.CompletedAt
		}
		return nil
	}
	if kind == "context.assembled" {
		var event struct {
			Attempt int                               `json:"attempt"`
			Context *agentsdk.ConversationContextView `json:"context"`
		}
		raw, err := json.Marshal(data)
		if err != nil || json.Unmarshal(raw, &event) != nil || event.Attempt != run.Attempt || event.Context == nil {
			return conversationError("bad_request", "context_event_invalid")
		}
		run.Context = event.Context
		return nil
	}
	if kind == "run.cancelled" || kind == "run.failed" {
		for i := range run.Steps {
			step := &run.Steps[i]
			if step.Status == "generating" || step.Status == "failed" && step.LastModelError != "" {
				step.Status = "interrupted"
			}
			interruptConversationTools(step.Calls)
		}
		return nil
	}
	if !strings.HasPrefix(kind, "step.") && !strings.HasPrefix(kind, "tool.") {
		return nil
	}
	var event struct {
		Reference       *agentsdk.ConversationResultReference `json:"reference"`
		ActorID         string                                `json:"actor_id"`
		CheckedAt       time.Time                             `json:"checked_at"`
		Effect          string                                `json:"effect"`
		Completion      string                                `json:"completion"`
		Step            int                                   `json:"step"`
		Attempt         int                                   `json:"attempt"`
		Index           int                                   `json:"index"`
		Offset          int                                   `json:"offset"`
		CallID          string                                `json:"call_id"`
		ParentCallID    string                                `json:"parent_call_id"`
		DispatchIndex   int                                   `json:"dispatch_index"`
		Name            string                                `json:"name"`
		Tool            string                                `json:"tool"`
		Arguments       string                                `json:"arguments"`
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
		ParallelWidth   int                                   `json:"parallel_width"`
		ParallelCalls   int                                   `json:"parallel_calls"`
		Usage           map[string]any                        `json:"usage"`
		Context         *agentsdk.ConversationContextView     `json:"context"`
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
		run.Steps = append(run.Steps, agentsdk.ConversationStepView{Number: event.Step, Attempt: event.Attempt, Status: "generating", Calls: []agentsdk.ConversationToolView{}, Context: event.Context})
		return nil
	}
	step := &run.Steps[index]
	switch kind {
	case "tool.receipt.reused":
		if call := conversationExecutionTool(step.Calls, event.CallID); call != nil {
			call.ReusedFrom = event.Reference
			return nil
		}
		return conversationError("bad_request", "step_event_invalid")
	case "step.started":
		return nil
	case "step.attempt.started":
		step.Attempt = event.Attempt
		step.Text = ""
		step.Calls = []agentsdk.ConversationToolView{}
		step.ToolExecution = ""
		step.ParallelToolCalls = 0
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
		step.Usage = event.Usage
		step.ToolExecution = ""
		step.ParallelToolCalls = 0
		step.Status = "completed"
		if event.Finish == "tool_calls" {
			step.Status = "tools"
		}
		step.Calls = []agentsdk.ConversationToolView{}
		if step.Context != nil {
			step.Context.CacheReadInputTokens, step.Context.CacheCreationInputTokens = conversationCacheUsage(event.Usage)
		}
		for _, call := range event.Calls {
			step.Calls = append(step.Calls, agentsdk.ConversationToolView{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: "queued"})
		}
		if attemptIndex := conversationModelAttemptIndex(*run, event.Step, event.Attempt); attemptIndex >= 0 {
			attempt := &run.ModelAttempts[attemptIndex]
			if attempt.Status == "started" {
				completed := time.Now().UTC()
				attempt.Status, attempt.Usage, attempt.CompletedAt = "completed", event.Usage, &completed
			}
		}
		if event.ParallelWidth > 1 && event.ParallelCalls > 1 {
			step.ToolExecution, step.ParallelToolCalls = "parallel_read", event.ParallelCalls
		}
	case "tool.inspection.started", "tool.inspection.completed":
		if call := conversationExecutionTool(step.Calls, event.CallID); call != nil {
			call.OutcomeInspection = &agentsdk.ConversationOutcomeInspectionView{Status: event.Status, ActorID: event.ActorID, CheckedAt: event.CheckedAt}
			return nil
		}
		return conversationError("conflict", "step_call_missing")
	case "tool.queued", "tool.started", "tool.completed", "tool.uncertain", "tool.waiting":
		call := conversationExecutionTool(step.Calls, event.CallID)
		if call == nil && event.ParentCallID != "" {
			parent := conversationExecutionTool(step.Calls, event.ParentCallID)
			if parent == nil || event.DispatchIndex != len(parent.Subcalls) || !executionText(event.CallID, 256, true) || !executionText(event.Tool, 64, true) {
				return conversationError("conflict", "step_call_missing")
			}
			parent.Subcalls = append(parent.Subcalls, agentsdk.ConversationToolView{ParentCallID: event.ParentCallID, DispatchIndex: event.DispatchIndex, ID: event.CallID, Name: event.Tool, Arguments: event.Arguments, Status: "queued", Effect: event.Effect})
			call = &parent.Subcalls[len(parent.Subcalls)-1]
		}
		if call == nil {
			return conversationError("conflict", "step_call_missing")
		}
		if kind == "tool.queued" {
			return nil
		}
		call.Status = "running"
		if event.Status != "" {
			call.Status = event.Status
		}
		if event.Effect != "" {
			call.Effect = event.Effect
		}
		call.Completion = event.Completion
		call.ErrorCode = event.ErrorCode
		call.ResourceID = event.ResourceID
		call.ResultPreview = event.ResultPreview
		call.ResultTruncated = event.ResultTruncated
		call.ResultReference = event.ResultReference
		call.Citations = event.Citations
	default:
		return conversationError("bad_request", "step_event_invalid")
	}
	return nil
}
