package application

import (
	"context"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationCodingAuthorizer struct{ s *ConversationService }

func (authorizer conversationCodingAuthorizer) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	if authorizer.s == nil || authorizer.s.options.PersonalAuthorizer == nil || !agentsdk.IsConversationCodingTool(in.Definition.Key) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	known := false
	for _, definition := range agentsdk.ConversationCodingTools() {
		if conversationDigest(definition) == conversationDigest(in.Definition) {
			known = true
			break
		}
	}
	if !known || !in.Authority.Known {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	auth, err := authorizer.s.options.PersonalAuthorizer.AuthorizeConversationTool(ctx, in)
	if err != nil || !auth.Granted || in.Definition.Effect != "write" || auth.ConfirmationRequired {
		return auth, err
	}
	auth.ConfirmationRequired = true
	if in.RunID == "" || in.ConversationID == "" {
		return auth, nil
	}
	run, err := authorizer.s.repo.Run(ctx, in.ConversationID, in.RunID, in.Authority)
	if err != nil {
		return auth, err
	}
	if run.WriteScope.Allows(in.Definition.Key) {
		auth.ConfirmationRequired = false
		return auth, nil
	}
	if in.Confirmation == nil {
		return auth, nil
	}
	claim := personalToolClaim(in)
	record, found, err := authorizer.s.repo.(persistence.ConversationInteractionRepository).ExecutionInteraction(ctx, claim, in.Step, in.Call.ID, "confirmation")
	if err != nil {
		return auth, err
	}
	receipt := confirmationReceipt(record.Interaction, in.Authority)
	if found && receipt != nil && conversationDigest(receipt) == conversationDigest(in.Confirmation) && receipt.ID == in.ConfirmationID && record.Interaction.DefinitionHash == conversationDigest(in.Definition) && record.Interaction.ArgumentsHash == conversationDigest(in.Call.Arguments) {
		auth.ConfirmationRequired = false
	}
	return auth, nil
}

func conversationCodingScope(a agentsdk.ConversationAuthority, conversationID, runID string) agentsdk.ConversationCodingScope {
	return agentsdk.ConversationCodingScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID, ConversationID: conversationID, RunID: runID}
}

func (s *ConversationService) executeConversationCoding(ctx context.Context, request agentsdk.ConversationToolRequest, reconcile bool) (agentsdk.ConversationToolResult, error) {
	if s.options.CodingRuntime == nil || !agentsdk.IsConversationCodingTool(request.Call.Name) {
		return personalToolFailure("coding_runtime_unavailable"), nil
	}
	return s.options.CodingRuntime.ExecuteConversationCoding(ctx, agentsdk.ConversationCodingRequest{
		Scope: conversationCodingScope(request.Authority, request.ConversationID, request.RunID), Tool: request.Call.Name,
		Arguments: json.RawMessage(request.Call.Arguments), IdempotencyKey: request.IdempotencyKey, Reconcile: reconcile,
	})
}

func (s *ConversationService) closeConversationCodingScope(ctx context.Context, a agentsdk.ConversationAuthority, conversationID, runID string) {
	if s.options.CodingRuntime != nil {
		_ = s.options.CodingRuntime.CloseConversationCodingScope(ctx, conversationCodingScope(a, conversationID, runID))
	}
}
