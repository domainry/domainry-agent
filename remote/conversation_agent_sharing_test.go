package remote

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type sharingConfigurationService struct {
	releasedResultService
	write sdk.ConversationAgentWrite
}

func (s *sharingConfigurationService) WriteConversationAgent(_ context.Context, id string, in sdk.ConversationAgentWrite, a sdk.ConversationAuthority) (sdk.ConversationAgent, error) {
	s.calls++
	s.id, s.authority, s.write = id, a, in
	if s.denied {
		return sdk.ConversationAgent{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.agent_configuration_owner_required"}
	}
	out := sdk.ConversationAgent{ID: id, OwnerUserID: a.UserID, Revision: in.ExpectedRevision + 1}
	if in.SharedWithUserIDs != nil {
		out.SharedWithUserIDs = *in.SharedWithUserIDs
	}
	return out, nil
}

func TestSaaSAgentSharingPreservesActualActorAndOmittedVersusEmptyGrants(t *testing.T) {
	source := &sharingConfigurationService{}
	svc, err := server.New(server.Config{APIKey: "sharing-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(svc.Handler())
	defer h.Close()
	opened, err := NewFactory(Options{BaseURL: h.URL, APIKey: "sharing-runtime-secret", Client: h.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	client := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationCollaborationService)
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "configuration-owner"}
	users, empty := []string{"caller"}, []string{}
	for _, sharing := range []*[]string{&users, nil, &empty} {
		in := sdk.ConversationAgentWrite{ClientID: "share-write", ExpectedRevision: 4, SharedWithUserIDs: sharing}
		out, err := client.WriteConversationAgent(t.Context(), "agent", in, a)
		if err != nil || source.authority != a || source.id != "agent" || !reflect.DeepEqual(source.write, in) || out.OwnerUserID != a.UserID || out.Revision != 5 {
			t.Fatal("SaaS changed sharing or write actor", out, err)
		}
	}
	source.denied = true
	if out, err := client.WriteConversationAgent(t.Context(), "agent", sdk.ConversationAgentWrite{}, a); err == nil || out.ID != "" {
		t.Fatal("current ownership denial lost", out, err)
	}
	before := source.calls
	a.WorkspaceID = "foreign"
	if _, err := client.WriteConversationAgent(t.Context(), "agent", sdk.ConversationAgentWrite{}, a); err == nil || before != source.calls {
		t.Fatal("foreign workspace reached configuration owner", err)
	}
}
