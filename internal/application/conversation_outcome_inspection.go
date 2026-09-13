package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func inspectOutcome(ctx context.Context, host sdk.ConversationToolHost, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if reader, ok := host.(sdk.ConversationOutcomeInspector); ok {
		return reader.InspectConversationToolOutcome(ctx, in)
	}
	return sdk.ConversationToolResult{}, conversationFailure("unavailable", "outcome_inspection_unavailable")
}
func (h *profileToolHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if !conversationProfileAllows(ctx, in.Definition.Key, h.allowed) {
		return sdk.ConversationToolResult{}, conversationFailure("forbidden", "tool_access_denied")
	}
	return inspectOutcome(ctx, h.base, in)
}
func (h *collaborationToolHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.ConversationToolHost, in)
}
func (h *assemblyConfirmationHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.ConversationToolHost, in)
}
func (h *extensionInteractionHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.ConversationToolHost, in)
}
func (h *knowledgeConversationHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.base, in)
}
func (h *attachmentKnowledgeHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.base, in)
}
func (h *businessConversationHost) InspectConversationToolOutcome(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return inspectOutcome(ctx, h.base, in)
}

func (s *ConversationService) inspectDelegationOutcome(ctx context.Context, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	if in.Inspection == nil || in.Inspection.Step < 0 || in.Inspection.Step >= 256 {
		return out, conversationFailure("bad_request", "outcome_inspection_invalid")
	}
	if s.options.ToolHost == nil {
		return out, conversationFailure("unavailable", "outcome_inspection_unavailable")
	}
	if err := s.authorizeConversationExecution(ctx, d.ConversationID, in.Inspection.RunID, "inspect_outcome", a); err != nil {
		return out, err
	}
	if err := s.checkRunSources(ctx, sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: in.Inspection.RunID}, a); err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationOutcomeInspectionRepository)
	if !ok {
		return out, conversationFailure("unavailable", "outcome_inspection_unavailable")
	}
	inspection, err := repo.BeginConversationOutcomeInspection(ctx, d.ID, in.ExpectedRevision, *in.Inspection, in.ClientID, a)
	if err != nil {
		return out, err
	}
	if inspection.Record.State == "completed" && inspection.Record.Result != nil {
		return s.projectConversationDelegation(ctx, d, a)
	}
	request := inspection.Request
	request.CorrelationID = inspection.Run.CorrelationID
	// Inspect the frozen Agent's tool scope without upgrading its configuration
	// or enabling execution. Current action and source access are checked below.
	ctx = context.WithValue(ctx, conversationAgentContextKey{}, inspection.Run.Agent)
	ready, readyErr := s.conversationToolAvailable(ctx, a, request.Definition.Key)
	auth, authErr := s.options.ToolHost.AuthorizeConversationTool(ctx, request)
	if authErr == nil && (!auth.Granted || auth.ConfirmationRequired) {
		authErr = conversationFailure("forbidden", "tool_access_denied")
	}
	if authErr == nil && readyErr != nil {
		authErr = readyErr
	} else if authErr == nil && !ready {
		authErr = conversationFailure("unavailable", "tool_unavailable")
	}
	result := sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown", Content: json.RawMessage(`{"error":"external_result_unknown"}`)}
	if authErr != nil {
		err = authErr
	} else {
		toolCtx, cancel := s.externalCallContext(ctx, time.Duration(request.Definition.TimeoutMillis)*time.Millisecond)
		result, err = inspectOutcome(toolCtx, s.options.ToolHost, request)
		cancel()
	}
	if err == nil {
		err = s.authorizeConversationExecution(ctx, d.ConversationID, request.RunID, "inspect_outcome_result", a)
	}
	if err == nil && result.Status == "completed" {
		schema, compileErr := compileConversationSchema(request.Definition.OutputSchema)
		if compileErr != nil || len(result.Content) > request.Definition.MaxOutputBytes || validateToolJSON(schema, result.Content) != nil {
			err = conversationFailure("unavailable", "tool_output_invalid")
		} else {
			err = s.authorizeStoredToolResult(ctx, request, result)
		}
	}
	if err != nil || result.Status != "completed" && result.Status != "failed" || result.Status == "failed" && result.ErrorCode == "" || result.Completion != "" && (result.Completion != "accepted" || result.Status != "completed") || len(result.Content) > request.Definition.MaxOutputBytes || len(result.Content) > 0 && !json.Valid(result.Content) {
		result = sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown", Content: json.RawMessage(`{"error":"external_result_unknown"}`)}
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := repo.FinishConversationOutcomeInspection(receiptCtx, inspection, result, a); saveErr != nil {
		return out, saveErr
	}
	if err != nil {
		var coded *sdk.Error
		if errors.As(err, &coded) {
			return out, err
		}
		return out, conversationFailure("unavailable", "outcome_inspection_unavailable")
	}
	return s.projectConversationDelegation(ctx, d, a)
}
