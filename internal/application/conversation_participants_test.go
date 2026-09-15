package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type participantReceiptTestRepository struct {
	persistence.ConversationRepository
	persistence.ConversationCollaborationRepository
	persistence.ConversationDelegationParticipantRepository
	delegation sdk.ConversationDelegation
	actor      sdk.ConversationAuthority
	writes     int
}

func (r *participantReceiptTestRepository) ConversationDelegation(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	return r.delegation, nil
}

func (r *participantReceiptTestRepository) SetConversationDelegationParticipants(_ context.Context, _ string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegation, error) {
	r.actor, r.writes = a, r.writes+1
	r.delegation.Participants = nil
	r.delegation.Revision++
	r.delegation.ParticipantsRevision++
	return r.delegation, nil
}

func (r *participantReceiptTestRepository) Get(context.Context, string, sdk.ConversationAuthority) (sdk.Conversation, error) {
	return sdk.Conversation{}, conversationFailure("forbidden", "private_source_withdrawn")
}

func TestExitedSubjectDelegationRemainsMetadataWithoutReadingDeletedRecords(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner"}
	d := sdk.ConversationDelegation{ID: "exited", SubjectExited: true, OwnerUserID: a.UserID, Status: "cancelled", Revision: 5, Brief: sdk.ConversationTaskBrief{Goal: "ERASED-CONTENT"}, TaskID: "erased-task", ConversationID: "erased-execution", Delivery: &sdk.ConversationDelegationDelivery{Summary: "ERASED-DELIVERY"}}
	repo := &participantReceiptTestRepository{delegation: d}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: sharingSubjectTestPolicy{}}}
	out, err := s.projectConversationDelegation(t.Context(), d, a)
	if err != nil || !out.SubjectExited || !out.ContractOmitted || out.Status != "cancelled" || out.Access == nil || !out.Access.View || out.Access.Receive || out.Access.ExecutionRead || out.Access.DeliveryRead || out.TaskID != "" || out.Task != nil || out.Delivery != nil || len(out.Messages) != 0 {
		t.Fatal("erased subject broke safe current metadata", out, err)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "ERASED-") {
		t.Fatal("erased source-derived payload survived", string(raw))
	}
}

func TestDelegationParticipantRevocationReturnsReceiptWithoutReadingWithdrawnSources(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner"}
	repo := &participantReceiptTestRepository{delegation: sdk.ConversationDelegation{
		ID: "work", OwnerUserID: "owner", Revision: 4, ParticipantsRevision: 1,
		Brief:        sdk.ConversationTaskBrief{Version: 1, Goal: "PRIVATE-SOURCE-CONTENT"},
		BriefSource:  &sdk.ConversationRunReference{ConversationID: "private", RunID: "withdrawn"},
		Participants: []sdk.ConversationDelegationParticipant{{UserID: "participant", Operations: []string{"view", "communicate"}, Revision: 1}},
	}}
	s := &ConversationService{runtimeID: "runtime", repo: repo, options: ConversationOptions{CollaborationAuthorizer: sharingSubjectTestPolicy{}}}
	empty := []sdk.ConversationDelegationParticipantInput{}
	in := sdk.ConversationDelegationUpdate{ClientID: "remove-members", ExpectedRevision: 4, Action: "set_participants", Reason: "End participation", Participants: &empty}
	out, err := s.UpdateConversationDelegation(t.Context(), "work", in, a)
	if err != nil || !out.ParticipantsOnly || out.ID != "work" || out.Revision != 5 || out.ParticipantsRevision != 2 || len(out.Participants) != 0 || out.MessagesComplete || repo.writes != 1 || repo.actor != a {
		t.Fatal("revocation depended on private content or lost its receipt", out, err)
	}
	raw, err := json.Marshal(out)
	if err != nil || strings.Contains(string(raw), "PRIVATE-SOURCE-CONTENT") || out.BriefSource != nil || out.Task != nil || out.SourceAgent != nil {
		t.Fatal("membership receipt exposed source content", string(raw), err)
	}
}

type participantRoleTestPolicy struct {
	sharingSubjectTestPolicy
	request sdk.ConversationCollaborationAuthorizationRequest
	allow   bool
}

func (p *participantRoleTestPolicy) AuthorizeConversationCollaboration(_ context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	p.request = in
	if p.allow {
		return sdk.ConversationCollaborationAuthorization{Allowed: append([]string{}, in.Operations...)}, nil
	}
	return sdk.ConversationCollaborationAuthorization{}, nil
}

func TestDelegationParticipantIntakeKeepsTheSendersSelectedRole(t *testing.T) {
	receiver := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "owner", RoleKey: "executor-role"}
	repo := &participantReceiptTestRepository{delegation: sdk.ConversationDelegation{ID: "work", OwnerUserID: "owner", Participants: []sdk.ConversationDelegationParticipant{{UserID: "participant", Operations: []string{"view", "communicate"}, Revision: 3, Publisher: &receiver}}}}
	policy := &participantRoleTestPolicy{allow: true}
	s := &ConversationService{runtimeID: "runtime", repo: repo, options: ConversationOptions{CollaborationAuthorizer: policy}}
	m := sdk.ConversationAgentMessage{DelegationID: "work", FromUserID: "participant", ParticipantUserID: "participant", ParticipantRoleKey: "reviewer-role", ParticipantRevision: 3}
	if err := s.authorizePendingParticipantMessage(t.Context(), m, receiver); err != nil {
		t.Fatal(err)
	}
	expected := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "participant", RoleKey: "reviewer-role"}
	if policy.request.Authority != expected || policy.request.OwnerUserID != "owner" || policy.request.DelegationID != "work" {
		t.Fatal("intake substituted execution role or record owner", policy.request)
	}
	policy.allow = false
	if err := s.authorizePendingParticipantMessage(t.Context(), m, receiver); !collaborationDenied(err) {
		t.Fatal("current role withdrawal did not reject pending message", err)
	}
}

func TestDelegationBoundSenderIntakeRechecksItsOwnRole(t *testing.T) {
	receiver := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "receiver", RoleKey: "professional-role"}
	repo := &participantReceiptTestRepository{delegation: sdk.ConversationDelegation{ID: "work", OwnerUserID: "issuer", ExecutionSubject: executionSubject(receiver)}}
	policy := &participantRoleTestPolicy{allow: true}
	s := &ConversationService{runtimeID: "runtime", repo: repo, options: ConversationOptions{CollaborationAuthorizer: policy}}
	m := sdk.ConversationAgentMessage{DelegationID: "work", FromUserID: "issuer", SenderUserID: "issuer", SenderRoleKey: "issuer-role"}
	if err := s.authorizePendingParticipantMessage(t.Context(), m, receiver); err != nil {
		t.Fatal(err)
	}
	if policy.request.Authority.UserID != "issuer" || policy.request.Authority.RoleKey != "issuer-role" || policy.request.OwnerUserID != "issuer" {
		t.Fatal("bound message borrowed execution authority", policy.request)
	}
	policy.allow = false
	if err := s.authorizePendingParticipantMessage(t.Context(), m, receiver); !collaborationDenied(err) {
		t.Fatal("withdrawn sender role accepted", err)
	}
}
