package application

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type sharingSubjectTestPolicy struct {
	allowCollaborationTestPolicy
	err error
}

func (p sharingSubjectTestPolicy) ValidateConversationAgentSubjects(ctx context.Context, _ sdk.ConversationAuthority, _ []string) error {
	if p.err != nil {
		return p.err
	}
	return ctx.Err()
}

func TestAgentSharingDirectoryDistinguishesDeniedMembershipFromUnavailablePolicy(t *testing.T) {
	s, repo, _, a := discoveryFixture()
	repo.agents = []sdk.ConversationAgent{{ID: "shared", Shared: true, OwnerUserID: "owner"}}
	denied := &sdk.Error{Class: "forbidden", Code: "agent.conversation.agent_sharing_subject_unavailable"}
	s.options.CollaborationAuthorizer = sharingSubjectTestPolicy{err: denied}
	page, err := s.conversationAgentDirectory(t.Context(), a)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "default" {
		t.Fatal("unavailable member retained in directory", page, err)
	}
	unavailable := errors.New("membership lookup failed")
	s.options.CollaborationAuthorizer = sharingSubjectTestPolicy{err: unavailable}
	if _, err = s.conversationAgentDirectory(t.Context(), a); !errors.Is(err, unavailable) {
		t.Fatal("transient policy failure silently presented as a complete directory", err)
	}
}

func TestAgentSnapshotBindsActualCallerWithoutTakingConfigurationOwnerAuthority(t *testing.T) {
	s, repo, _, a := discoveryFixture()
	s.options.CollaborationAuthorizer = sharingSubjectTestPolicy{}
	repo.agents = []sdk.ConversationAgent{{ID: "shared", Name: "Shared", Instructions: "Work", OwnerUserID: "configuration-owner", Shared: true, Tools: []string{"time_now"}, ModelKey: "default", Enabled: true, Revision: 1, MaxConcurrent: 1}}
	snapshot, err := s.freezeConversationAgent(t.Context(), "shared", a)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OwnerUserID != "configuration-owner" || snapshot.ExecutionSubject == nil || *snapshot.ExecutionSubject != *executionSubject(a) {
		t.Fatal("configuration owner became execution user", snapshot)
	}
	if _, err := s.selectConversationAgent(t.Context(), snapshot, a); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"user", "workspace", "runtime"} {
		changed := a
		switch field {
		case "user":
			changed.UserID = "configuration-owner"
		case "workspace":
			changed.WorkspaceID = "other"
		case "runtime":
			changed.RuntimeID = "other"
		}
		if _, err := s.selectConversationAgent(t.Context(), snapshot, changed); err == nil {
			t.Fatal("snapshot accepted another execution scope", field)
		}
	}
	legacy := *snapshot
	legacy.OwnerUserID = ""
	legacy.ExecutionSubject = nil
	legacy.Digest = ""
	legacy.Digest = conversationDigest(legacy)
	if _, err := s.selectConversationAgent(t.Context(), &legacy, a); err == nil {
		t.Fatal("shared profile acquired ambiguous legacy identity")
	}
	repo.agents[0].Shared = false
	repo.agents[0].OwnerUserID = a.UserID
	if _, err := s.selectConversationAgent(t.Context(), &legacy, a); err != nil {
		t.Fatal("existing owned snapshot lost compatibility", err)
	}
}
