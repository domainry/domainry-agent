package application

import (
	"context"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (h *businessConversationHost) authorizeBusinessWorkflowTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	source, ok := h.source.(agentsdk.ConversationBusinessWorkflowSource)
	if !ok {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	return h.authorizeBusinessMutation(ctx, in, func(arguments string) (agentsdk.ConversationToolAuthorization, error) {
		var start agentsdk.ConversationWorkflowStart
		_ = json.Unmarshal([]byte(arguments), &start)
		return source.AuthorizeWorkflowStart(ctx, start, in.Authority)
	})
}

func (h *businessConversationHost) invokeBusinessWorkflowTool(ctx context.Context, in agentsdk.ConversationToolRequest, reconcile bool) (agentsdk.ConversationToolResult, error) {
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil {
		return personalToolFailure(businessFailureCode(err)), nil
	}
	source, ok := h.source.(agentsdk.ConversationBusinessWorkflowSource)
	if !ok || !auth.Granted || auth.ConfirmationRequired || in.Call.Name != in.Definition.Key {
		return personalToolFailure("tool_access_denied"), nil
	}
	schema, err := compileConversationSchema(in.Definition.InputSchema)
	if err != nil || validateToolJSON(schema, []byte(in.Call.Arguments)) != nil {
		return personalToolFailure("arguments_invalid"), nil
	}
	identity := h.source.BusinessSourceIdentity()
	var output any
	if in.Call.Name == "workflow_start" {
		var start agentsdk.ConversationWorkflowStart
		_ = json.Unmarshal([]byte(in.Call.Arguments), &start)
		check, err := source.AuthorizeWorkflowStart(ctx, start, in.Authority)
		if err != nil {
			return personalToolFailure(businessFailureCode(err)), nil
		}
		if !check.Granted || check.ConfirmationRequired || in.Confirmation == nil || in.IdempotencyKey == "" || in.RunID == "" || in.ConversationID == "" || in.Call.ID == "" {
			return personalToolFailure("tool_access_denied"), nil
		}
		request := agentsdk.ConversationWorkflowStartRequest{Authority: in.Authority, Start: start, ConversationID: in.ConversationID, RunID: in.RunID, CorrelationID: in.CorrelationID, Step: in.Step, CallID: in.Call.ID, IdempotencyKey: in.IdempotencyKey, Confirmation: in.Confirmation, Arguments: in.Call.Arguments}
		var receipt agentsdk.ConversationWorkflowReceipt
		if reconcile {
			receipt, err = source.ReconcileBusinessWorkflow(ctx, request)
		} else {
			receipt, err = source.StartBusinessWorkflow(ctx, request)
		}
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if receipt.Status == "failed" {
			return personalToolFailure("business_request_invalid"), nil
		}
		if identity != h.source.BusinessSourceIdentity() || receipt.Status != "accepted" || receipt.ErrorCode != "" || receipt.WorkflowKey != start.WorkflowKey || receipt.InvocationID == "" || len(receipt.InvocationID) > 256 || receipt.ProcessID == "" || len(receipt.ProcessID) > 256 || receipt.ExecutionID == "" || len(receipt.ExecutionID) > 256 {
			return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown", Content: json.RawMessage(`{"error":"external_result_unknown"}`)}, nil
		}
		output = receipt
	} else {
		var q agentsdk.ConversationWorkflowGet
		_ = json.Unmarshal([]byte(in.Call.Arguments), &q)
		state, err := source.GetBusinessWorkflow(ctx, q, in.Authority)
		if err != nil {
			return personalToolFailure(businessFailureCode(err)), nil
		}
		if state.ProcessID != q.ProcessID || state.WorkflowKey != q.WorkflowKey || state.Status == "" || len(state.Status) > 64 || len(state.Name) > 1024 || len(state.BusinessOutcome) > 1024 || len(state.CurrentSteps) > 100 || state.CurrentStepCount < len(state.CurrentSteps) || state.StepsTruncated != (state.CurrentStepCount > len(state.CurrentSteps)) {
			return personalToolFailure("business_response_invalid"), nil
		}
		output = state
	}
	data, err := json.Marshal(output)
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	evidence := agentsdk.ConversationBusinessEvidence{Version: 1, Source: identity, ScopeSHA256: businessEvidenceScope(identity, in.Authority), Operation: in.Call.Name, Input: json.RawMessage(in.Call.Arguments), Data: data}
	if in.Call.Name == "workflow_get" {
		if sealer, ok := h.source.(agentsdk.ConversationBusinessEvidenceSealer); ok {
			evidence.HostProof, err = sealer.SealBusinessEvidence(ctx, evidence, in.Authority)
			if err != nil || len(evidence.HostProof) > 4096 {
				return personalToolFailure("business_source_changed"), nil
			}
		}
		if identity != h.source.BusinessSourceIdentity() {
			return personalToolFailure("business_source_changed"), nil
		}
	}
	result, err := personalToolResult(evidence)
	if err == nil && in.Call.Name == "workflow_start" {
		result.Completion = "accepted"
	}
	return result, err
}
