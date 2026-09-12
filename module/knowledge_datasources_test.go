package module

import (
	"context"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func TestKnowledgeDatasourceCatalogIsolationAndImmutableConfiguration(t *testing.T) {
	t.Setenv("TEST_DATASOURCE_API_KEY", "fixture-secret")
	raw := `[{"key":"approved","name":"知识源","workspace_id":"w","base_url":"https://knowledge.example.test","team_id":"team","kb_id":"kb","api_key_env":"TEST_DATASOURCE_API_KEY","response_mapping":{"search":{"items":"/hits","many":true,"doc_id":"/doc_id","excerpt":"/body"},"fetch":{"items":"/data","doc_id":"/doc_id","excerpt":"/body"}}}]`
	values, err := knowledgeDatasourceEnvironment(raw)
	if err != nil || len(values) != 1 || values[0].Knowledge.APIKey != "fixture-secret" {
		t.Fatal("environment reference", err)
	}
	for _, invalid := range []string{raw + `{}`, `[]`, `null`, strings.Replace(raw, `"api_key_env":"TEST_DATASOURCE_API_KEY"`, `"api_key":"secret"`, 1), strings.Replace(raw, `TEST_DATASOURCE_API_KEY`, `bad-name`, 1)} {
		if _, e := knowledgeDatasourceEnvironment(invalid); e == nil {
			t.Fatal("invalid environment accepted")
		}
	}
	var options ConversationOptions
	if err = assembleKnowledgeDatasources(&options, values, "", "r"); err != nil {
		t.Fatal(err)
	}
	// Mutating host inputs after assembly cannot change the live catalog.
	values[0].Knowledge.ResponseMapping.Fetch.Excerpt = "/wrong"
	values[0].Knowledge.APIKey = "changed"
	scope := agentsdk.KnowledgeDocumentStorageScope{RuntimeID: "r", WorkspaceID: "w", LibraryID: "lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	catalog := options.KnowledgeDatasources
	entry := catalog.(*knowledgeDatasourceCatalog).entries["approved"]
	if entry.config.Knowledge.ResponseMapping.Fetch.Excerpt != "/body" || entry.config.Knowledge.APIKey != "fixture-secret" {
		t.Fatal("host input mutation changed catalog")
	}
	definitions, err := catalog.KnowledgeDatasources(t.Context(), scope)
	if err != nil || len(definitions) != 1 || definitions[0].Key != "approved" {
		t.Fatal(err)
	}
	first, err := catalog.OpenKnowledgeDatasource(t.Context(), "approved", scope)
	if err != nil || first.KnowledgeDocumentManagementReady() != nil || first.KnowledgeDocumentMaxBytes() != 10<<20 {
		t.Fatal(err)
	}
	policy := first.KnowledgeDocumentAccessPolicySHA256()
	if policy == "" {
		t.Fatal("public upload catalog")
	}
	scope.LibraryID = "lib_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second, err := catalog.OpenKnowledgeDatasource(t.Context(), "approved", scope)
	if err != nil || second.KnowledgeDocumentSourceIdentity() != definitions[0].SourceID || second.KnowledgeDocumentAccessPolicySHA256() == policy {
		t.Fatal("library policy not isolated", err)
	}
	for _, bad := range []agentsdk.KnowledgeDocumentStorageScope{{RuntimeID: "other", WorkspaceID: "w", LibraryID: scope.LibraryID}, {RuntimeID: "r", WorkspaceID: "foreign", LibraryID: scope.LibraryID}, {RuntimeID: "r", WorkspaceID: "w", LibraryID: "forged"}} {
		if _, e := catalog.OpenKnowledgeDatasource(t.Context(), "approved", bad); e == nil {
			t.Fatal("foreign scope accepted")
		}
	}
	scope.WorkspaceID = "foreign"
	if page, e := catalog.KnowledgeDatasources(t.Context(), scope); e != nil || len(page) != 0 {
		t.Fatal("catalog leaked across workspace", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := catalog.KnowledgeDatasources(ctx, scope); e == nil {
		t.Fatal("cancellation ignored")
	}
	values, _ = knowledgeDatasourceEnvironment(raw)
	duplicate := values[0]
	duplicate.Key = "alias"
	duplicate.Knowledge.BaseURL = "https://KNOWLEDGE.example.test:443/"
	if e := assembleKnowledgeDatasources(&ConversationOptions{}, append(values, duplicate), "", "r"); e == nil {
		t.Fatal("physical KB alias allowed")
	}
	if e := assembleKnowledgeDatasources(&ConversationOptions{Knowledge: first.(*provider.Knowledge)}, values, "", "r"); e == nil {
		t.Fatal("default source alias allowed")
	}
}
