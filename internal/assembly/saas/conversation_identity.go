// Package saas composes standalone Agent dependencies through their public SDKs.
package saas

import (
	"context"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
)

// ConversationIdentity binds one deployed Agent runtime to an explicit Identity
// application scope. The service credential remains owned by the SDK transport;
// it is never part of an Agent run or a browser invocation.
type ConversationIdentity struct {
	binding     identitysdk.Binding
	runtimeID   string
	application identitysdk.ApplicationRef
}

func OpenConversationIdentity(ctx context.Context, runtimeID string, config identityremote.Config) (*ConversationIdentity, error) {
	if strings.TrimSpace(runtimeID) == "" || strings.TrimSpace(config.Endpoint) == "" || strings.TrimSpace(config.WorkspaceID) == "" || strings.TrimSpace(config.Audience) == "" || strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.ServiceAccessToken) == "" || strings.TrimSpace(config.CapabilityContractSHA256) == "" {
		return nil, errors.New("Agent SaaS conversations require IDENTITY_ENDPOINT, IDENTITY_WORKSPACE_ID, IDENTITY_AUDIENCE, IDENTITY_ISSUER, IDENTITY_SERVICE_ACCESS_TOKEN and IDENTITY_CAPABILITY_CONTRACT_SHA256")
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(strings.TrimSpace(config.WorkspaceID)), ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(config.Audience))}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	binding, err := identityremote.NewFactory(config).Open(ctx, application)
	if err != nil {
		// Configuration or remote error text can include a credential-bearing URL.
		return nil, errors.New("Agent SaaS Identity binding is unavailable or incompatible")
	}
	return &ConversationIdentity{binding: binding, runtimeID: runtimeID, application: application}, nil
}

func (h *ConversationIdentity) WorkspaceID() string { return string(h.application.WorkspaceID) }

func (h *ConversationIdentity) Close(ctx context.Context) error { return h.binding.Close(ctx) }

func (h *ConversationIdentity) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	a := in.Authority
	if !a.Known || a.RuntimeID != h.runtimeID || a.WorkspaceID != string(h.application.WorkspaceID) || strings.TrimSpace(a.UserID) == "" {
		return false, nil
	}
	// The public SDK sends the configured application service credential and
	// performs a fresh principal resolution; no queued policy bundle is reused.
	resolution, err := h.binding.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		if subjectResolutionDenied(err) {
			return false, nil
		}
		return false, err
	}
	p := resolution.Principal
	return p.Known && p.UserID == a.UserID && p.WorkspaceID == a.WorkspaceID, nil
}

var _ agentsdk.ConversationExecutionAuthorizer = (*ConversationIdentity)(nil)

// Source Identity returns these exact subject denials after authenticating the
// application credential. Credential failures and malformed responses remain
// unavailable, so they cannot be mistaken for a successful policy lookup.
func subjectResolutionDenied(err error) bool {
	var boundary *identitysdk.Error
	if !errors.As(err, &boundary) || boundary.StatusCode != 403 && boundary.StatusCode != 404 {
		return false
	}
	switch boundary.Code {
	case "identity.principal_unavailable", "identity.session_role_unavailable", "identity.subject_not_found":
		return true
	default:
		return false
	}
}

func (h *ConversationIdentity) AuthorizeConversationCollaboration(ctx context.Context, in agentsdk.ConversationCollaborationAuthorizationRequest) (agentsdk.ConversationCollaborationAuthorization, error) {
	var out agentsdk.ConversationCollaborationAuthorization
	a := in.Authority
	if !a.Known || a.RuntimeID != h.runtimeID || a.WorkspaceID != string(h.application.WorkspaceID) || a.UserID == "" || in.OwnerUserID == "" {
		return out, nil
	}
	resolution, err := h.binding.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		if subjectResolutionDenied(err) {
			return out, nil
		}
		return out, err
	}
	if !resolution.Principal.Known || resolution.Principal.UserID != a.UserID || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return out, nil
	}
	out.Revision = string(resolution.AccessBundle.AuthorizationRevision)
	for _, operation := range in.Operations {
		p := agentsdk.ConversationCollaborationPermission(operation)
		if p == nil {
			continue
		}
		decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: p.ResourceKey, Action: p.OperationKey}, identitysdk.ResourceFacts{"owner_user_id": in.OwnerUserID, "workspace_id": a.WorkspaceID, "delegation_id": in.DelegationID, "from_agent_id": in.FromAgentID, "to_agent_id": in.ToAgentID}, time.Now().UTC())
		if err != nil {
			return agentsdk.ConversationCollaborationAuthorization{}, err
		}
		if decision.Allowed {
			out.Allowed = append(out.Allowed, operation)
		}
	}
	return out, nil
}

var _ agentsdk.ConversationCollaborationAuthorizer = (*ConversationIdentity)(nil)
