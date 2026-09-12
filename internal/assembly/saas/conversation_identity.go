// Package saas composes standalone Agent dependencies through their public SDKs.
package saas

import (
	"context"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
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
	application := identitysdk.ApplicationRef{TenantID: identitysdk.TenantID(strings.TrimSpace(config.TenantID)), WorkspaceID: identitysdk.WorkspaceID(strings.TrimSpace(config.WorkspaceID)), ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(config.Audience))}
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
	scope := identitysdk.ApplicationScope{TenantID: h.application.TenantID, WorkspaceID: h.application.WorkspaceID, ApplicationKey: h.application.ApplicationKey}
	resolution, err := h.binding.Principals().Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: scope, SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return false, err
	}
	p := resolution.Principal
	return p.Known && p.UserID == a.UserID && p.WorkspaceID == a.WorkspaceID, nil
}

var _ agentsdk.ConversationExecutionAuthorizer = (*ConversationIdentity)(nil)
