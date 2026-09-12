package web

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

const knowledgePermissionResource = "agent.knowledge_documents"

func (h *Host) authorizeKnowledgeWorkspace(ctx context.Context, a agentsdk.ConversationAuthority) error {
	if !a.Known || a.RuntimeID != h.runtimeID || a.WorkspaceID == "" || a.UserID == "" {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.knowledge_access_denied"}
	}
	resolution, err := h.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return err
	}
	if !resolution.Principal.Known || resolution.Principal.UserID != a.UserID || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.knowledge_access_denied"}
	}
	return nil
}

func (h *Host) registerKnowledgePermissions(ctx context.Context, bindings map[string]string) error {
	const owner = "agent:knowledge_documents"
	if len(bindings) > 100 {
		return fmt.Errorf("at most 100 knowledge permission bindings are supported")
	}
	keyPattern := regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	h.knowledgePermissions = make(map[string]string, len(bindings))
	keys := []string{}
	for key, id := range bindings {
		if !keyPattern.MatchString(key) || strings.TrimSpace(id) == "" || len(id) > 256 || !utf8.ValidString(id) || strings.ContainsAny(id, "\x00\r\n") {
			return fmt.Errorf("invalid knowledge permission binding")
		}
		h.knowledgePermissions[key] = id
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if h.identityBorrowed {
		return nil
	}
	definitions := []identitysdk.PermissionDefinition{}
	for _, key := range keys {
		definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: knowledgePermissionResource + "." + key, ResourceKey: knowledgePermissionResource, OperationKey: key, Label: "Knowledge documents: " + key, Category: "Knowledge documents", SourceKind: "agent"})
	}
	reader, ok := h.Identity.Permissions().(identitysdk.PermissionSnapshotReader)
	if !ok {
		return fmt.Errorf("Identity permission snapshot reader is required")
	}
	previous, err := reader.CurrentSourceSnapshot(ctx, identitysdk.PermissionSourceSnapshotRequest{Application: h.application, SourceOwner: owner})
	if err != nil {
		return err
	}
	request, err := identitysdk.NewPermissionReconcileRequest(h.application, owner, previous.SnapshotHash, definitions)
	if err != nil {
		return err
	}
	_, err = h.Identity.Permissions().Reconcile(ctx, request)
	return err
}

func (h *Host) knowledgePermissionIDs(ctx context.Context, a agentsdk.ConversationAuthority) ([]string, error) {
	denied := &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.knowledge_access_denied"}
	if !a.Known || a.RuntimeID != h.runtimeID || (a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID)) || a.UserID == "" {
		return nil, denied
	}
	resolution, err := h.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(ctx, a.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return nil, err
	}
	if !resolution.Principal.Known || resolution.Principal.UserID != a.UserID || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return nil, denied
	}
	ids := []string{}
	seen := map[string]bool{}
	for key, id := range h.knowledgePermissions {
		decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: knowledgePermissionResource, Action: key}, identitysdk.ResourceFacts{"owner_user_id": a.UserID, "workspace_id": a.WorkspaceID}, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if decision.Allowed && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Strings(ids)
	return ids, nil
}
