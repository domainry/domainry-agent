package web

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	integration "github.com/domainry/domainry-integration-sdk"
	sdk "github.com/domainry/domainry-tools-sdk"
)

// The host projects live Identity policy for the exact account action. The
// account owner still enforces ownership and actual grants on every SDK call.
func (h *Host) connectionAccountSubject(ctx context.Context, a sdk.Authority, action string) (integration.ConnectionAccountSubject, error) {
	operation := ""
	switch action {
	case integration.ActionIntegrationConnectionAccountsList:
		operation = "list"
	case integration.ActionIntegrationConnectionAccountsRead:
		operation = "read"
	case integration.ActionIntegrationConnectionAccountsWrite:
		operation = "write"
	default:
		return integration.ConnectionAccountSubject{}, fmt.Errorf("unsupported account subject action")
	}
	current, known, err := h.resolveConversationPrincipal(ctx, a)
	if err != nil {
		return integration.ConnectionAccountSubject{}, err
	}
	if !known || current.Principal.MustChangePassword {
		return integration.ConnectionAccountSubject{}, &sdk.Error{Class: "forbidden", Code: "integration.account_access_denied"}
	}
	subject := integration.ConnectionAccountSubject{WorkspaceID: a.WorkspaceID, UserID: a.UserID}
	for _, shared := range []bool{false, true} {
		owner := a.UserID
		if shared {
			owner = ""
		}
		decision, err := evaluator.Evaluate(current.AccessBundle, identity.AccessRequest{ObjectKey: "integration.connection_accounts", Action: operation}, identity.ResourceFacts{"workspace_id": a.WorkspaceID, "owner_user_id": owner}, time.Now().UTC())
		if err != nil {
			return integration.ConnectionAccountSubject{}, err
		}
		if shared {
			subject.Access.Workspace = decision.Allowed
		} else {
			subject.Access.Personal = decision.Allowed
		}
	}
	return subject, nil
}

// ToolAccountRequirement is trusted deployment composition. Alternatives are
// ORed; all scopes within an alternative are required. This preflight cannot
// choose the execution account or grant permission for the eventual operation.
type ToolAccountRequirement struct {
	ConnectorKey, ProviderKey string
	Scopes                    []string
}

func cloneToolAccountRequirements(in map[string][]ToolAccountRequirement) map[string][]ToolAccountRequirement {
	out := make(map[string][]ToolAccountRequirement, len(in))
	for key, alternatives := range in {
		out[key] = append([]ToolAccountRequirement(nil), alternatives...)
		for i := range out[key] {
			out[key][i].Scopes = slices.Clone(out[key][i].Scopes)
		}
	}
	return out
}
func (h *Host) toolAccountAvailable(ctx context.Context, a sdk.Authority, key string) (bool, error) {
	requirements, requiresAccount := h.toolAccountRequirements[key]
	if !requiresAccount {
		return true, nil
	}
	binding, ok := h.Integration.(integration.ConnectionAccountsBinding)
	if !ok || binding.ConnectionAccounts() == nil || len(requirements) == 0 {
		return false, nil
	}
	current, known, err := h.resolveConversationPrincipal(ctx, a)
	if err != nil || !known || current.Principal.MustChangePassword {
		return false, err
	}
	// Evaluate the actual two supported account ownership facts using Identity's
	// public evaluator. Function/data policies and deny guardrails remain current.
	access := integration.ConnectionAccountAccess{}
	for _, shared := range []bool{false, true} {
		owner := a.UserID
		if shared {
			owner = ""
		}
		decision, err := evaluator.Evaluate(current.AccessBundle, identity.AccessRequest{ObjectKey: "integration.connection_accounts", Action: "list"}, identity.ResourceFacts{"workspace_id": a.WorkspaceID, "owner_user_id": owner}, time.Now().UTC())
		if err != nil {
			return false, err
		}
		if shared {
			access.Workspace = decision.Allowed
		} else {
			access.Personal = decision.Allowed
		}
	}
	if !access.Personal && !access.Workspace {
		return false, nil
	}
	accounts, err := binding.ConnectionAccounts().ListConnectionAccounts(ctx, integration.ConnectionAccountSubject{WorkspaceID: a.WorkspaceID, UserID: a.UserID, Access: access})
	if err != nil {
		return false, err
	}
	for _, account := range accounts {
		if account.WorkspaceID != a.WorkspaceID || account.Status != "active" || account.Readiness == nil || !account.Readiness.Available {
			continue
		}
		if account.Scope == integration.ConnectionAccountScopePersonal {
			if !access.Personal || account.OwnerUserID != a.UserID {
				continue
			}
		} else if account.Scope != integration.ConnectionAccountScopeWorkspace || !access.Workspace || account.OwnerUserID != "" {
			continue
		}
		for _, required := range requirements {
			if required.ConnectorKey == "" || required.ProviderKey == "" || account.ConnectorKey != required.ConnectorKey || account.ProviderKey != required.ProviderKey {
				continue
			}
			granted := true
			for _, scope := range required.Scopes {
				granted = granted && strings.TrimSpace(scope) != "" && slices.Contains(account.Readiness.GrantedScopes, scope)
			}
			if granted {
				return true, nil
			}
		}
	}
	return false, nil
}
