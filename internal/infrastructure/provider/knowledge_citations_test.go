package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-connectors/providers/knowledge_base/http_api"
)

func citationFixtureMapping() *KnowledgeResponseMapping {
	return &KnowledgeResponseMapping{Search: &KnowledgeCitationMapping{Items: "/hits", Many: true, DocumentID: "/doc_id", Title: "/title", URL: "/url", Excerpt: "/snippet"}, Fetch: &KnowledgeCitationMapping{DocumentID: "/doc_id", Title: "/title", URL: "/url", Excerpt: "/body"}}
}

func TestKnowledgeFetchChunkCitationsUseVerifiedDocumentMetadata(t *testing.T) {
	mapping, err := knowledgeMappingJSON(`{"fetch":{"items":"/data/chunks","many":true,"metadata_object":"/data","doc_id":"/doc_id","title":"/title","url":"/source/source_url","excerpt":"/content"}}`)
	if err != nil {
		t.Fatal(err)
	}
	k := &Knowledge{config: KnowledgeConfig{KBID: "kb", WorkspaceID: "workspace", ResponseMapping: mapping}}
	// Field layout observed on the real API push document, with synthetic IDs
	// and a non-public storage URL. The two excerpts must remain separate.
	raw := `{"err_code":0,"data":{"doc_id":"doc-1","title":"验收指南.md","status":"INDEXED","doc_version":1,"source":{"source_url":"s3://private-storage/content.md"},"chunks":[{"record_id":"part-1","content":"收到发票后30日付款。"},{"record_id":"part-2","content":"费用合计200.00元。"}]}}`
	read := func(body, id string) (agentsdk.ConversationKnowledgeResult, error) {
		envelope, _ := json.Marshal(httpapi.Output{Provider: httpapi.ProviderKey, KBID: "kb", Result: json.RawMessage(body)})
		return k.knowledgeEvidence(envelope, nil, "fetch", "", id, knowledgeAuthority())
	}
	result, err := read(raw, "doc-1")
	if err != nil || len(result.Citations) != 2 {
		t.Fatal("chunk mapping failed", err)
	}
	for i, citation := range result.Citations {
		if citation.DocumentID != "doc-1" || citation.Title != "验收指南.md" || citation.URL != "" || citation.Excerpt == "" {
			t.Fatal("document metadata or chunk evidence lost")
		}
		if i > 0 && (citation.ID == result.Citations[0].ID || citation.Excerpt == result.Citations[0].Excerpt) {
			t.Fatal("distinct chunks collapsed into one citation")
		}
	}
	if _, err := read(raw, "other-document"); err == nil {
		t.Fatal("wrong document metadata accepted")
	}
	changed, err := read(strings.Replace(raw, "验收指南.md", "修订指南.md", 1), "doc-1")
	if err != nil || changed.Citations[0].ID == result.Citations[0].ID {
		t.Fatal("changed document title kept old citation identity", err)
	}
	for _, pointer := range []string{"/missing", "/data/title", "/data/chunks"} {
		*mapping.Fetch.MetadataObject = pointer
		if _, err := read(raw, "doc-1"); err == nil {
			t.Fatal("invalid metadata object accepted")
		}
	}
	for _, config := range []string{`{"search":{"items":"/hits","doc_id":"/doc_id","metadata_object":"/data"}}`, `{"fetch":{"metadata_object":"bad"}}`} {
		if _, err := knowledgeMappingJSON(config); err == nil {
			t.Fatal("invalid shared metadata configuration accepted")
		}
	}
}
func TestKnowledgeCitationsBindActualResponseWithoutGuessingFields(t *testing.T) {
	k := &Knowledge{config: KnowledgeConfig{KBID: "kb", BaseURL: "https://example.test", TeamID: "team", WorkspaceID: "workspace", ResponseMapping: citationFixtureMapping()}}
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	response := func(raw string, owner agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
		envelope, _ := json.Marshal(httpapi.Output{Provider: httpapi.ProviderKey, KBID: "kb", Result: json.RawMessage(raw)})
		return k.knowledgeEvidence(envelope, nil, "search", "query", "", owner)
	}
	raw := `{"hits":[{"doc_id":"doc-1","title":"费用规则","url":"https://example.test/docs/policy#fee","snippet":"金额为9007199254740993.01。","amount":9007199254740993}]}`
	first, err := response(raw, a)
	if err != nil || len(first.Citations) != 1 {
		t.Fatal(first, err)
	}
	citation := first.Citations[0]
	if citation.DocumentID != "doc-1" || citation.Title != "费用规则" || citation.URL != "https://example.test/docs/policy#fee" || citation.Excerpt != "金额为9007199254740993.01。" || !strings.Contains(string(first.Data), "9007199254740993") {
		t.Fatal("source lost exact fields or numeric precision")
	}
	same, _ := response(raw, a)
	if same.Citations[0].ID != citation.ID {
		t.Fatal("unstable source ID")
	}
	other := a
	other.UserID = "other"
	different, _ := response(raw, other)
	if different.Citations[0].ID == citation.ID {
		t.Fatal("source identity crossed owner boundary")
	}
	changed, _ := response(strings.Replace(raw, "金额为", "金额应为", 1), a)
	if changed.Citations[0].ID == citation.ID {
		t.Fatal("changed evidence reused citation identity")
	}
	unsafe, _ := response(strings.Replace(raw, "https://example.test/docs/policy#fee", "javascript:alert(1)", 1), a)
	if unsafe.Citations[0].URL != "" {
		t.Fatal("unsafe link exposed")
	}
	long, _ := json.Marshal(map[string]any{"hits": []any{map[string]string{"doc_id": "doc-1", "snippet": strings.Repeat("证据", 1000)}}})
	bounded, err := response(string(long), a)
	if err != nil || !bounded.Citations[0].ExcerptTruncated || !utf8.ValidString(bounded.Citations[0].Excerpt) || len(bounded.Citations[0].Excerpt) > 2048 {
		t.Fatal("excerpt was not safely bounded", err)
	}
	for _, bad := range []string{`{"results":[]}`, `{"hits":{}}`, `{"hits":[{"doc_id":123}]}`, `{"hits":[{"title":"no ID"}]}`} {
		if _, err := response(bad, a); err == nil {
			t.Fatal("unverified shape became a citation", bad)
		}
	}
	empty, err := response(`{"hits":[]}`, a)
	if err != nil || len(empty.Citations) != 0 {
		t.Fatal("empty search became evidence", err)
	}
	k.config.ResponseMapping = nil
	unmapped, err := response(raw, a)
	if err != nil || len(unmapped.Citations) != 0 {
		t.Fatal("guessed undocumented fields", err)
	}
}

