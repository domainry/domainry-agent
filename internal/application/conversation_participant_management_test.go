package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type participantManagementTestRepository struct {
	participantReceiptTestRepository
}

func TestExecutorCanReceiveExplicitManagementWithoutLosingAdmittedAccessOnRevocation(t *testing.T) {
	owner := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner", RoleKey: "granting-role"}
	executor := owner
	executor.UserID, executor.RoleKey = "professional", "execution-role"
	d := sdk.ConversationDelegation{ID: "work", OwnerUserID: owner.UserID, ExecutionSubject: executionSubject(executor), Participants: []sdk.ConversationDelegationParticipant{{UserID: executor.UserID, Operations: []string{"view", "manage"}, Revision: 1, Publisher: &owner}}}
	policy := &executionBindingTestPolicy{deniedRole: "withdrawn"}
	s := &ConversationService{runtimeID: executor.RuntimeID, options: ConversationOptions{CollaborationAuthorizer: policy}}
	if err := s.authorizeCollaboration(t.Context(), "manage", &d, executor); err != nil {
		t.Fatal("explicit management still excluded the receiving peer", err)
	}
	policy.deniedRole = owner.RoleKey
	if err := s.authorizeCollaboration(t.Context(), "manage", &d, executor); !collaborationDenied(err) {
		t.Fatal("withdrawn management publisher still empowered the receiver", err)
	}
	if access, err := s.collaborationAccess(t.Context(), d, executor); err != nil || access.Manage || !access.View || !access.Receive || !access.ExecutionRead {
		t.Fatal("management withdrawal removed independent admitted access", access, err)
	}
}

type participantManagementTestPolicy struct {
	sharingSubjectTestPolicy
	denyManage bool
}

func (p *participantManagementTestPolicy) AuthorizeConversationCollaboration(_ context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	out := sdk.ConversationCollaborationAuthorization{}
	for _, op := range in.Operations {
		if op != "manage" || !p.denyManage {
			out.Allowed = append(out.Allowed, op)
		}
	}
	return out, nil
}

func (r *participantManagementTestRepository) UpdateConversationDelegation(_ context.Context, _ string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	r.actor, r.writes = a, r.writes+1
	r.delegation.Status = "paused"
	r.delegation.Decision = in.Reason
	r.delegation.Revision++
	return r.delegation, nil
}

func TestParticipantManagementReturnsOnlyReceiptAndChecksCurrentRoleAndGrant(t *testing.T) {
	owner := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner", RoleKey: "granting-role"}
	actor := owner
	actor.UserID, actor.RoleKey = "participant", "managing-role"
	repo := &participantManagementTestRepository{participantReceiptTestRepository: participantReceiptTestRepository{delegation: sdk.ConversationDelegation{
		ID: "work", OwnerUserID: owner.UserID, Revision: 4, Status: "running", TaskID: "private-task", SourceConversationID: "private-source", ConversationID: "private-execution",
		Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "PRIVATE-SOURCE-CONTENT"}, BriefSource: &sdk.ConversationRunReference{ConversationID: "private", RunID: "withdrawn"},
		Participants: []sdk.ConversationDelegationParticipant{{UserID: actor.UserID, Operations: []string{"view", "manage"}, Revision: 1, Publisher: &owner}},
	}}}
	policy := &participantManagementTestPolicy{}
	s := &ConversationService{runtimeID: "runtime", repo: repo, options: ConversationOptions{CollaborationAuthorizer: policy}}
	in := sdk.ConversationDelegationUpdate{ClientID: "pause", ExpectedRevision: 4, Action: "pause", Reason: "Pause shared task"}
	result, err := s.UpdateConversationDelegation(t.Context(), "work", in, actor)
	if err != nil || !result.ManagementOnly || result.Status != "paused" || result.Revision != 5 || repo.writes != 1 || repo.actor != actor || result.Task != nil || result.Delivery != nil || result.SourceConversationID != "" || result.ConversationID != "" || result.TaskID != "" || result.BriefSource != nil || len(result.Participants) != 0 {
		t.Fatal("management borrowed private ownership or depended on source reading", result, repo.actor, err)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "PRIVATE-SOURCE-CONTENT") {
		t.Fatal("management receipt exposed private content", string(raw))
	}
	policy.denyManage = true
	if _, err := s.UpdateConversationDelegation(t.Context(), "work", in, actor); !collaborationDenied(err) || repo.writes != 1 {
		t.Fatal("withdrawn current manage permission still modified the task", err, repo.writes)
	}
	policy.denyManage = false
	repo.delegation.Participants[0].Operations = []string{"view"}
	if _, err := s.UpdateConversationDelegation(t.Context(), "work", in, actor); !collaborationDenied(err) || repo.writes != 1 {
		t.Fatal("view-only grant inherited global manage permission", err, repo.writes)
	}
}
