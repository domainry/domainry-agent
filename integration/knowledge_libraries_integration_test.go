package integration_test

import (
	"context"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type libraryTestPolicy struct{ denied atomic.Bool }

func (p *libraryTestPolicy) AuthorizeKnowledgeLibrary(_ context.Context, op string, _ agentsdk.KnowledgeLibrary, _ agentsdk.ConversationAuthority) error {
	if p.denied.Load() || (agentsdk.KnowledgeLibraryPermission(op) == nil && agentsdk.KnowledgeDocumentPermission(op) == nil) {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.library_access_denied"}
	}
	return nil
}
func (*libraryTestPolicy) ValidateKnowledgeLibraryMember(_ context.Context, user string, _ agentsdk.ConversationAuthority) error {
	if user != "second" {
		return &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.library_member_unavailable"}
	}
	return nil
}
func TestKnowledgeLibrarySaaSContractAndLiveAuthorization(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy := &libraryTestPolicy{}
	service, err := conversationassembly.NewService(repo, nil, a.RuntimeID, application.ConversationOptions{LibraryAuthorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "library-saas-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "library-saas-fixture", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	api := binding.(agentsdk.ConversationBinding).Conversations().(agentsdk.KnowledgeLibraryService)
	in := agentsdk.KnowledgeLibraryCreate{ClientID: "sdk", Kind: "shared", Name: "SDK shared library"}
	lib, err := api.CreateKnowledgeLibrary(t.Context(), in, a)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := api.CreateKnowledgeLibrary(t.Context(), in, a)
	if err != nil || replay.ID != lib.ID {
		t.Fatal("duplicate create", err)
	}
	lib, err = api.UpdateKnowledgeLibrary(t.Context(), lib.ID, agentsdk.KnowledgeLibraryUpdate{ExpectedRevision: lib.Revision, Name: "SDK renamed"}, a)
	if err != nil {
		t.Fatal(err)
	}
	lib, err = api.SetKnowledgeLibraryMember(t.Context(), lib.ID, "second", agentsdk.KnowledgeLibraryMemberWrite{ExpectedRevision: lib.Revision, Role: "reader"}, a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.UserID = "second"
	got, err := api.KnowledgeLibrary(t.Context(), lib.ID, b)
	if err != nil || got.Role != "reader" || got.Name != "SDK renamed" {
		t.Fatal("bad member read", got, err)
	}
	page, err := api.KnowledgeLibraries(t.Context(), "", 1, b)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	members, err := api.KnowledgeLibraryMembers(t.Context(), lib.ID, "", 1, a)
	if err != nil || members.Complete || members.NextAfter == "" {
		t.Fatal("bad member page", members, err)
	}
	next, err := api.KnowledgeLibraryMembers(t.Context(), lib.ID, members.NextAfter, 1, a)
	if err != nil || !next.Complete || len(next.Items) != 1 || next.Items[0].UserID == members.Items[0].UserID {
		t.Fatal("bad next page", next, err)
	}
	policy.denied.Store(true)
	if _, err = api.KnowledgeLibrary(t.Context(), lib.ID, b); err == nil {
		t.Fatal("revoked policy ignored")
	}
	policy.denied.Store(false)
	lib, err = api.RemoveKnowledgeLibraryMember(t.Context(), lib.ID, b.UserID, lib.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = api.KnowledgeLibrary(t.Context(), lib.ID, b); err == nil {
		t.Fatal("removed member retained access")
	}
	other := a
	other.RuntimeID = "forged-runtime"
	if _, err = api.KnowledgeLibraries(t.Context(), "", 10, other); err == nil {
		t.Fatal("forged runtime accepted")
	}
}
