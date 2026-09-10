package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (h *businessConversationHost) authorizeBusinessActionTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	source, ok := h.source.(agentsdk.ConversationBusinessActionSource)
	if !ok {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	return h.authorizeBusinessMutation(ctx, in, func(arguments string) (agentsdk.ConversationToolAuthorization, error) {
		var action agentsdk.ConversationBusinessAction
		_ = json.Unmarshal([]byte(arguments), &action)
		return source.AuthorizeBusinessAction(ctx, action, in.Authority)
	})
}

func (h *businessConversationHost) authorizeBusinessMutation(ctx context.Context, in agentsdk.ConversationToolRequest, preflight func(string) (agentsdk.ConversationToolAuthorization, error)) (agentsdk.ConversationToolAuthorization, error) {
	if _, ok := h.repo.(persistence.ConversationInteractionRepository); !ok {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if _, ok := h.base.(agentsdk.ConversationInteractionAuthorizer); !ok {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if support, ok := h.base.(interface{ supportsConversationInteractions() bool }); ok && !support.supportsConversationInteractions() {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if in.ConversationID != "" {
		if _, err := h.repo.Get(ctx, in.ConversationID, in.Authority); err != nil {
			return agentsdk.ConversationToolAuthorization{}, err
		}
	}
	auth, err := h.authorizer.AuthorizeConversationTool(ctx, in)
	if err != nil || !auth.Granted {
		return auth, err
	}
	if in.Call.Arguments != "" {
		schema, err := compileConversationSchema(in.Definition.InputSchema)
		if err != nil {
			return agentsdk.ConversationToolAuthorization{}, err
		}
		if validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
			// Let the executor record schema feedback without approving an
			// invalid request. The invocation boundary checks the schema again.
			auth.ConfirmationRequired = false
			return auth, nil
		}
	}
	business, err := preflight(in.Call.Arguments)
	if err != nil {
		var coded *agentsdk.Error
		if errors.As(err, &coded) && coded.Class == "bad_request" {
			auth.ConfirmationRequired = false
			return auth, nil
		}
		return agentsdk.ConversationToolAuthorization{}, err
	}
	if !business.Granted {
		return business, nil
	}
	if auth.ConfirmationRequired || business.ConfirmationRequired {
		auth.ConfirmationRequired = true
		return auth, nil
	}
	auth.ConfirmationRequired = true
	if in.ConversationID == "" || in.RunID == "" || in.Confirmation == nil {
		return auth, nil
	}
	interactions, ok := h.repo.(persistence.ConversationInteractionRepository)
	if !ok {
		return agentsdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
	}
	record, found, err := interactions.ExecutionInteraction(ctx, personalToolClaim(in), in.Step, in.Call.ID, "confirmation")
	if err != nil {
		return auth, err
	}
	i := record.Interaction
	receipt := confirmationReceipt(i, in.Authority)
	if found && receipt != nil && conversationDigest(receipt) == conversationDigest(in.Confirmation) && receipt.ID == in.ConfirmationID && i.DefinitionHash == conversationDigest(in.Definition) && i.ArgumentsHash == conversationDigest(in.Call.Arguments) {
		auth.ConfirmationRequired = false
	}
	return auth, nil
}

func businessActionFailureCode(err error) string {
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		switch coded.Code {
		case "business_action_changed", "business_action_invalid", "business_record_version_conflict", "business_action_assurance_required", "business_action_receipt_conflict":
			return coded.Code
		}
	}
	return businessFailureCode(err)
}

func validBusinessActionResult(result agentsdk.ConversationBusinessActionResult, action agentsdk.ConversationBusinessAction) bool {
	if result.InvocationID == "" || len(result.InvocationID) > 256 || result.ActionKey != action.ActionKey || result.ObjectKey != action.ObjectKey || result.RecordID != action.RecordID || result.ErrorCode != "" {
		return false
	}
	refs := append([]agentsdk.ConversationBusinessRecordReference(nil), result.CreatedRecords...)
	refs = append(refs, result.UpdatedRecords...)
	refs = append(refs, result.DeletedRecords...)
	refs = append(refs, result.RestoredRecords...)
	if len(refs) > 100 || result.ReferenceCount < len(refs) || result.ReferencesTruncated != (result.ReferenceCount > len(refs)) {
		return false
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.ObjectKey) == "" || len(ref.ObjectKey) > 128 || strings.TrimSpace(ref.RecordID) == "" || len(ref.RecordID) > 256 {
			return false
		}
	}
	return true
}

func (h *businessConversationHost) invokeBusinessActionTool(ctx context.Context, in agentsdk.ConversationToolRequest, reconcile bool) (agentsdk.ConversationToolResult, error) {
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil {
		return personalToolFailure(businessActionFailureCode(err)), nil
	}
	if !auth.Granted || auth.ConfirmationRequired || in.Call.Name != "invoke_action" {
		return personalToolFailure("tool_access_denied"), nil
	}
	schema, err := compileConversationSchema(in.Definition.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	var action agentsdk.ConversationBusinessAction
	_ = json.Unmarshal([]byte(in.Call.Arguments), &action)
	source, ok := h.source.(agentsdk.ConversationBusinessActionSource)
	if !ok {
		return personalToolFailure("business_unavailable"), nil
	}
	// Preflight validation failures are correction results, not user approvals.
	check, err := source.AuthorizeBusinessAction(ctx, action, in.Authority)
	if err != nil {
		return personalToolFailure(businessActionFailureCode(err)), nil
	}
	if !check.Granted || check.ConfirmationRequired || in.Confirmation == nil || in.IdempotencyKey == "" || in.RunID == "" || in.ConversationID == "" || in.Call.ID == "" {
		return personalToolFailure("tool_access_denied"), nil
	}
	request := agentsdk.ConversationBusinessActionRequest{Authority: in.Authority, Action: action, Arguments: in.Call.Arguments, ConversationID: in.ConversationID, RunID: in.RunID, Step: in.Step, CallID: in.Call.ID, IdempotencyKey: in.IdempotencyKey, Confirmation: in.Confirmation}
	identity := h.source.BusinessSourceIdentity()
	var result agentsdk.ConversationBusinessActionResult
	if reconcile {
		result, err = source.ReconcileBusinessAction(ctx, request)
	} else {
		result, err = source.InvokeBusinessAction(ctx, request)
	}
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	unknown := func() (agentsdk.ConversationToolResult, error) {
		return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown", Content: json.RawMessage(`{"error":"external_result_unknown"}`)}, nil
	}
	switch result.Status {
	case "failed":
		return personalToolFailure(businessActionFailureCode(&agentsdk.Error{Class: "bad_request", Code: result.ErrorCode})), nil
	case "completed":
		if identity != h.source.BusinessSourceIdentity() || !validBusinessActionResult(result, action) {
			return unknown()
		}
		data, err := json.Marshal(result)
		if err != nil {
			return unknown()
		}
		out, err := personalToolResult(agentsdk.ConversationBusinessEvidence{Version: 1, Source: identity, ScopeSHA256: businessEvidenceScope(identity, in.Authority), Operation: "invoke_action", Input: json.RawMessage(in.Call.Arguments), Data: data})
		out.ResourceID = result.InvocationID
		return out, err
	default:
		return unknown()
	}
}
