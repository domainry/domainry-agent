package integration_test

import (
	"context"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

type datasourceTestCatalog struct {
	source agentsdk.KnowledgeDatasourceSource
}

func (c datasourceTestCatalog) KnowledgeDatasources(context.Context, agentsdk.KnowledgeDocumentStorageScope) ([]agentsdk.KnowledgeDatasourceDefinition, error) {
	return []agentsdk.KnowledgeDatasourceDefinition{{Key: "approved", Name: "Shared source", SourceID: c.source.KnowledgeDocumentSourceIdentity()}}, nil
}
func (c datasourceTestCatalog) OpenKnowledgeDatasource(context.Context, string, agentsdk.KnowledgeDocumentStorageScope) (agentsdk.KnowledgeDatasourceSource, error) {
	return c.source, nil
}

func TestKnowledgeDatasourceSaaSContractAndLiveAuthorization(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	lib, err := repo.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "shared", Kind: "shared", Name: "Shared"}, a)
	if err != nil {
		t.Fatal(err)
	}
	source, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: "https://knowledge.invalid", APIKey: "fixture-host-secret", TeamID: "team", KBID: "kb", WorkspaceID: a.WorkspaceID, DocumentPermissionIDs: []string{"library:test"}, DocumentManagement: true, ResponseMapping: &provider.KnowledgeResponseMapping{Search: &provider.KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Excerpt: "/body"}, Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Excerpt: "/body"}}})
	if err != nil {
		t.Fatal(err)
	}
	files, err := knowledgemodule.NewDocumentFiles(t.TempDir() + "/private")
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	policy := &libraryTestPolicy{}
	service, err := conversationassembly.NewService(repo, &executionModel{}, a.RuntimeID, application.ConversationOptions{DocumentStorage: files, LibraryAuthorizer: policy, PersonalAuthorizer: personalReadAuthorizer{}, ToolHost: tools, KnowledgeDatasources: datasourceTestCatalog{source}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "datasource-rpc-fixture", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	rpc := httptest.NewServer(server.Handler())
	defer rpc.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: rpc.URL, APIKey: "datasource-rpc-fixture", Client: rpc.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	api := binding.(agentsdk.ConversationBinding).Conversations().(agentsdk.KnowledgeDatasourceService)
	page, err := api.KnowledgeLibrarySources(t.Context(), lib.ID, "", 1, a)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Available || !page.CanBind {
		t.Fatal("SaaS source projection", err)
	}
	in := agentsdk.KnowledgeLibrarySourceWrite{DatasourceKey: "approved", ExpectedRevision: lib.Revision}
	current, err := api.BindKnowledgeLibrarySource(t.Context(), lib.ID, in, a)
	if err != nil || current.DatasourceKey != "approved" || !current.DocumentsConfigured {
		t.Fatal("SaaS binding", err)
	}
	replayed, err := api.BindKnowledgeLibrarySource(t.Context(), lib.ID, in, a)
	if err != nil || replayed.Revision != current.Revision {
		t.Fatal("SaaS duplicate mutation", err)
	}
	page, err = api.KnowledgeLibrarySources(t.Context(), lib.ID, "", 1, a)
	if err != nil || page.Status != "connected" || page.CurrentKey != "approved" || page.Items[0].Available {
		t.Fatal("SaaS binding status", err)
	}
	other := a
	other.UserID = "foreign"
	if _, err = api.KnowledgeLibrarySources(t.Context(), lib.ID, "", 1, other); err == nil {
		t.Fatal("catalog disclosed to non-member")
	}
	if _, err = api.BindKnowledgeLibrarySource(t.Context(), lib.ID, in, other); err == nil {
		t.Fatal("binding replay bypassed membership")
	}
	policy.denied.Store(true)
	_, err = api.BindKnowledgeLibrarySource(t.Context(), lib.ID, in, a)
	artifactErrorClass(t, err, "forbidden")
	_, err = api.KnowledgeLibrarySources(t.Context(), lib.ID, "", 1, a)
	artifactErrorClass(t, err, "forbidden")
	if strings.Contains(err.Error(), "fixture-host-secret") {
		t.Fatal("credential leaked in remote error")
	}
}