func TestKnowledgeCitationMappingsAndReceiptsAreRevalidated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":[{"doc_id":"doc-1","title":"Original","snippet":"Actual evidence"}]}`))
	}))
	defer upstream.Close()
	mapping := citationFixtureMapping()
	k, err := NewKnowledge(KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture", TeamID: "team", KBID: "kb", WorkspaceID: "workspace", ResponseMapping: mapping})
	if err != nil {
		t.Fatal(err)
	}
	mapping.Search.Title = "/forged" // caller mutation cannot change source configuration
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	saved, err := k.SearchKnowledge(t.Context(), "query", a)
	if err != nil || saved.Citations[0].Title != "Original" {
		t.Fatal("mutable source mapping", err)
	}
	if err = k.RevalidateKnowledge(t.Context(), saved, a); err != nil {
		t.Fatal(err)
	}
	forged := saved
	forged.Citations = append([]agentsdk.ConversationCitation(nil), saved.Citations...)
	forged.Citations[0].Excerpt = "Invented evidence"
	if k.RevalidateKnowledge(t.Context(), forged, a) == nil {
		t.Fatal("forged source excerpt accepted")
	}
	legacy := saved
	legacy.Citations = nil
	if err = k.RevalidateKnowledge(t.Context(), legacy, a); err != nil {
		t.Fatal("legacy receipt became unreadable", err)
	}
	for _, raw := range []string{`{"unexpected":1}`, `{"search":{"items":"hits","doc_id":"/doc_id"}}`, `{"search":{"items":"/hits","doc_id":"/bad~2"}}`, `{"search":{"items":"/hits"}}`, `{} {}`} {
		if _, err := knowledgeMappingJSON(raw); err == nil {
			t.Fatal("invalid mapping accepted", raw)
		}
	}
	value := map[string]any{"a/b": map[string]any{"~": []any{"exact"}}}
	if got, ok := citationPointer(value, "/a~1b/~0/0"); !ok || got != "exact" {
		t.Fatal("RFC 6901 lookup failed")
	}
	for _, path := range []string{"/a~1b/~0/01", "/a~1b/~0/-1", "/a~1b/~0/1"} {
		if _, ok := citationPointer(value, path); ok {
			t.Fatal("invalid array position accepted")
		}
	}
}
