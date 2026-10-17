package agent

import (
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
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
	if kind == "run.planning.started" || kind == "run.planning.completed" || kind == "run.planning.failed" {
		var event struct {
			Attempt   int       `json:"attempt"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		raw, err := marshalDurableJSON(data)
		if err != nil || unmarshalDurableJSON(raw, &event) != nil || event.Attempt != run.Attempt || event.UpdatedAt.IsZero() {
			return conversationError("bad_request", "run_planning_event_invalid")
		}
		return nil
	}
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
		raw, err := marshalDurableJSON(data)
		if err != nil || unmarshalDurableJSON(raw, &event) != nil || !validConversationModelAttemptStep(event.Step) || event.Attempt != run.Attempt || event.ModelAttempt < 1 {
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
				run.DraftMessageID, run.DraftMessageSeq = "", 0
			} else if event.Step >= 0 {
				if step := conversationRunStep(run, event.Step); step != nil {
					step.Attempt, step.ModelAttempts, step.Status = event.Attempt, event.ModelAttempt, "generating"
					step.Text, step.Calls, step.ToolExecution, step.ParallelToolCalls = "", []agentsdk.ConversationToolView{}, "", 0
					step.MessageID, step.MessageSeq = "", 0
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
		raw, err := marshalDurableJSON(data)
		if err != nil || unmarshalDurableJSON(raw, &event) != nil || event.Attempt != run.Attempt || event.Context == nil {
			return conversationError("bad_request", "context_event_invalid")
		}
		run.Context = event.Context
		return nil
	}
	if kind == "run.plan.created" {
		var event struct {
			Attempt   int                                `json:"attempt"`
			Key       string                             `json:"key"`
			Label     string                             `json:"label"`
			Stages    []agentsdk.ConversationRunPlanStep `json:"stages"`
			UpdatedAt time.Time                          `json:"updated_at"`
		}
		raw, err := marshalDurableJSON(data)
		if err != nil || unmarshalDurableJSON(raw, &event) != nil || event.Attempt != run.Attempt || !executionText(event.Key, 96, true) || !executionText(event.Label, 160, true) || len(event.Stages) < 1 || len(event.Stages) > 32 || event.UpdatedAt.IsZero() {
			return conversationError("bad_request", "run_plan_invalid")
		}
		seen := map[string]bool{}
		for _, stage := range event.Stages {
			validBinding := stage.Binding.Kind == "model" && len(stage.Binding.Tools) == 0 && stage.Binding.ProgressKey == "" ||
				stage.Binding.Kind == "tool_progress" && len(stage.Binding.Tools) == 1 && executionText(stage.Binding.ProgressKey, 64, true) ||
				stage.Binding.Kind == "tool" && len(stage.Binding.Tools) > 0 && len(stage.Binding.Tools) <= 16 && stage.Binding.ProgressKey == ""
			if !executionText(stage.Key, 96, true) || seen[stage.Key] || !executionText(stage.Label, 160, true) || !executionText(stage.Detail, 512, false) || !validBinding || stage.CallID != "" {
				return conversationError("bad_request", "run_plan_invalid")
			}
			for _, tool := range stage.Binding.Tools {
				if !executionText(tool, 64, true) {
					return conversationError("bad_request", "run_plan_invalid")
				}
			}
			seen[stage.Key] = true
		}
		if run.Plan != nil {
			if conversationHash(run.Plan) != conversationHash(agentsdk.ConversationRunPlan{Key: event.Key, Label: event.Label, Steps: event.Stages, CreatedAt: event.UpdatedAt, UpdatedAt: event.UpdatedAt}) {
				return conversationError("conflict", "run_plan_changed")
			}
			return nil
		}
		plan := &agentsdk.ConversationRunPlan{Key: event.Key, Label: event.Label, CreatedAt: event.UpdatedAt, UpdatedAt: event.UpdatedAt}
		for index, stage := range event.Stages {
			stage.Index, stage.Total, stage.Status, stage.UpdatedAt = index+1, len(event.Stages), "planned", event.UpdatedAt
			plan.Steps = append(plan.Steps, stage)
		}
		run.Plan = plan
		return nil
	}
	if kind == "run.progress" {
		var event struct {
			Attempt     int       `json:"attempt"`
			ProgressKey string    `json:"progress_key"`
			CallID      string    `json:"call_id"`
			Status      string    `json:"status"`
			Detail      string    `json:"detail"`
			UpdatedAt   time.Time `json:"updated_at"`
		}
		raw, err := marshalDurableJSON(data)
		if err != nil || unmarshalDurableJSON(raw, &event) != nil {
			return conversationError("bad_request", "run_progress_invalid")
		}
		validStatus := event.Status == "started" || event.Status == "waiting_user" || event.Status == "waiting_confirmation" || event.Status == "completed" || event.Status == "skipped" || event.Status == "failed"
		if event.Attempt != run.Attempt || run.Plan == nil || !executionText(event.ProgressKey, 96, true) || !executionText(event.CallID, 128, false) || !executionText(event.Detail, 512, false) || !validStatus || event.UpdatedAt.IsZero() {
			return conversationError("bad_request", "run_progress_invalid")
		}
		for index := range run.Plan.Steps {
			step := &run.Plan.Steps[index]
			if step.Key != event.ProgressKey {
				continue
			}
			if step.Binding.Kind == "model" {
				if event.CallID != "" {
					return conversationError("conflict", "run_progress_call_invalid")
				}
			} else if event.Status == "skipped" {
				if event.CallID != "" || step.CallID != "" {
					return conversationError("conflict", "run_progress_call_invalid")
				}
			} else if event.CallID == "" || step.CallID != "" && step.CallID != event.CallID {
				return conversationError("conflict", "run_progress_call_invalid")
			}
			if step.Status == event.Status {
				if step.Binding.Kind != "model" && step.CallID == "" {
					step.CallID = event.CallID
				}
				if event.Detail != "" {
					step.Detail = event.Detail
				}
				step.UpdatedAt, run.Plan.UpdatedAt = event.UpdatedAt, event.UpdatedAt
				return nil
			}
			if step.Status == "completed" || step.Status == "skipped" || step.Status == "failed" {
				return nil
			}
			switch event.Status {
			case "started":
				if step.Status != "planned" && step.Status != "waiting_user" && step.Status != "waiting_confirmation" {
					return conversationError("conflict", "run_progress_order")
				}
				for previous := 0; previous < index; previous++ {
					if run.Plan.Steps[previous].Status != "completed" && run.Plan.Steps[previous].Status != "skipped" {
						return conversationError("conflict", "run_progress_order")
					}
				}
			case "waiting_user", "waiting_confirmation":
				if step.Status != "started" {
					return conversationError("conflict", "run_progress_order")
				}
			case "completed", "failed":
				if step.Status != "started" && step.Status != "waiting_user" && step.Status != "waiting_confirmation" {
					return conversationError("conflict", "run_progress_order")
				}
			case "skipped":
				if step.Status != "planned" {
					return conversationError("conflict", "run_progress_order")
				}
			}
			step.Status, step.UpdatedAt = event.Status, event.UpdatedAt
			if event.Detail != "" {
				step.Detail = event.Detail
			}
			if step.Binding.Kind != "model" && step.CallID == "" {
				step.CallID = event.CallID
			}
			run.Plan.UpdatedAt = event.UpdatedAt
			return nil
		}
		return conversationError("conflict", "run_progress_not_planned")
	}
	if kind == "run.cancelled" || kind == "run.failed" {
		if run.Plan != nil {
			for index := range run.Plan.Steps {
				step := &run.Plan.Steps[index]
				if step.Status == agentsdk.ConversationRunProgressCompleted || step.Status == agentsdk.ConversationRunProgressSkipped || step.Status == agentsdk.ConversationRunProgressFailed {
					continue
				}
				// Cancellation is an intentional terminal outcome (including a
				// user's rejection), so no remaining plan step should look failed.
				// A failed run marks only work that had actually started as failed;
				// untouched future work is skipped.
				wasPlanned := step.Status == agentsdk.ConversationRunProgressPlanned
				step.Status = agentsdk.ConversationRunProgressSkipped
				if kind == "run.failed" && !wasPlanned {
					step.Status = agentsdk.ConversationRunProgressFailed
				}
			}
		}
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
		ProgressKey     string                                `json:"progress_key"`
		Detail          string                                `json:"detail"`
		UpdatedAt       time.Time                             `json:"updated_at"`
		Stages          []toolsdk.ProgressStep                `json:"stages"`
		Calls           []agentsdk.ConversationToolCall       `json:"calls"`
		ParallelWidth   int                                   `json:"parallel_width"`
		ParallelCalls   int                                   `json:"parallel_calls"`
		Usage           map[string]any                        `json:"usage"`
		Context         *agentsdk.ConversationContextView     `json:"context"`
	}
	raw, err := marshalDurableJSON(data)
	if err != nil || unmarshalDurableJSON(raw, &event) != nil || event.Step < 0 || event.Step >= 256 || event.Attempt != run.Attempt {
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
		step.MessageID, step.MessageSeq = "", 0
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
	case "tool.plan.created":
		call := conversationExecutionTool(step.Calls, event.CallID)
		if call == nil {
			return conversationError("conflict", "step_call_missing")
		}
		if len(event.Stages) < 1 || len(event.Stages) > 32 || event.UpdatedAt.IsZero() {
			return conversationError("bad_request", "tool_plan_invalid")
		}
		seen := map[string]bool{}
		for _, stage := range event.Stages {
			if !executionText(stage.Key, 64, true) || !executionText(stage.Label, 160, true) || !executionText(stage.Detail, 512, false) || seen[stage.Key] {
				return conversationError("bad_request", "tool_plan_invalid")
			}
			seen[stage.Key] = true
		}
		if len(call.Progress) > 0 {
			if len(call.Progress) != len(event.Stages) {
				return conversationError("conflict", "tool_plan_changed")
			}
			for index, stage := range event.Stages {
				if call.Progress[index].Key != stage.Key || call.Progress[index].Label != stage.Label || call.Progress[index].Detail != stage.Detail || call.Progress[index].Optional != stage.Optional || call.Progress[index].Index != index+1 || call.Progress[index].Total != len(event.Stages) {
					return conversationError("conflict", "tool_plan_changed")
				}
			}
			return nil
		}
		for index, stage := range event.Stages {
			call.Progress = append(call.Progress, agentsdk.ConversationToolProgressView{Key: stage.Key, Label: stage.Label, Detail: stage.Detail, Optional: stage.Optional, Index: index + 1, Total: len(event.Stages), Status: toolsdk.ProgressStatusPlanned, UpdatedAt: event.UpdatedAt})
		}
		return nil
	case "tool.progress":
		call := conversationExecutionTool(step.Calls, event.CallID)
		if call == nil {
			return conversationError("conflict", "step_call_missing")
		}
		if !executionText(event.ProgressKey, 64, true) || !executionText(event.Detail, 512, false) || event.UpdatedAt.IsZero() || event.Status != toolsdk.ProgressStatusStarted && event.Status != toolsdk.ProgressStatusCompleted && event.Status != toolsdk.ProgressStatusSkipped && event.Status != toolsdk.ProgressStatusFailed {
			return conversationError("bad_request", "tool_progress_invalid")
		}
		for index := range call.Progress {
			progress := &call.Progress[index]
			if progress.Key != event.ProgressKey {
				continue
			}
			if progress.Status == event.Status {
				progress.Detail, progress.UpdatedAt = event.Detail, event.UpdatedAt
				return nil
			}
			if (progress.Status == toolsdk.ProgressStatusCompleted || progress.Status == toolsdk.ProgressStatusSkipped || progress.Status == toolsdk.ProgressStatusFailed) && event.Status == toolsdk.ProgressStatusStarted {
				return nil
			}
			if event.Status == toolsdk.ProgressStatusStarted {
				if progress.Status != "planned" {
					return conversationError("conflict", "tool_progress_order")
				}
				for previous := 0; previous < index; previous++ {
					if call.Progress[previous].Status != toolsdk.ProgressStatusCompleted && call.Progress[previous].Status != toolsdk.ProgressStatusSkipped {
						return conversationError("conflict", "tool_progress_order")
					}
				}
			} else if event.Status == toolsdk.ProgressStatusSkipped {
				if !progress.Optional || progress.Status != "planned" {
					return conversationError("conflict", "tool_progress_order")
				}
			} else if progress.Status != toolsdk.ProgressStatusStarted {
				return conversationError("conflict", "tool_progress_order")
			}
			progress.Detail, progress.Status, progress.UpdatedAt = event.Detail, event.Status, event.UpdatedAt
			return nil
		}
		return conversationError("conflict", "tool_progress_not_planned")
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
