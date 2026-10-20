package web

import (
	"context"
	"sort"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

func (h *Host) AuthorizeKnowledgeLibrary(ctx context.Context, op string, library agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) error {
	denied := &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	action, found, actionErr := knowledgesdk.AuthorizationAction(op)
	if actionErr != nil {
		return actionErr
	}
	p := action.Permission
	if !found || p == nil || !a.Known || a.RuntimeID != h.runtimeID || a.UserID == "" || a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID) {
		return denied
	}
	resolution, err := h.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return err
	}
	if !resolution.Principal.Known || resolution.Principal.UserID != a.UserID || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return denied
	}
	facts := identitysdk.ResourceFacts{"workspace_id": a.WorkspaceID, "library_id": library.ID, "library_kind": library.Kind, "library_role": library.Role, "owner_user_id": library.OwnerUserID}
	decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: p.ResourceKey, Action: p.OperationKey}, facts, time.Now().UTC())
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return denied
	}
	return nil
}

func (h *Host) SearchKnowledgePrincipals(ctx context.Context, query, after string, limit int, a agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePrincipalPage, error) {
	if !a.Known || a.RuntimeID != h.runtimeID || a.UserID == "" || a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID) {
		return knowledgesdk.KnowledgePrincipalPage{}, &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		return knowledgesdk.KnowledgePrincipalPage{}, &agentsdk.Error{Class: "bad_request", Code: "knowledge.principal_limit_invalid"}
	}
	users, err := h.Identity.Projection().ListUsers(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.ProjectionQuery{})
	if err != nil {
		return knowledgesdk.KnowledgePrincipalPage{}, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	items := make([]knowledgesdk.KnowledgePrincipal, 0, len(users))
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive || user.ID <= after {
			continue
		}
		haystack := strings.ToLower(user.ID + "\n" + user.Name + "\n" + user.Email)
		if needle != "" && !strings.Contains(haystack, needle) {
			continue
		}
		items = append(items, knowledgesdk.KnowledgePrincipal{UserID: user.ID, DisplayName: user.Name, Email: user.Email, Status: user.Status})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UserID < items[j].UserID })
	result := knowledgesdk.KnowledgePrincipalPage{Items: items, Complete: true}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextAfter = result.Items[len(result.Items)-1].UserID
		result.Complete = false
	}
	return result, nil
}

func (h *Host) ResolveKnowledgePrincipals(ctx context.Context, userIDs []string, a agentsdk.ConversationAuthority) ([]knowledgesdk.KnowledgePrincipal, error) {
	if len(userIDs) > 100 {
		return nil, &agentsdk.Error{Class: "bad_request", Code: "knowledge.principal_limit_invalid"}
	}
	if !a.Known || a.RuntimeID != h.runtimeID || a.UserID == "" || a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID) {
		return nil, &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	result := make([]knowledgesdk.KnowledgePrincipal, 0, len(userIDs))
	for _, userID := range userIDs {
		user, found, err := h.Identity.Projection().FindUser(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)})
		if err != nil {
			return nil, err
		}
		if !found || user.Status != identitysdk.UserStatusActive {
			result = append(result, knowledgesdk.KnowledgePrincipal{UserID: userID, DisplayName: "账号不可用", Status: "unavailable"})
			continue
		}
		result = append(result, knowledgesdk.KnowledgePrincipal{UserID: user.ID, DisplayName: user.Name, Email: user.Email, Status: user.Status})
	}
	return result, nil
}

func (h *Host) ExplainKnowledgeAccess(ctx context.Context, input knowledgesdk.KnowledgeAccessExplainRequest, library agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) (knowledgesdk.KnowledgeAccessExplanation, error) {
	result := knowledgesdk.KnowledgeAccessExplanation{UserID: input.UserID, LibraryID: library.ID, PageID: input.PageID, Action: input.Action}
	action, found, err := knowledgesdk.AuthorizationAction(input.Action)
	if err != nil {
		return result, err
	}
	if !found || action.Permission == nil || strings.TrimSpace(input.UserID) == "" {
		return result, &agentsdk.Error{Class: "bad_request", Code: "knowledge.access_explain_invalid"}
	}
	resolution, err := h.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(input.UserID)})
	if err != nil {
		return result, err
	}
	if !resolution.Principal.Known || resolution.Principal.WorkspaceID != a.WorkspaceID {
		result.Identity = knowledgesdk.KnowledgeAccessLayer{Allowed: false, Code: "identity_principal_unavailable", Label: "Identity 功能权限"}
		return result, nil
	}
	permission := action.Permission
	facts := identitysdk.ResourceFacts{"workspace_id": a.WorkspaceID, "library_id": library.ID, "library_kind": library.Kind, "library_role": library.Role, "owner_user_id": library.OwnerUserID}
	decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: permission.ResourceKey, Action: permission.OperationKey}, facts, time.Now().UTC())
	if err != nil {
		return result, err
	}
	result.Identity = knowledgesdk.KnowledgeAccessLayer{Allowed: decision.Allowed, Code: decision.Code, Label: "Identity 功能权限", Source: decision.PolicyKey}
	return result, nil
}

func (h *Host) ValidateKnowledgeLibraryMember(ctx context.Context, user string, a agentsdk.ConversationAuthority) error {
	denied := &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.library_member_unavailable"}
	if user == "" || len(user) > 255 {
		return denied
	}
	resolution, err := h.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(user)})
	// Do not leak whether the subject belongs to another workspace, is inactive
	// or does not exist. No account is created by a membership mutation.
	if err != nil || !resolution.Principal.Known || resolution.Principal.UserID != user || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return denied
	}
	return nil
}
