package application

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestManagementReceiptUsesCurrentCanonicalGrantAndRejectsExtraContent(t *testing.T) {
	owner := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner", RoleKey: "publisher-role"}
	manager := owner
	manager.UserID, manager.RoleKey = "manager", "current-management-role"
	repo := &participantReceiptTestRepository{delegation: sdk.ConversationDelegation{ID: "work", OwnerUserID: owner.UserID, Status: "paused", Revision: 5, Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "PRIVATE-AGREEMENT"}, Participants: []sdk.ConversationDelegationParticipant{{UserID: manager.UserID, Operations: []string{"view", "manage"}, Revision: 1, Publisher: &owner}}}}
	policy := &participantManagementTestPolicy{}
	s := &ConversationService{runtimeID: owner.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: policy}}
	receipt := sdk.ConversationDelegationDetail{ManagementOnly: true, Messages: []sdk.ConversationAgentMessage{}, ConversationDelegation: sdk.ConversationDelegation{ID: "work", OwnerUserID: owner.UserID, Status: "paused", Revision: 4, Decision: "Original management receipt"}}
	audit := s.sourceAudit(manager)
	if err := audit.collaborationResult(t.Context(), receipt); err != nil {
		t.Fatal("safe original receipt could not use current canonical grant", err)
	}
	for _, field := range []string{"brief", "input", "conversation", "task", "participants"} {
		extra := receipt
		switch field {
		case "brief":
			extra.Brief = repo.delegation.Brief
		case "input":
			extra.Input = "PRIVATE-INPUT"
		case "conversation":
			extra.SourceConversationID = "private-owner-conversation"
		case "task":
			extra.Task = &sdk.ConversationTaskDetail{}
		case "participants":
			extra.Participants = repo.delegation.Participants
		}
		if err := audit.collaborationResult(t.Context(), extra); err == nil {
			t.Fatal("management_only flag released extra content", field)
		}
	}
	policy.denyManage = true
	if err := audit.collaborationResult(t.Context(), receipt); !collaborationDenied(err) {
		t.Fatal("same audit cached management permission after current role withdrawal", err)
	}
	policy.denyManage = false
	repo.delegation.Participants[0].Operations = []string{"view"}
	if err := audit.collaborationResult(t.Context(), receipt); !collaborationDenied(err) {
		t.Fatal("same audit reread a management receipt after grant reduction", err)
	}
}
