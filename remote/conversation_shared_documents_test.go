package remote

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type sharedMessageService struct {
	releasedResultService
	message sdk.ConversationAgentMessageSend
}

func (s *sharedMessageService) SendConversationAgentMessage(_ context.Context, id string, in sdk.ConversationAgentMessageSend, a sdk.ConversationAuthority) (sdk.ConversationAgentMessage, error) {
	s.calls++
	s.id, s.message, s.authority = id, in, a
	if s.denied {
		return sdk.ConversationAgentMessage{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.collaboration_access_denied"}
	}
	return sdk.ConversationAgentMessage{ID: "message", DelegationID: id, FromUserID: a.UserID, ToAgentID: in.ToAgentID, Content: in.Content, Documents: in.Documents}, nil
}
func TestSaaSSharedDocumentsPreserveActorAndExactReference(t *testing.T) {
	source := &sharedMessageService{}
	svc, err := server.New(server.Config{APIKey: "shared-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(svc.Handler())
	defer h.Close()
	opened, err := NewFactory(Options{BaseURL: h.URL, APIKey: "shared-runtime-secret", Client: h.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	client := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationCollaborationService)
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "sender"}
	in := sdk.ConversationAgentMessageSend{ClientID: "share-once", Content: "明确资料引用", ToAgentID: "receiver", BriefVersion: 2, AgreementRevision: 4, DeliveryMode: "next_run", Documents: []sdk.ConversationDocumentReference{{LibraryID: "lib_" + strings.Repeat("a", 32), DocumentID: "kdoc_" + strings.Repeat("b", 32), Revision: 7, SHA256: strings.Repeat("c", 64)}}}
	out, err := client.SendConversationAgentMessage(t.Context(), "delegation", in, a)
	if err != nil || source.authority != a || source.id != "delegation" || !reflect.DeepEqual(source.message, in) || !reflect.DeepEqual(out.Documents, in.Documents) || out.FromUserID != a.UserID {
		t.Fatalf("SaaS changed reference or actor: %+v %v", out, err)
	}
	source.denied = true
	if out, err = client.SendConversationAgentMessage(t.Context(), "delegation", in, a); err == nil || len(out.Documents) > 0 {
		t.Fatal("current sharing denial lost", err)
	}
	before := source.calls
	a.WorkspaceID = "other"
	if _, err = client.SendConversationAgentMessage(t.Context(), "delegation", in, a); err == nil || source.calls != before {
		t.Fatal("cross-workspace request reached owner", err)
	}
}
