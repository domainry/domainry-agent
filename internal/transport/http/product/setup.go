package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	identityhttp "github.com/domainry/domainry-identity-sdk/httpapi"
	"net/http"
	"strings"
	"time"
)

// SetupRoutes lets the signed-in administrator initialize product grants
// through Identity's authoring API. It preserves existing grants and never
// reapplies permissions at startup, so revocations survive restarts.
func SetupRoutes(binding identity.Binding, runtime string, authorize func(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error), key string, definitions []sdk.ConversationToolDefinition, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/product/setup" {
			next.ServeHTTP(w, r)
			return
		}
		current, ok := identity.RequestIdentityFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == "GET" {
			ready := true
			for _, d := range definitions {
				decision, e := authorize(r.Context(), sdk.ConversationToolRequest{Authority: sdk.ConversationAuthority{Known: true, RuntimeID: runtime, WorkspaceID: current.Principal.WorkspaceID, UserID: current.Principal.UserID, RoleKey: current.Principal.RoleKey}, Definition: d})
				ready = ready && e == nil && decision.Granted
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"ready": ready})
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		permissions := []identity.ProjectRolePermission{}
		keys := map[string]bool{}
		for _, p := range permissions {
			keys[p.PermissionKey] = true
		}
		add := func(k string) {
			scope := identity.DataScopeOwner
			if strings.HasPrefix(k, sdk.ConversationActionPrefix+"libraries_") || strings.HasPrefix(k, sdk.ConversationActionPrefix+"documents_") {
				scope = identity.DataScopeAll
			}
			if !keys[k] {
				keys[k] = true
				permissions = append(permissions, identity.ProjectRolePermission{PermissionKey: k, DataScope: scope})
			}
		}
		for _, d := range definitions {
			add(d.ActionKey)
		}
		for _, d := range append(sdk.PersonalConversationTools(), sdk.ArtifactConversationTools()...) {
			add(d.ActionKey)
		}
		for _, d := range sdk.KnowledgeConversationTools() {
			add(d.ActionKey)
		}
		for _, d := range sdk.AttachmentConversationTools() {
			add(d.ActionKey)
		}
		add(sdk.KnowledgeLibraryCatalogTool().ActionKey)
		add(sdk.KnowledgeExtractionTool().ActionKey)
		add(sdk.ConversationInteractionPermission().Key)
		for _, d := range sdk.ConversationHTTPDefinitions() {
			if p := sdk.ConversationAttachmentPermission(d.Operation); p != nil {
				add(p.Key)
			}
			if p := sdk.KnowledgeLibraryPermission(d.Operation); p != nil {
				add(p.Key)
			}
			if p := sdk.KnowledgeDocumentPermission(d.Operation); p != nil {
				add(p.Key)
			}
		}
		initializeAdministratorPermissions(w, r, binding, key, permissions)
	})
}

type capturedResponse struct {
	headers http.Header
	status  int
	body    bytes.Buffer
}

func (w *capturedResponse) Header() http.Header { return w.headers }
func (w *capturedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *capturedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(p)
}
func writeSetupError(w http.ResponseWriter, status int, code string) {
	if status < 400 {
		status = 500
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

// PermissionSetup is an explicit administrator action over a host-selected list.
// It accepts no client-selected permission, role, user or data scope.
func PermissionSetup(binding identity.Binding, key string, grants []identity.ProjectRolePermission) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current, ok := identity.RequestIdentityFromContext(r.Context())
		if !ok {
			writeSetupError(w, 401, "product.login_required")
			return
		}
		if r.Method == "GET" {
			app := identity.ApplicationScope{WorkspaceID: identity.WorkspaceID(current.Principal.WorkspaceID), ApplicationKey: identity.ApplicationKey(key)}
			assignments, err := binding.Projection().ListUserRoleAssignments(r.Context(), identity.UserRoleAssignmentQuery{Application: app, UserID: identity.SubjectID(current.Principal.UserID)})
			if err != nil {
				productError(w, err)
				return
			}
			admin := false
			for _, a := range assignments {
				admin = admin || a.RoleID == "admin"
			}
			resolved, err := binding.Principals().Resolve(r.Context(), identity.PrincipalResolutionRequest{Application: app, SubjectID: identity.SubjectID(current.Principal.UserID), RoleKey: current.Principal.RoleKey})
			if err != nil {
				productError(w, err)
				return
			}
			resolved.Principal.AccessBundle = &resolved.AccessBundle
			ready := true
			for _, p := range grants {
				ready = ready && resolved.Principal.HasPermission(p.PermissionKey)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"ready": ready, "administrator": admin})
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		initializeAdministratorPermissions(w, r, binding, key, grants)
	})
}

func initializeAdministratorPermissions(w http.ResponseWriter, r *http.Request, binding identity.Binding, key string, grants []identity.ProjectRolePermission) {
	current, ok := identity.RequestIdentityFromContext(r.Context())
	if !ok {
		writeSetupError(w, 401, "product.login_required")
		return
	}
	mux := http.NewServeMux()
	provider, ok := binding.(identityhttp.Provider)
	if !ok {
		writeSetupError(w, 503, "product.setup_unavailable")
		return
	}
	for _, adapter := range provider.HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	assignments, err := binding.Projection().ListUserRoleAssignments(r.Context(), identity.UserRoleAssignmentQuery{Application: identity.ApplicationScope{WorkspaceID: identity.WorkspaceID(current.Principal.WorkspaceID), ApplicationKey: identity.ApplicationKey(key)}, UserID: identity.SubjectID(current.Principal.UserID)})
	if err != nil {
		productError(w, err)
		return
	}
	admin := false
	for _, a := range assignments {
		admin = admin || a.RoleID == "admin"
	}
	if !admin {
		writeSetupError(w, 403, "product.administrator_required")
		return
	}
	call := func(method string, body []byte, hash string) *capturedResponse {
		req, err := http.NewRequestWithContext(identity.WithRequestIdentity(r.Context(), identity.RequestIdentity{}), method, "http://identity.internal/identity/roles/admin/permissions", bytes.NewReader(body))
		if err != nil {
			panic(err)
		}
		req.Header.Set("Authorization", "Bearer "+current.AccessToken)
		req.Header.Set("X-Workspace-ID", current.Principal.WorkspaceID)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Expected-Schema-Hash", hash)
		req.Header.Set("Idempotency-Key", fmt.Sprintf("product-setup-%d", time.Now().UnixNano()))
		out := &capturedResponse{headers: make(http.Header)}
		mux.ServeHTTP(out, req)
		return out
	}
	prior := call("GET", nil, "")
	if prior.status != 200 {
		writeSetupError(w, prior.status, "product.setup_denied")
		return
	}
	var permissions []identity.ProjectRolePermission
	if json.Unmarshal(prior.body.Bytes(), &permissions) != nil {
		writeSetupError(w, 500, "product.setup_unavailable")
		return
	}

	keys := map[string]bool{}
	for _, p := range permissions {
		keys[p.PermissionKey] = true
	}
	for _, p := range grants {
		if !keys[p.PermissionKey] {
			permissions = append(permissions, p)
			keys[p.PermissionKey] = true
		}
	}
	raw, _ := json.Marshal(map[string]any{"permissions": permissions, "business_reason": "Administrator enabled product workspace capabilities"})
	out := call("PUT", raw, prior.headers.Get("X-Resource-Hash"))
	if out.status != 200 {
		writeSetupError(w, out.status, "product.setup_failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ready": true})
}
