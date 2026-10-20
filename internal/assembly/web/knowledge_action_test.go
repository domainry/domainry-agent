package web

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

func knowledgeTestAdapters(t *testing.T, host *Host) []modulehttp.Adapter {
	t.Helper()
	provider, ok := host.Agent.(interface{ HTTPAdapters() []modulehttp.Adapter })
	if !ok {
		t.Fatal("Agent binding does not expose module adapters")
	}
	adapters := []modulehttp.Adapter{}
	for _, adapter := range provider.HTTPAdapters() {
		if adapter.Owner() == "knowledge" {
			adapters = append(adapters, adapter)
		}
	}
	if len(adapters) == 0 {
		t.Fatal("Agent binding does not expose the Knowledge module adapter")
	}
	return adapters
}

func stripKnowledgeRolePermissions(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
	out := make([]identitysdk.ProjectRolePermission, 0, len(previous))
	for _, permission := range previous {
		if !strings.HasPrefix(permission.PermissionKey, "knowledge.") {
			out = append(out, permission)
		}
	}
	return out
}

func appendKnowledgeRolePermissions(t *testing.T, permissions []identitysdk.ProjectRolePermission, deniedAction string) []identitysdk.ProjectRolePermission {
	t.Helper()
	actions, err := knowledgesdk.AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		if action.Permission == nil || action.Key == deniedAction {
			continue
		}
		permissions = append(permissions, identitysdk.ProjectRolePermission{PermissionKey: action.Permission.Key, DataScope: identitysdk.DataScopeAll})
	}
	return permissions
}
