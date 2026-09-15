package remote

import (
	"context"
	"net/http/httptest"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/server"
)

type remotePublicationService struct {
	releasedResultService
	publication sdk.ConversationDeliveryPublicationRequest
	before      int64
	actor       sdk.ConversationAuthority
	id          string
	denied      bool
}

func (s *remotePublicationService) PreviewConversationDeliveryPublication(_ context.Context, id string, in sdk.ConversationDeliveryPublicationRequest, a sdk.ConversationAuthority) (sdk.ConversationDeliveryPublicationPreview, error) {
	s.publication, s.actor, s.id = in, a, id
	if s.denied {
		return sdk.ConversationDeliveryPublicationPreview{}, &sdk.Error{Class: "forbidden", Code: "agent.conversation.source_access_denied"}
	}
	return sdk.ConversationDeliveryPublicationPreview{Record: sdk.ConversationDeliveryRecord{Revision: in.DeliveryRevision}, ExpectedRevision: 8, RecordDigest: "original-canonical-record", Publisher: a, RecipientUserID: "issuer"}, nil
}
func (s *remotePublicationService) ConversationDeliveryPublicationCandidates(_ context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryPublicationCandidates, error) {
	s.before, s.actor, s.id = before, a, id
	return sdk.ConversationDeliveryPublicationCandidates{Items: []sdk.ConversationDeliveryPublicationCandidate{{Revision: 7, Kind: "accept_delivery"}}, Complete: true}, nil
}

func TestSaaSDeliveryPublicationForwardsSelectedHistoryAndActualAuthority(t *testing.T) {
	source := &remotePublicationService{}
	svc, err := server.New(server.Config{APIKey: "publication-runtime-secret", Conversations: source, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(svc.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "publication-runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), sdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	reader, ok := opened.(sdk.ConversationBinding).Conversations().(sdk.ConversationDeliveryPublicationReader)
	if !ok {
		t.Fatal("SaaS binding omitted publication preparation")
	}
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "executor", RoleKey: "selected-publisher"}
	in := sdk.ConversationDeliveryPublicationRequest{DeliveryRevision: 7}
	preview, err := reader.PreviewConversationDeliveryPublication(t.Context(), "delegation", in, a)
	if err != nil || preview.Record.Revision != 7 || preview.Publisher != a || source.publication != in || source.actor != a || source.id != "delegation" {
		t.Fatal("preview lost selected history or actual caller", preview, err)
	}
	index, err := reader.ConversationDeliveryPublicationCandidates(t.Context(), "delegation", 8, a)
	if err != nil || len(index.Items) != 1 || source.before != 8 || source.actor != a {
		t.Fatal("history index forwarding", index, err)
	}
	source.denied = true
	if out, err := reader.PreviewConversationDeliveryPublication(t.Context(), "delegation", in, a); err == nil || out.RecordDigest != "" || out.Record.Delivery.Summary != "" {
		t.Fatal("remote source denial returned preview bytes", out, err)
	}
}
