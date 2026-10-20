package application

import (
	"context"
	"errors"
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
	ctx, cancel := s.externalCallContext(ctx, 5*time.Second)
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

type conversationResultAuthorizationFailure interface {
	error
	ConversationResultAuthorizationFailed()
}

func resultAuthorizationFailed(err error) bool {
	var failure conversationResultAuthorizationFailure
	return errors.As(err, &failure)
}

func (s *ConversationService) authorizeStoredToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if result.Status != "completed" {
		return nil
	}
	policy, ok := s.options.ToolHost.(agentsdk.ConversationToolResultAuthorizer)
	if !ok {
		return nil
	}
	ctx, cancel := s.externalCallContext(ctx, time.Duration(in.Definition.TimeoutMillis)*time.Millisecond)
	defer cancel()
	return policy.AuthorizeConversationToolResult(ctx, in, result)
}

func (s *ConversationService) authorizeConversationClaim(ctx context.Context, claim persistence.ConversationClaim, stage string) error {
	if _, err := s.selectConversationAgent(ctx, claim.Run.Agent, claim.Authority); err != nil {
		return err
	}
	if task := claim.Run.BackgroundTask; task != nil && task.DelegationID != "" {
		repo, err := s.collaborationRepository()
		if err != nil {
			return err
		}
		d, err := repo.ConversationDelegation(ctx, task.DelegationID, claim.Authority)
		if err != nil {
			return err
		}
		if err := s.authorizeDelegationExecutionBinding(ctx, d, claim.Run.Agent, claim.Authority); err != nil {
			return err
		}
		if err = s.authorizeCollaboration(ctx, "receive", &d, claim.Authority); err != nil {
			return err
		}
		if d.TaskID != task.TaskID || d.ConversationID != claim.Run.ConversationID || d.Brief.Version != task.BriefVersion || max(1, task.AgreementRevision) != max(1, d.AgreementRevision) || len(d.PendingChanges) > 0 || d.Status != "running" && d.Status != "delivered" {
			return conversationFailure("conflict", "delegation_superseded")
		}
	}
	return s.authorizeConversationExecution(ctx, claim.Run.ConversationID, claim.Run.ID, stage, claim.Authority)
}
