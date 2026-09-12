package web

import (
	"context"
	"fmt"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	producthttp "github.com/domainry/domainry-agent/internal/transport/http/product"
	"github.com/domainry/domainry-foundation/modulehttp"
	identity "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-orm/query"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolhost "github.com/domainry/domainry-tools-sdk/modulehost"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func (h *Host) bindToolSettings(ctx context.Context, catalog toolsdk.Catalog, previous toolsdk.Availability) (toolsdk.Availability, error) {
	if h.ToolSettings != nil {
		return nil, fmt.Errorf("Tools preferences are already bound")
	}
	renderer, ok := h.registrar.Renderer.(query.Renderer)
	if !ok {
		return nil, fmt.Errorf("Tools preferences require the host ORM renderer")
	}
	known := map[string]bool{}
	defs := append(agentsdk.PersonalConversationTools(), agentsdk.ArtifactConversationTools()...)
	defs = append(defs, agentsdk.KnowledgeConversationTools()...)
	defs = append(defs, agentsdk.AttachmentConversationTools()...)
	defs = append(defs, agentsdk.KnowledgeLibraryCatalogTool(), agentsdk.KnowledgeExtractionTool())
	defs = append(defs, agentsdk.BusinessConversationTools()...)
	defs = append(defs, agentsdk.BusinessActionConversationTools()...)
	defs = append(defs, agentsdk.BusinessWorkflowConversationTools()...)
	defs = append(defs, agentsdk.BusinessRelationConversationTools()...)
	defs = append(defs, h.toolDefinitions...)
	for _, d := range defs {
		known[d.Key] = true
	}
	connections := &selectedToolConnections{host: h, catalog: catalog, previous: previous, known: known}
	binding, err := toolmodule.OpenSettings(ctx, toolhost.Persistence{Database: h.db, Renderer: renderer, Profile: h.profile, Migrations: h.registrar}, catalog, connections)
	if err != nil {
		return nil, err
	}
	if err = h.registerToolSettingsPermissions(ctx); err != nil {
		return nil, err
	}
	h.ToolSettings = binding
	return binding.Availability(), nil
}

// This is deployment composition over the unfiltered engine catalog. Built-in
// and declared product tools need no account by default; source-specific live
// policies are retained. Undeclared extensions must occur in the current source
// catalog, never in a model-supplied name or persisted preference alone.
type selectedToolConnections struct {
	host     *Host
	catalog  toolsdk.Catalog
	previous toolsdk.Availability
	known    map[string]bool
}

func (p *selectedToolConnections) ToolConnectionAvailable(ctx context.Context, a toolsdk.Authority, key string) (bool, error) {
	if !a.Known || a.RuntimeID != p.host.runtimeID || a.WorkspaceID == "" || a.UserID == "" || !p.host.external && a.WorkspaceID != string(p.host.application.WorkspaceID) {
		return false, nil
	}
	if !p.known[key] {
		defs, err := p.catalog.ConversationTools(ctx, a)
		if err != nil {
			return false, err
		}
		found := false
		for _, d := range defs {
			found = found || d.Key == key
		}
		if !found {
			return false, nil
		}
	}
	if p.previous != nil {
		available, err := p.previous.ConversationToolAvailable(ctx, a, key)
		if err != nil || !available {
			return false, err
		}
	}
	if available, selected, err := p.host.accountToolAvailable(ctx, a, key); selected && (err != nil || !available) {
		return false, err
	}
	if key == "report_query" && p.host.reportToolAvailability != nil {
		if ready, err := p.host.reportToolAvailability.ConversationToolAvailable(ctx, a, key); err != nil || !ready {
			return false, err
		}
	}
	if key == "analysis_run" && p.host.analysisToolAvailability != nil {
		if ready, err := p.host.analysisToolAvailability.ConversationToolAvailable(ctx, a, key); err != nil || !ready {
			return false, err
		}
	}
	return p.host.toolAccountAvailable(ctx, a, key)
}
func (h *Host) registerToolSettingsPermissions(ctx context.Context) error {
	if h.identityBorrowed {
		return nil
	}
	reader, ok := h.Identity.Permissions().(identity.PermissionSnapshotReader)
	if !ok {
		return fmt.Errorf("Identity permission snapshot reader is required")
	}
	const owner = "tools:user_preferences"
	prior, err := reader.CurrentSourceSnapshot(ctx, identity.PermissionSourceSnapshotRequest{Application: h.application, SourceOwner: owner})
	if err != nil {
		return err
	}
	defs := []identity.PermissionDefinition{}
	for _, route := range toolsdk.ToolSettingsRoutes() {
		p := route.Action.Permission
		defs = append(defs, identity.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: route.Action.SourceKind})
	}
	request, err := identity.NewPermissionReconcileRequest(h.application, owner, prior.SnapshotHash, defs)
	if err != nil {
		return err
	}
	_, err = h.Identity.Permissions().Reconcile(ctx, request)
	return err
}
func (h *Host) ToolSettingsAdapters() ([]modulehttp.Adapter, error) {
	if h.ToolSettings == nil {
		return nil, nil
	}
	adapter, err := toolmodule.SettingsHTTPAdapter(h.ToolSettings.Settings(), h.runtimeID)
	if err != nil {
		return nil, err
	}
	return []modulehttp.Adapter{adapter}, nil
}
func (h *Host) ToolSettingsSetupRoutes() map[string]http.Handler {
	if h.ToolSettings == nil {
		return nil
	}
	grants := []identity.ProjectRolePermission{}
	for _, route := range toolsdk.ToolSettingsRoutes() {
		grants = append(grants, identity.ProjectRolePermission{PermissionKey: route.Action.Permission.Key, DataScope: identity.DataScopeOwner})
	}
	handler := producthttp.PermissionSetup(h.Identity, string(h.application.ApplicationKey), grants)
	return map[string]http.Handler{"GET /app/product/tool-settings-setup": handler, "POST /app/product/tool-settings-setup": handler}
}
