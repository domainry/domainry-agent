package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// VerifyConversationSources is the Agent-owned read boundary used by Delivery
// before it freezes requirement provenance. Repository reads apply the current
// Conversation sharing policy for the supplied authority; no message, result,
// attachment or artifact body leaves Agent.
func (service *ConversationService) VerifyConversationSources(ctx context.Context, request agentsdk.ConversationSourceVerificationRequest) (agentsdk.ConversationSourceVerificationReceipt, error) {
	if err := request.Validate(); err != nil {
		return agentsdk.ConversationSourceVerificationReceipt{}, conversationFailure("invalid", "source_verification_invalid")
	}
	references := make(map[agentsdk.ConversationRunReference]struct{}, len(request.References))
	for _, reference := range request.References {
		conversation, err := service.repo.Get(ctx, reference.ConversationID, request.Reader)
		if err != nil {
			return agentsdk.ConversationSourceVerificationReceipt{}, err
		}
		if conversation.WorkspaceID != request.Reader.WorkspaceID {
			return agentsdk.ConversationSourceVerificationReceipt{}, conversationFailure("forbidden", "source_workspace_mismatch")
		}
		run, err := service.repo.Run(ctx, reference.ConversationID, reference.RunID, request.Reader)
		if err != nil {
			return agentsdk.ConversationSourceVerificationReceipt{}, err
		}
		if run.ConversationID != reference.ConversationID || (reference.BeforeStep > 0 && reference.BeforeStep > len(run.Steps)+1) {
			return agentsdk.ConversationSourceVerificationReceipt{}, conversationFailure("invalid", "source_run_boundary_invalid")
		}
		reference.BeforeStep = 0
		references[reference] = struct{}{}
	}
	for _, sourceID := range request.SourceIDs {
		reference, err := agentsdk.ParseConversationRunSourceID(sourceID)
		if err != nil {
			return agentsdk.ConversationSourceVerificationReceipt{}, conversationFailure("invalid", "source_identity_invalid")
		}
		if _, found := references[reference]; !found {
			return agentsdk.ConversationSourceVerificationReceipt{}, conversationFailure("invalid", "source_run_mismatch")
		}
	}
	return agentsdk.ConversationSourceVerificationReceipt{
		WorkspaceID: request.Reader.WorkspaceID,
		References:  append([]agentsdk.ConversationRunReference(nil), request.References...),
		SourceIDs:   append([]string(nil), request.SourceIDs...),
		VerifiedAt:  time.Now().UTC(),
	}, nil
}

var _ agentsdk.ConversationSourceVerifier = (*ConversationService)(nil)
