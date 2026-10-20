package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	identity "github.com/domainry/domainry-identity-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
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
			if strings.HasPrefix(k, "knowledge.") {
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
		if actions, actionErr := knowledgesdk.AuthorizationActions(); actionErr == nil {
			for _, action := range actions {
				if action.Permission != nil {
					add(action.Permission.Key)
				}
			}
		}
		for _, d := range sdk.ConversationHTTPDefinitions() {
			if p := sdk.ConversationAttachmentPermission(d.Operation); p != nil {
				add(p.Key)
			}
		}
		initializeAdministratorPermissions(w, r, binding, key, permissions)
	})
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
			ctx := requestcontext.WithWorkspaceID(r.Context(), current.Principal.WorkspaceID)
			assignments, err := binding.Projection().ListUserRoleAssignments(ctx, identity.UserRoleAssignmentQuery{UserID: identity.SubjectID(current.Principal.UserID)})
			if err != nil {
				productError(w, err)
				return
			}
			admin := false
			for _, a := range assignments {
				admin = admin || a.RoleID == "admin"
			}
			resolved, err := binding.Principals().Resolve(ctx, identity.PrincipalResolutionRequest{SubjectID: identity.SubjectID(current.Principal.UserID), RoleKey: current.Principal.RoleKey})
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
	publisher, ok := binding.(identity.ProjectRolePermissionBundlePublisher)
	if !ok {
		writeSetupError(w, 503, "product.setup_unavailable")
		return
	}
	assignments, err := binding.Projection().ListUserRoleAssignments(requestcontext.WithWorkspaceID(r.Context(), current.Principal.WorkspaceID), identity.UserRoleAssignmentQuery{UserID: identity.SubjectID(current.Principal.UserID)})
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
	_, err = publisher.PublishProjectRolePermissionBundle(r.Context(), identity.ProjectRolePermissionBundleRequest{
		Application: identity.ApplicationRef{WorkspaceID: identity.WorkspaceID(current.Principal.WorkspaceID), ApplicationKey: identity.ApplicationKey(key)},
		SourceOwner: "application:" + strings.ToLower(strings.TrimSpace(key)),
		RoleKey:     "admin", ActorID: identity.SubjectID(current.Principal.UserID), Permissions: grants,
		BusinessReason: "Administrator enabled fixed product workspace capabilities",
		OperationID:    fmt.Sprintf("product-setup-%d", time.Now().UnixNano()),
	})
	if err != nil {
		status := http.StatusInternalServerError
		var sdkError *identity.Error
		var appError *apperror.AppError
		switch {
		case errors.As(err, &sdkError) && sdkError.StatusCode > 0:
			status = sdkError.StatusCode
		case errors.As(err, &sdkError):
			status = http.StatusBadRequest
		case errors.As(err, &appError):
			switch appError.Kind {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			case apperror.KindConflict:
				status = http.StatusConflict
			case apperror.KindUnavailable:
				status = http.StatusServiceUnavailable
			}
		}
		writeSetupError(w, status, "product.setup_failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ready": true})
}
