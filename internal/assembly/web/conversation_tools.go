package web

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

func (h *Host) registerConversationToolPermissions(ctx context.Context) error {
	const owner = "agent:conversation_tools"
	reader, ok := h.Identity.Permissions().(identitysdk.PermissionSnapshotReader)
	if !ok {
		return fmt.Errorf("Identity permission snapshot reader is required")
	}
	previous, err := reader.CurrentSourceSnapshot(ctx, identitysdk.PermissionSourceSnapshotRequest{Application: h.application, SourceOwner: owner})
	if err != nil {
		return err
	}
	definitions := []identitysdk.PermissionDefinition{}
	for _, action := range agentsdk.ConversationToolActions() {
		permission := action.Permission
		definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, OperationKey: permission.OperationKey, Label: permission.Label, Category: permission.Category, SourceKind: action.SourceKind})
	}
	permission := agentsdk.ConversationInteractionPermission()
	definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, OperationKey: permission.OperationKey, Label: permission.Label, Category: permission.Category, SourceKind: "agent"})
	for _, operation := range agentsdk.ConversationHTTPDefinitions() {
		if p := agentsdk.KnowledgeLibraryPermission(operation.Operation); p != nil {
			definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: "agent"})
		}
		if p := agentsdk.KnowledgeDocumentPermission(operation.Operation); p != nil {
			definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: p.Key, ResourceKey: p.ResourceKey, OperationKey: p.OperationKey, Label: p.Label, Category: p.Category, SourceKind: "agent"})
		}
		if permission := agentsdk.ConversationAttachmentPermission(operation.Operation); permission != nil {
			definitions = append(definitions, identitysdk.PermissionDefinition{PermissionKey: permission.Key, ResourceKey: permission.ResourceKey, OperationKey: permission.OperationKey, Label: permission.Label, Category: permission.Category, SourceKind: "agent"})
		}
	}
	request, err := identitysdk.NewPermissionReconcileRequest(h.application, owner, previous.SnapshotHash, definitions)
	if err != nil {
		return err
	}
	_, err = h.Identity.Permissions().Reconcile(ctx, request)
	return err
}

func (h *Host) AuthorizeConversationAttachment(ctx context.Context, operation string, a agentsdk.ConversationAuthority) error {
	permission := agentsdk.ConversationAttachmentPermission(operation)
	if permission == nil {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.attachment_access_denied"}
	}
	decision, err := h.authorizeConversationAction(ctx, a, permission.Key)
	if err != nil {
		return err
	}
	if !decision.Granted || decision.ConfirmationRequired {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.attachment_access_denied"}
	}
	return nil
}

func (h *Host) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	var out agentsdk.ConversationToolAuthorization
	a := in.Authority
	if !a.Known || a.RuntimeID != h.runtimeID || (a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID)) || a.UserID == "" {
		return out, nil
	}
	registered := false
	definitions := append(agentsdk.PersonalConversationTools(), agentsdk.KnowledgeConversationTools()...)
	definitions = append(definitions, agentsdk.KnowledgeLibraryCatalogTool())
	definitions = append(definitions, agentsdk.BusinessConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessActionConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessWorkflowConversationTools()...)
	definitions = append(definitions, agentsdk.BusinessRelationConversationTools()...)
	for _, definition := range append(definitions, agentsdk.ArtifactConversationTools()...) {
		if definition.Key == in.Definition.Key && definition.ActionKey == in.Definition.ActionKey {
			registered = true
			break
		}
	}
	if !registered {
		return out, nil
	}
	return h.authorizeConversationAction(ctx, a, in.Definition.ActionKey)
}

func (h *Host) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, interaction agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return h.authorizeConversationAction(ctx, a, agentsdk.ConversationInteractionPermission().Key)
}

func (h *Host) authorizeConversationAction(ctx context.Context, a agentsdk.ConversationAuthority, actionKey string) (agentsdk.ConversationToolAuthorization, error) {
	var out agentsdk.ConversationToolAuthorization
	if !a.Known || a.RuntimeID != h.runtimeID || (a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID)) || a.UserID == "" {
		return out, nil
	}
	// Resolve the current subject for trusted background work. Never reuse the
	// cookie, token or policy bundle captured when the message was submitted.
	resolution, err := h.Identity.Principals().Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(a.WorkspaceID), ApplicationKey: h.application.ApplicationKey}, SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
	if err != nil {
		return out, err
	}
	if !resolution.Principal.Known || resolution.Principal.UserID != a.UserID || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return out, nil
	}
	separator := strings.LastIndexByte(actionKey, '.')
	if separator < 1 {
		return out, nil
	}
	// Personal resources are always owner-scoped by the Agent repository. A
	// host business adapter must load its own facts instead of accepting these
	// from model-supplied arguments.
	decision, err := evaluator.Evaluate(resolution.AccessBundle, identitysdk.AccessRequest{ObjectKey: actionKey[:separator], Action: actionKey[separator+1:]}, identitysdk.ResourceFacts{"owner_user_id": a.UserID, "workspace_id": a.WorkspaceID}, time.Now().UTC())
	if err != nil {
		return out, err
	}
	out.Granted = decision.Allowed
	out.UserTimezone = resolution.Principal.User.Timezone
	out.Revision = string(resolution.AccessBundle.AuthorizationRevision)
	out.Evidence = map[string]any{"source": "identity", "action": actionKey, "decision": decision.Code}
	return out, nil
}
