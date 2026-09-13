package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type executionBindingTestPolicy struct {
	sharingSubjectTestPolicy
	requests   []sdk.ConversationCollaborationAuthorizationRequest
	deniedRole string
}

func (p *executionBindingTestPolicy) AuthorizeConversationCollaboration(_ context.Context, in sdk.ConversationCollaborationAuthorizationRequest) (sdk.ConversationCollaborationAuthorization, error) {
	p.requests = append(p.requests, in)
	if in.Authority.RoleKey == p.deniedRole {
		return sdk.ConversationCollaborationAuthorization{}, nil
	}
	return sdk.ConversationCollaborationAuthorization{Allowed: in.Operations}, nil
}

type executionBindingTestHost struct{ discoveryHost }

func (h executionBindingTestHost) AuthorizeConversationTool(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{Granted: in.Authority.RoleKey == "professional-role"}, nil
}

func TestDelegationExecutionBindingUsesSelectedRoleWithoutChangingOrdinaryConversationIdentity(t *testing.T) {
	for _, sameUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "different-users", true: "same-user-different-roles"}[sameUser], func(t *testing.T) {
			s, repo, _, issuer := discoveryFixture()
			issuer.RoleKey = "issuer-role"
			owner := "professional"
			if sameUser {
				owner = issuer.UserID
			}
			policy := &executionBindingTestPolicy{deniedRole: "issuer-role"}
			s.options.CollaborationAuthorizer = policy
			repo.agents = []sdk.ConversationAgent{{ID: "bound", Name: "Professional", Instructions: "Work", Tools: []string{"calculate"}, ModelKey: "default", Enabled: true, Revision: 1, MaxConcurrent: 1, OwnerUserID: owner, Shared: !sameUser, DelegationExecution: "owner", DelegationRoleKey: "professional-role"}}
			snapshot, actual, err := s.delegationExecutionAgent(t.Context(), "bound", issuer)
			if err != nil || actual.UserID != owner || actual.RoleKey != "professional-role" || snapshot.DelegationRoleKey != "professional-role" {
				t.Fatal("receiving binding not used", snapshot, actual, err)
			}
			if _, err := s.selectConversationAgent(t.Context(), snapshot, actual); err != nil {
				t.Fatal(err)
			}
			if _, err := s.selectConversationAgent(t.Context(), snapshot, issuer); err == nil {
				t.Fatal("issuer could substitute its identity or role")
			}
			ordinary, err := s.freezeConversationAgent(t.Context(), "bound", issuer)
			if err != nil || ordinary.DelegationRoleKey != "" || ordinary.ExecutionSubject.UserID != issuer.UserID {
				t.Fatal("ordinary conversation acquired delegation role", ordinary, err)
			}
			found := false
			for _, request := range policy.requests {
				for _, op := range request.Operations {
					if op == "receive" {
						found = true
						if request.Authority != actual || request.OwnerUserID != issuer.UserID {
							t.Fatal("policy facts changed record owner or executor", request)
						}
					}
				}
			}
			if !found {
				t.Fatal("receiver permission never checked")
			}
			policy.deniedRole = "professional-role"
			if _, _, err := s.delegationExecutionAgent(t.Context(), "bound", issuer); !collaborationDenied(err) {
				t.Fatal("revoked receiver role was accepted", err)
			}
			policy.deniedRole = "issuer-role"
			repo.agents[0].DelegationRoleKey = "another-role"
			if _, err := s.selectConversationAgent(t.Context(), snapshot, actual); err == nil {
				t.Fatal("saved snapshot followed a changed execution role")
			}
		})
	}
}

func TestDelegationDiscoveryUsesReceiverToolGrants(t *testing.T) {
	s, repo, _, issuer := discoveryFixture()
	issuer.RoleKey = "issuer-role"
	s.options.CollaborationAuthorizer = &executionBindingTestPolicy{deniedRole: "unavailable-role"}
	s.options.ToolHost = executionBindingTestHost{}
	repo.agents = []sdk.ConversationAgent{{ID: "bound", Name: "Professional", Instructions: "Work", Tools: []string{"calculate"}, ModelKey: "default", Enabled: true, Revision: 1, MaxConcurrent: 1, OwnerUserID: "professional", Shared: true, DelegationExecution: "owner", DelegationRoleKey: "professional-role"}}
	page, err := s.MatchConversationAgents(t.Context(), sdk.ConversationAgentMatchRequest{Requirements: sdk.ConversationAgentRequirements{Tools: []string{"calculate"}}}, issuer)
	if err != nil || page.RecommendedAgentID != "bound" {
		t.Fatal("receiver was ranked with issuer tool grants", page, err)
	}
	repo.agents[0].DelegationRoleKey = "unavailable-role"
	page, err = s.MatchConversationAgents(t.Context(), sdk.ConversationAgentMatchRequest{}, issuer)
	if err != nil || page.RecommendedAgentID != "" {
		t.Fatal("unavailable receiver role stayed recommended", page, err)
	}
}

func TestDelegationExecutorRelationshipDoesNotGrantIssuerManagement(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "executor"}
	s := &ConversationService{runtimeID: a.RuntimeID, options: ConversationOptions{CollaborationAuthorizer: allowCollaborationTestPolicy{}}}
	d := sdk.ConversationDelegation{ID: "work", OwnerUserID: "issuer", ExecutionSubject: executionSubject(a)}
	for _, op := range []string{"view", "receive", "communicate", "execution_read", "delivery_read"} {
		if err := s.authorizeCollaboration(t.Context(), op, &d, a); err != nil {
			t.Fatal(op, err)
		}
	}
	for _, op := range []string{"manage", "share"} {
		if err := s.authorizeCollaboration(t.Context(), op, &d, a); !collaborationDenied(err) {
			t.Fatal("executor acquired issuer management", op, err)
		}
	}
}
