package web

import (
	"context"
	"fmt"
	producthttp "github.com/domainry/domainry-agent/internal/transport/http/product"
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
)

// The product opts into account management only. Connection execution, secret
// administration, signed webhooks and other Integration surfaces stay unmounted.
func accountAction(key string) bool {
	switch key {
	case integration.ActionIntegrationConnectionAccountsList, integration.ActionIntegrationConnectionAccountsGet,
		integration.ActionIntegrationConnectionAccountsTest, integration.ActionIntegrationConnectionAccountsRevoke,
		integration.ActionIntegrationOAuthApplicationsList, integration.ActionIntegrationOAuthApplicationsUpsert,
		integration.ActionIntegrationOAuthAuthorizationsOptions, integration.ActionIntegrationOAuthAuthorizationsStart,
		integration.ActionIntegrationOAuthAuthorizationsGet, integration.ActionIntegrationOAuthAuthorizationsComplete:
		return true
	}
	return false
}

// AccountSetupRoutes composes an explicit Identity administrator operation.
// Integration declares permissions; Identity retains role authoring and audit.
func (h *Host) AccountSetupRoutes() map[string]http.Handler {
	if h.Integration == nil {
		return nil
	}
	grants := []identity.ProjectRolePermission{}
	for _, route := range integration.IntegrationHTTPAdapterContract().Routes {
		if accountAction(route.Action.Key) || len(h.accountToolDefinitions) > 0 && route.Action.Key == integration.ActionIntegrationConnectionAccountsRead {
			grants = append(grants, identity.ProjectRolePermission{PermissionKey: route.Action.Permission.Key, DataScope: identity.DataScopeAll})
		}
	}
	if len(h.accountToolDefinitions) > 0 {
		for _, d := range h.accountToolDefinitions {
			grants = append(grants, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
		}
	}
	if h.hasAccountWrites() {
		grants = append(grants, identity.ProjectRolePermission{PermissionKey: integration.ConnectionAccountWritePermission().Key, DataScope: identity.DataScopeAll})
	}
	handler := producthttp.PermissionSetup(h.Identity, string(h.application.ApplicationKey), grants)
	return map[string]http.Handler{"GET /app/product/account-setup": handler, "POST /app/product/account-setup": handler}
}

type accountAdapter struct {
	modulehttp.Adapter
	routes []modulehttp.Route
}

func (a accountAdapter) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), a.routes...)
}

// IntegrationAdapters preserves the owner-provided HTTP implementation and
// permission metadata. Agent neither forwards arbitrary paths nor handles OAuth.
func (h *Host) IntegrationAdapters() ([]modulehttp.Adapter, error) {
	if h.Integration == nil {
		return nil, nil
	}
	provider, ok := h.Integration.(modulehttp.Provider)
	if !ok {
		return nil, fmt.Errorf("Integration product HTTP adapters are required")
	}
	out := []modulehttp.Adapter{}
	seen := map[string]bool{}
	for _, adapter := range provider.HTTPAdapters() {
		if adapter.Owner() != "integration" {
			return nil, fmt.Errorf("Integration adapter owner mismatch")
		}
		selected := accountAdapter{Adapter: adapter}
		for _, route := range adapter.Routes() {
			if !accountAction(route.Action.Key) {
				continue
			}
			if seen[route.Action.Key] {
				return nil, fmt.Errorf("Integration account route is duplicated")
			}
			seen[route.Action.Key] = true
			selected.routes = append(selected.routes, route)
		}
		if len(selected.routes) > 0 {
			out = append(out, selected)
		}
	}
	for _, contract := range integration.IntegrationHTTPAdapterContract().Routes {
		if accountAction(contract.Action.Key) && !seen[contract.Action.Key] {
			return nil, fmt.Errorf("Integration account route is unavailable")
		}
	}
	return out, nil
}

func (h *Host) registerIntegrationPermissions(ctx context.Context) error {
	if h.Integration == nil || h.identityBorrowed {
		return nil
	}
	adapters, err := h.IntegrationAdapters()
	if err != nil {
		return err
	}
	reader, ok := h.Identity.Permissions().(identity.PermissionSnapshotReader)
	if !ok {
		return fmt.Errorf("Identity permission snapshots are required")
	}
	const owner = "integration:work_accounts"
	previous, err := reader.CurrentSourceSnapshot(ctx, identity.PermissionSourceSnapshotRequest{Application: h.application, SourceOwner: owner})
	if err != nil {
		return err
	}
	definitions := []identity.PermissionDefinition{}
	for _, adapter := range adapters {
		for _, route := range adapter.Routes() {
			p := route.Action.Permission
			if p == nil {
				return fmt.Errorf("Integration account action permission is required")
			}
			definitions = append(definitions, identity.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: route.Action.SourceKind})
		}
	}
	// Account tool execution consumes the owner SDK. Register its permission without
	// mounting the generic provider read endpoint in the browser application.
	if len(h.accountToolDefinitions) > 0 {
		for _, route := range integration.IntegrationHTTPAdapterContract().Routes {
			if route.Action.Key == integration.ActionIntegrationConnectionAccountsRead {
				p := route.Action.Permission
				definitions = append(definitions, identity.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: route.Action.SourceKind})
			}
		}
	}
	if h.hasAccountWrites() {
		p := integration.ConnectionAccountWritePermission()
		definitions = append(definitions, identity.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: "module_host"})
	}
	request, err := identity.NewPermissionReconcileRequest(h.application, owner, previous.SnapshotHash, definitions)
	if err != nil {
		return err
	}
	_, err = h.Identity.Permissions().Reconcile(ctx, request)
	return err
}
