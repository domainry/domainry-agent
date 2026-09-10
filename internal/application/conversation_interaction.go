package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

var errConversationWaiting = errors.New("conversation execution is durably waiting")

func (s *ConversationService) waitConversation(ctx context.Context, claim persistence.ConversationClaim, wait persistence.ConversationWait) error {
	repo, ok := s.repo.(persistence.ConversationInteractionRepository)
	if !ok {
		return conversationFailure("unavailable", "interaction_unavailable")
	}
	if wait.Kind != "reconciliation" {
		if _, ok := s.options.ToolHost.(agentsdk.ConversationInteractionAuthorizer); !ok {
			return conversationFailure("unavailable", "interaction_unavailable")
		}
		wait.TTL = s.options.InteractionTTL
	}
	if _, err := repo.WaitExecution(ctx, claim, wait); err != nil {
		return err
	}
	return errConversationWaiting
}

func (s *ConversationService) toolInteraction(ctx context.Context, claim persistence.ConversationClaim, step int, call agentsdk.ConversationToolCall, definition agentsdk.ConversationToolDefinition) (persistence.ConversationInteractionRecord, bool, error) {
	repo, ok := s.repo.(persistence.ConversationInteractionRepository)
	if !ok {
		return persistence.ConversationInteractionRecord{}, false, nil
	}
	kind := "confirmation"
	if call.Name == "ask_user" {
		kind = "input"
	}
	record, found, err := repo.ExecutionInteraction(ctx, claim, step, call.ID, kind)
	if err != nil || !found {
		return record, found, err
	}
	i := record.Interaction
	if i.Tool != definition.Key || i.ToolVersion != definition.Version || i.ActionKey != definition.ActionKey || i.ArgumentsHash != conversationDigest(call.Arguments) || i.DefinitionHash != conversationDigest(definition) {
		return record, found, conversationFailure("conflict", "tool_changed")
	}
	if i.Status == "rejected" || i.Status == "cancelled" || i.Status == "expired" {
		return record, found, conversationFailure("conflict", "interaction_closed")
	}
	return record, found, nil
}

func confirmationReceipt(i agentsdk.ConversationInteraction, a agentsdk.ConversationAuthority) *agentsdk.ConversationConfirmation {
	if i.Kind != "confirmation" || i.Status != "approved" || i.RespondedBy != a.UserID || i.RespondedAt == nil {
		return nil
	}
	return &agentsdk.ConversationConfirmation{ID: i.ID, UserID: i.RespondedBy, ActionKey: i.ActionKey, ToolVersion: i.ToolVersion, ArgumentsHash: i.ArgumentsHash, ApprovedAt: *i.RespondedAt}
}

func (s *ConversationService) Respond(ctx context.Context, id, runID string, response agentsdk.ConversationInteractionResponse, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	if !conversationKey(response.InteractionID) || !conversationKey(response.ClientID) || response.ExpectedRevision < 1 || !conversationText(response.Answer, min(s.options.MaxInputBytes, 16384), false) || response.Decision != "answer" && response.Decision != "approve" && response.Decision != "reject" {
		return agentsdk.ConversationRun{}, conversationFailure("bad_request", "interaction_response_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationInteractionRepository)
	authorizer, authorized := s.options.ToolHost.(agentsdk.ConversationInteractionAuthorizer)
	if !ok || !authorized {
		return agentsdk.ConversationRun{}, conversationFailure("unavailable", "interaction_unavailable")
	}
	run, err := s.repo.Run(ctx, id, runID, a)
	if err != nil {
		return agentsdk.ConversationRun{}, err
	}
	if run.Interaction == nil {
		return agentsdk.ConversationRun{}, conversationFailure("not_found", "interaction_not_found")
	}
	// The repository resolves the exact response ID, including duplicate
	// responses to an earlier step. Policy never comes from the response body.
	auth, err := authorizer.AuthorizeConversationInteraction(ctx, a, *run.Interaction)
	if err != nil {
		return agentsdk.ConversationRun{}, err
	}
	if !auth.Granted {
		return agentsdk.ConversationRun{}, conversationFailure("forbidden", "interaction_access_denied")
	}
	if run.Interaction.ID == response.InteractionID && run.Interaction.Status == "pending" && response.Decision != "reject" {
		i := run.Interaction
		_, tools, err := s.executionCatalog(ctx, a)
		if err != nil {
			return agentsdk.ConversationRun{}, err
		}
		tool, exists := tools[i.Tool]
		if !exists {
			return agentsdk.ConversationRun{}, conversationFailure("forbidden", "tool_access_denied")
		}
		if conversationDigest(tool.definition) != i.DefinitionHash || conversationDigest(i.Arguments) != i.ArgumentsHash {
			return agentsdk.ConversationRun{}, conversationFailure("conflict", "tool_changed")
		}
		auth, err := s.options.ToolHost.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, ConversationID: id, RunID: runID, Step: i.Step, Call: agentsdk.ConversationToolCall{ID: i.CallID, Name: i.Tool, Arguments: i.Arguments}, Definition: tool.definition})
		if err != nil {
			return agentsdk.ConversationRun{}, err
		}
		if !auth.Granted {
			return agentsdk.ConversationRun{}, conversationFailure("forbidden", "tool_access_denied")
		}
	}
	if response.Decision != "reject" {
		if err := s.checkRunSources(ctx, agentsdk.ConversationRunReference{ConversationID: id, RunID: runID}, a); err != nil {
			return agentsdk.ConversationRun{}, conversationFailure("forbidden", "source_access_unavailable")
		}
	}
	out, err := repo.RespondExecution(ctx, id, runID, response, a)
	if err == nil {
		s.signal()
		out = s.projectConversationRun(ctx, out, a)
	}
	return out, err
}

func (s *ConversationService) expireConversationInteractions(ctx context.Context, repo persistence.ConversationInteractionRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(max(50*time.Millisecond, min(s.options.Poll, time.Minute)))
	defer ticker.Stop()
	failed := false
	for {
		_, err := repo.ExpireInteractions(ctx, s.runtimeID, 100)
		if err != nil && !failed && ctx.Err() == nil {
			slog.Warn("conversation interaction expiration unavailable")
		}
		failed = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

var _ agentsdk.ConversationInteractionService = (*ConversationService)(nil)
