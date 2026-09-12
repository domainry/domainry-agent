package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// A scope starts from an already frozen execution plan. Neither model text nor
// a client-supplied target list becomes approval. The authenticated response
// selects this exact, displayed list, and all policies are checked again then.
func (s *ConversationService) confirmationOperations(ctx context.Context, claim persistence.ConversationClaim, step persistence.ConversationExecutionStep, currentCall string) []string {
	_, catalog, err := s.executionCatalog(ctx, claim.Authority)
	if err != nil || step.Result == nil {
		return nil
	}
	remaining := false
	var ids []string
	for _, call := range step.Result.Message.ToolCalls {
		remaining = remaining || call.ID == currentCall
		if !remaining {
			continue
		}
		tool, exists := catalog[call.Name]
		if !exists {
			return nil
		}
		if tool.definition.Effect != "write" {
			continue
		}
		var frozen agentsdk.ConversationToolDefinition
		for _, definition := range step.Input.Tools {
			if definition.Key == call.Name {
				frozen = definition
				break
			}
		}
		if conversationDigest(frozen) != conversationDigest(tool.definition) || validateToolJSON(tool.input, []byte(call.Arguments)) != nil {
			return nil
		}
		request := agentsdk.ConversationToolRequest{Authority: claim.Authority, ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Step: step.Number, Call: call, Definition: frozen, LeaseOwner: claim.Owner, Fence: claim.Fence}
		auth, err := s.options.ToolHost.AuthorizeConversationTool(ctx, request)
		if err != nil || !auth.Granted {
			return nil
		}
		if !auth.ConfirmationRequired {
			continue
		}
		ids = append(ids, call.ID)
		if len(ids) > 20 {
			return nil
		}
	}
	if len(ids) < 2 || ids[0] != currentCall {
		return nil
	}
	return ids
}

func (s *ConversationService) authorizeListedOperations(ctx context.Context, i agentsdk.ConversationInteraction, a agentsdk.ConversationAuthority) error {
	if i.Kind != "confirmation" || len(i.Operations) < 2 || len(i.Operations) > 20 || i.Operations[0].CallID != i.CallID {
		return conversationFailure("bad_request", "interaction_scope_invalid")
	}
	_, catalog, err := s.executionCatalog(ctx, a)
	if err != nil {
		return err
	}
	for _, operation := range i.Operations {
		tool, exists := catalog[operation.Tool]
		if !exists {
			return conversationFailure("forbidden", "tool_access_denied")
		}
		if tool.definition.Effect != "write" || conversationDigest(tool.definition) != operation.DefinitionHash || conversationDigest(operation.Arguments) != operation.ArgumentsHash || validateToolJSON(tool.input, []byte(operation.Arguments)) != nil {
			return conversationFailure("conflict", "tool_changed")
		}
		request := agentsdk.ConversationToolRequest{Authority: a, ConversationID: i.ConversationID, RunID: i.RunID, Step: i.Step, Call: agentsdk.ConversationToolCall{ID: operation.CallID, Name: operation.Tool, Arguments: operation.Arguments}, Definition: tool.definition}
		auth, err := s.options.ToolHost.AuthorizeConversationTool(ctx, request)
		if err != nil {
			return err
		}
		if !auth.Granted {
			return conversationFailure("forbidden", "tool_access_denied")
		}
	}
	return nil
}
