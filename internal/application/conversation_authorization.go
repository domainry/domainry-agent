package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) authorizeConversationExecution(ctx context.Context, id, run, stage string, a agentsdk.ConversationAuthority) error {
	if err := s.authorize(a); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.options.ExecutionAuthorizer == nil {
		// Legacy service-only deployments still delegate policy to their host.
		// Identity-backed module assembly requires this port before workers start.
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	allowed, err := s.options.ExecutionAuthorizer.AuthorizeConversationExecution(ctx, agentsdk.ConversationExecutionAuthorizationRequest{Authority: a, ConversationID: id, RunID: run, Stage: stage})
	if err != nil || ctx.Err() != nil {
		// Host errors can contain credentials or private Identity details.
		return conversationFailure("unavailable", "execution_authorization_unavailable")
	}
	if !allowed {
		return conversationFailure("forbidden", "execution_access_denied")
	}
	return nil
}

func (s *ConversationService) authorizeConversationClaim(ctx context.Context, claim persistence.ConversationClaim, stage string) error {
	return s.authorizeConversationExecution(ctx, claim.Run.ConversationID, claim.Run.ID, stage, claim.Authority)
}
