package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationCodeInput struct {
	Language string `json:"language"`
	Source   string `json:"source"`
}

func conversationCodeCallID(runID, parentCallID string, index int) string {
	digest := conversationDigest([]any{"code-dispatch-v1", runID, parentCallID, index})
	return "code_" + digest[:48]
}

func (s *ConversationService) executeConversationCode(ctx context.Context, claim persistence.ConversationClaim, step persistence.ConversationExecutionStep, parent agentsdk.ConversationToolCall) (agentsdk.ConversationToolResult, error) {
	if s.options.CodeRuntime == nil {
		return agentsdk.ConversationToolResult{}, conversationFailure("unavailable", "code_runtime_unavailable")
	}
	var input conversationCodeInput
	if json.Unmarshal([]byte(parent.Arguments), &input) != nil || input.Language != agentsdk.ConversationCodeLanguageLua || strings.TrimSpace(input.Source) == "" {
		return personalToolFailure("code_input_invalid"), nil
	}
	definitions := make([]agentsdk.ConversationToolDefinition, 0, len(step.Input.Tools))
	byName := make(map[string]agentsdk.ConversationToolDefinition, len(step.Input.Tools))
	for _, definition := range step.Input.Tools {
		if definition.Key == agentsdk.ConversationCodeToolKey {
			continue
		}
		definitions = append(definitions, definition)
		byName[definition.Key] = definition
	}
	expectedIndex := 0
	repository := s.repo.(persistence.ConversationCodeExecutionRepository)
	dispatch := func(dispatchCtx context.Context, dispatched agentsdk.ConversationCodeDispatch) (agentsdk.ConversationToolResult, error) {
		if err := dispatchCtx.Err(); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if dispatched.Index != expectedIndex || dispatched.Index < 0 || dispatched.Index >= step.Input.MaxToolCalls || len(dispatched.Arguments) > step.Input.MaxArgumentBytes || !json.Valid(dispatched.Arguments) {
			return agentsdk.ConversationToolResult{}, &agentsdk.ConversationCodeFailure{Code: "code_dispatch_invalid"}
		}
		definition, exists := byName[dispatched.Name]
		if !exists || dispatched.Name == agentsdk.ConversationCodeToolKey {
			return agentsdk.ConversationToolResult{}, &agentsdk.ConversationCodeFailure{Code: "code_tool_unavailable"}
		}
		call := agentsdk.ConversationToolCall{ID: conversationCodeCallID(claim.Run.ID, parent.ID, dispatched.Index), Name: dispatched.Name, Arguments: string(dispatched.Arguments)}
		if _, _, err := repository.PrepareExecutionSubtool(dispatchCtx, claim, step.Number, parent.ID, dispatched.Index, call, definition, step.Input.MaxToolCalls); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		expectedIndex++
		return s.executeConversationToolWithParent(dispatchCtx, claim, step, call, parent.ID, dispatched.Index)
	}
	codeResult, err := s.options.CodeRuntime.ExecuteConversationCode(ctx, agentsdk.ConversationCodeExecution{
		ProtocolVersion: agentsdk.ConversationCodeProtocolVersion,
		Language:        input.Language, Source: input.Source, Tools: definitions,
		MaxDispatches: step.Input.MaxToolCalls, MaxOutputBytes: 48 * 1024, MaxLogBytes: 8 * 1024,
	}, dispatch)
	if err != nil {
		if ctx.Err() != nil {
			return agentsdk.ConversationToolResult{}, ctx.Err()
		}
		var failure *agentsdk.ConversationCodeFailure
		if errors.As(err, &failure) {
			code := failure.Code
			if !strings.HasPrefix(code, "code_") {
				code = "code_runtime_failed"
			}
			return personalToolFailure(code), nil
		}
		// Dispatcher errors include durable waits for user confirmation and
		// reconciliation. Preserve the exact error so the outer receipt stays
		// started and the existing run state can resume deterministically.
		return agentsdk.ConversationToolResult{}, err
	}
	if codeResult.Language != input.Language || codeResult.Dispatches != expectedIndex || len(codeResult.Value) == 0 || !json.Valid(codeResult.Value) {
		return personalToolFailure("code_result_invalid"), nil
	}
	content, err := json.Marshal(codeResult)
	logBytes := 0
	for _, line := range codeResult.Logs {
		logBytes += len(line)
		if len(line) > 2048 {
			return personalToolFailure("code_result_invalid"), nil
		}
	}
	if err != nil || len(content) > agentsdk.ConversationCodeTool().MaxOutputBytes || len(codeResult.Logs) > 128 || logBytes > 8*1024 || codeResult.Dispatches > step.Input.MaxToolCalls {
		return personalToolFailure("code_result_invalid"), nil
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: content}, nil
}

func (s *ConversationService) reauthorizeConversationCodeResults(ctx context.Context, claim persistence.ConversationClaim, step int, parentCallID string, parentResult agentsdk.ConversationToolResult, current map[string]conversationCompiledTool) error {
	if parentResult.Status != "completed" {
		return nil
	}
	var recorded agentsdk.ConversationCodeResult
	if json.Unmarshal(parentResult.Content, &recorded) != nil || recorded.Dispatches < 0 {
		return conversationFailure("conflict", "tool_result_invalid")
	}
	children, err := s.repo.(persistence.ConversationCodeExecutionRepository).ExecutionSubtools(ctx, claim, step, parentCallID)
	if err != nil {
		return err
	}
	if len(children) != recorded.Dispatches {
		return conversationFailure("conflict", "tool_result_invalid")
	}
	seen := map[string]bool{}
	for index, child := range children {
		if child.ParentCallID != parentCallID || child.DispatchIndex != index || child.State != "completed" || child.Result == nil {
			return conversationFailure("conflict", "tool_result_invalid")
		}
		if err = s.authorizeConversationRecord(ctx, claim.Run.ConversationID, claim.Run.ID, child, claim.Authority, current, seen, claim.Run.ConversationID); err != nil {
			return err
		}
	}
	return nil
}
