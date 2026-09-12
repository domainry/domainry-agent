package application

import (
	"context"
	"encoding/json"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) streamConversationExecutionStep(ctx context.Context, claim persistence.ConversationClaim, step persistence.ConversationExecutionStep) (agentsdk.ConversationStepResult, error) {
	if err := s.authorizeConversationClaim(ctx, claim, "model"); err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	raw, err := json.Marshal(step.Input)
	if err != nil || len(raw) > s.options.ContextBytes {
		return agentsdk.ConversationStepResult{}, conversationFailure("rate_limited", "execution_context_exceeded")
	}
	streamCtx, cancel := s.externalCallContext(ctx, 0)
	defer cancel()
	text := ""
	arguments := 0
	calls := []agentsdk.ConversationToolCall{}
	var callbackErr error
	known := map[string]bool{}
	for _, definition := range step.Input.Tools {
		known[definition.Key] = true
	}
	maxText := min(step.Input.MaxOutputBytes, s.options.MaxOutputBytes)
	maxArguments := min(step.Input.MaxArgumentBytes, s.options.MaxArgumentBytes)
	maxCalls := min(step.Input.MaxToolCalls, s.options.MaxToolCalls)
	result, err := s.model.(agentsdk.ConversationAgentModel).StreamConversationStep(streamCtx, step.Input, func(event agentsdk.ConversationModelEvent) error {
		if callbackErr != nil {
			return callbackErr
		}
		if err := streamCtx.Err(); err != nil {
			return err
		}
		valid := false
		switch event.Type {
		case "text.delta":
			valid = event.Offset == len(text) && conversationText(event.Delta, maxText-len(text), false) && event.Name == "" && event.CallID == ""
			if valid {
				text += event.Delta
			}
		case "tool.started":
			valid = event.Index == len(calls) && len(calls) < maxCalls && known[event.Name] && conversationText(event.CallID, 256, true) && event.Offset == 0 && event.Delta == ""
			for _, call := range calls {
				if call.ID == event.CallID {
					valid = false
				}
			}
			if valid {
				calls = append(calls, agentsdk.ConversationToolCall{ID: event.CallID, Name: event.Name})
			}
		case "tool.arguments.delta":
			if event.Index >= 0 && event.Index < len(calls) {
				call := &calls[event.Index]
				valid = event.CallID == call.ID && event.Name == call.Name && event.Offset == len(call.Arguments) && conversationText(event.Delta, maxArguments-arguments, false)
				if valid {
					call.Arguments += event.Delta
					arguments += len(event.Delta)
				}
			}
		}
		if !valid {
			callbackErr = fmt.Errorf("invalid execution preview event")
		} else if event.Type == "tool.started" || event.Delta != "" {
			callbackErr = s.repo.AppendEvent(streamCtx, claim, "step."+event.Type, map[string]any{"step": step.Number, "attempt": claim.Run.Attempt, "index": event.Index, "call_id": event.CallID, "name": event.Name, "offset": event.Offset, "delta": event.Delta})
		}
		if callbackErr != nil {
			cancel()
		}
		return callbackErr
	})
	if callbackErr != nil {
		return agentsdk.ConversationStepResult{}, callbackErr
	}
	if err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	if !conversationText(result.Message.Content, maxText, false) || text != "" && text != result.Message.Content || len(result.Message.ToolCalls) > maxCalls {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("model result differs from bounded stream")
	}
	size := 0
	for _, call := range result.Message.ToolCalls {
		if call.Name == "ask_user" && len(result.Message.ToolCalls) != 1 {
			return agentsdk.ConversationStepResult{}, conversationFailure("bad_request", "question_must_be_separate")
		}
		size += len(call.Arguments)
	}
	if size > maxArguments {
		return agentsdk.ConversationStepResult{}, fmt.Errorf("model result arguments exceeded")
	}
	if len(calls) > 0 {
		if len(calls) != len(result.Message.ToolCalls) {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("model tool preview differs from result")
		}
		for i, call := range calls {
			if call != result.Message.ToolCalls[i] {
				return agentsdk.ConversationStepResult{}, fmt.Errorf("model tool preview differs from result")
			}
		}
	}
	return result, nil
}
