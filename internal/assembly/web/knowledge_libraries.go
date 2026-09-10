package web

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-identity-sdk/authorization/evaluator"
)

func (h *Host) AuthorizeKnowledgeLibrary(ctx context.Context, op string, library agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) error {
	denied := &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	p := agentsdk.KnowledgeLibraryPermission(op)
	if p == nil {
		p = agentsdk.KnowledgeDocumentPermission(op)
	}
	if p == nil || !a.Known || a.RuntimeID != h.runtimeID || a.UserID == "" || a.WorkspaceID == "" || !h.external && a.WorkspaceID != string(h.application.WorkspaceID) {
		return denied
	}
	resolution, err := h.Identity.Principals().Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(a.WorkspaceID), ApplicationKey: h.application.ApplicationKey}, SubjectID: identitysdk.SubjectID(a.UserID), RoleKey: a.RoleKey})
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

func (h *Host) ValidateKnowledgeLibraryMember(ctx context.Context, user string, a agentsdk.ConversationAuthority) error {
	denied := &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.library_member_unavailable"}
	if user == "" || len(user) > 255 {
		return denied
	}
	resolution, err := h.Identity.Principals().Resolve(ctx, identitysdk.PrincipalResolutionRequest{Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(a.WorkspaceID), ApplicationKey: h.application.ApplicationKey}, SubjectID: identitysdk.SubjectID(user)})
	// Do not leak whether the subject belongs to another workspace, is inactive
	// or does not exist. No account is created by a membership mutation.
	if err != nil || !resolution.Principal.Known || resolution.Principal.UserID != user || resolution.Principal.WorkspaceID != a.WorkspaceID {
		return denied
	}
	return nil
}
