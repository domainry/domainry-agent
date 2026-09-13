package web

import (
	"context"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

func (h *Host) AuthorizeConversationCollaboration(ctx context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	var out sdk.ConversationCollaborationAuthorization
	resolution, known, err := h.resolveConversationPrincipal(ctx, in.Authority)
	if err != nil || !known || in.OwnerUserID == "" {
		return out, err
	}
	out.Revision = string(resolution.AccessBundle.AuthorizationRevision)
	for _, operation := range in.Operations {
		permission := sdk.ConversationCollaborationPermission(operation)
		if permission == nil {
			continue
		}
		decision, err := evaluator.Evaluate(resolution.AccessBundle, identity.AccessRequest{ObjectKey: permission.ResourceKey, Action: permission.OperationKey}, identity.ResourceFacts{"owner_user_id": in.OwnerUserID, "workspace_id": in.Authority.WorkspaceID, "delegation_id": in.DelegationID, "from_agent_id": in.FromAgentID, "to_agent_id": in.ToAgentID}, time.Now().UTC())
		if err != nil {
			return sdk.ConversationCollaborationAuthorization{}, err
		}
		if decision.Allowed {
			out.Allowed = append(out.Allowed, operation)
		}
	}
	return out, nil
}

var _ sdk.ConversationCollaborationAuthorizer = (*Host)(nil)
