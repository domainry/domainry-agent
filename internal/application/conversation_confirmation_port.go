package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Offered only to startup tool composition. External adapters receive the
// public verification port, never Agent persistence or application objects.
type assemblyConfirmationHost struct {
	sdk.ConversationToolHost
	service *ConversationService
}

func (h *assemblyConfirmationHost) VerifyConversationToolConfirmation(ctx context.Context, r sdk.ConversationToolRequest) (bool, error) {
	if !r.Authority.Known || r.Authority.RuntimeID != h.service.runtimeID || r.Authority.WorkspaceID == "" || r.Authority.UserID == "" || r.Definition.Effect != "write" || r.ConversationID == "" || r.RunID == "" || r.Call.ID == "" || r.Call.Name != r.Definition.Key || r.Confirmation == nil || r.ConfirmationID == "" {
		return false, nil
	}
	repo, ok := h.service.repo.(persistence.ConversationInteractionRepository)
	if !ok {
		return false, conversationFailure("unavailable", "interaction_unavailable")
	}
	// Repository lookup checks current conversation ownership, the exact run
	// and call, and any approved list scope. Caller-supplied receipt fields are
	// compared to that durable record, never accepted on their own.
	if _, err := h.service.repo.Run(ctx, r.ConversationID, r.RunID, r.Authority); err != nil {
		return false, err
	}
	record, found, err := repo.ExecutionInteraction(ctx, personalToolClaim(r), r.Step, r.Call.ID, "confirmation")
	if err != nil || !found {
		return false, err
	}
	i := record.Interaction
	confirmed := confirmationReceipt(i, r.Authority)
	return confirmed != nil && conversationDigest(confirmed) == conversationDigest(r.Confirmation) && confirmed.ID == r.ConfirmationID && i.Tool == r.Definition.Key && i.ActionKey == r.Definition.ActionKey && i.ToolVersion == r.Definition.Version && i.DefinitionHash == conversationDigest(r.Definition) && i.ArgumentsHash == conversationDigest(r.Call.Arguments), nil
}

var _ sdk.ConversationConfirmationVerifier = (*assemblyConfirmationHost)(nil)

// Composition must preserve the original host's optional result policy. In
// particular, an accepted local result can become unreadable after revocation.
func (h *assemblyConfirmationHost) AuthorizeConversationToolResult(ctx context.Context, r sdk.ConversationToolRequest, out sdk.ConversationToolResult) error {
	if p, ok := h.ConversationToolHost.(sdk.ConversationToolResultAuthorizer); ok {
		return p.AuthorizeConversationToolResult(ctx, r, out)
	}
	auth, err := h.AuthorizeConversationTool(ctx, r)
	if err != nil {
		return err
	}
	if !auth.Granted {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	return nil
}

func (h *assemblyConfirmationHost) AuthorizeConversationInteraction(ctx context.Context, a sdk.ConversationAuthority, in sdk.ConversationInteraction) (sdk.ConversationToolAuthorization, error) {
	if p, ok := h.ConversationToolHost.(sdk.ConversationInteractionAuthorizer); ok {
		return p.AuthorizeConversationInteraction(ctx, a, in)
	}
	return sdk.ConversationToolAuthorization{}, conversationFailure("unavailable", "interaction_unavailable")
}
