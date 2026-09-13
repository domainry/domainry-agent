package web

import (
	"net/http"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/transport/http/product"
	identity "github.com/domainry/domainry-identity-sdk"
)

// Explicit Identity administrator setup. Never regrant revoked permissions on
// startup; this only supplies the existing role-authoring UI with fixed grants.
func (h *Host) CollaborationSetupRoutes() map[string]http.Handler {
	grants := []identity.ProjectRolePermission{}
	for _, operation := range sdk.ConversationCollaborationOperations() {
		grants = append(grants, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(operation).Key, DataScope: identity.DataScopeOwner})
	}
	handler := product.PermissionSetup(h.Identity, string(h.application.ApplicationKey), grants)
	return map[string]http.Handler{"GET /app/product/collaboration-setup": handler, "POST /app/product/collaboration-setup": handler}
}
