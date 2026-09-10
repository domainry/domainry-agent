package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func knowledgeAuthority() agentsdk.ConversationAuthority {
	return agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
}

func newTestKnowledge(t *testing.T, server *httptest.Server) *Knowledge {
	t.Helper()
	k, err := NewKnowledge(KnowledgeConfig{BaseURL: server.URL, APIKey: "private-key", TeamID: "team", KBID: "kb", WorkspaceID: "workspace", Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKnowledgeSearchFetchUseDocumentedProtocolAndPreserveSources(t *testing.T) {
	requests := make(chan map[string]any, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer private-key" || r.Header.Get("x-api-key") != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect knowledge request protocol")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		body["path"] = r.URL.Path
		requests <- body
		// An illustrative fixture, not a claimed upstream response schema.
		io.WriteString(w, `{"passages":[{"doc_id":"guide","text":"文档内容","url":"https://example.com/guide"}],"future_field":7}`)
	}))
	defer server.Close()
	k := newTestKnowledge(t, server)
	for _, fetch := range []bool{false, true} {
		var raw json.RawMessage
		var err error
		if fetch {
			raw, err = k.Fetch(t.Context(), "guide", knowledgeAuthority())
		} else {
			raw, err = k.Search(t.Context(), "文档内容是什么？", knowledgeAuthority())
		}
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			KBID   string                     `json:"kb_id"`
			Result map[string]json.RawMessage `json:"result"`
		}
		if json.Unmarshal(raw, &out) != nil || out.KBID != "kb" || string(out.Result["future_field"]) != "7" || !strings.Contains(string(raw), "文档内容") || !strings.Contains(string(raw), "https://example.com/guide") {
			t.Fatalf("source data lost: %s", raw)
		}
		body := <-requests
		want := map[string]any{"path": "/v1/kb/search", "team_id": "team", "kb_id": "kb", "query": "文档内容是什么？", "top_k": float64(5)}
		if fetch {
			want = map[string]any{"path": "/v1/kb/fetch", "team_id": "team", "kb_id": "kb", "doc_id": "guide"}
		}
		if !reflect.DeepEqual(body, want) {
			t.Fatalf("request = %+v; want %+v", body, want)
		}
	}
}

func TestKnowledgeScopeAndServerPermissions(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Permissions []string `json:"permission_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body.Permissions, []string{"dept:finance"}) {
			t.Error("missing server-derived permissions")
		}
		io.WriteString(w, `[]`)
	}))
	defer server.Close()
	k := newTestKnowledge(t, server)
	k.config.PermissionIDs = func(_ context.Context, a agentsdk.ConversationAuthority) ([]string, error) {
		if a != knowledgeAuthority() {
			t.Error("wrong authority")
		}
		return []string{"dept:finance"}, nil
	}
	for _, fetch := range []bool{false, true} {
		if fetch {
			if _, err := k.Fetch(t.Context(), "doc", knowledgeAuthority()); err != nil {
				t.Fatal(err)
			}
		} else if _, err := k.Search(t.Context(), "query", knowledgeAuthority()); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*agentsdk.ConversationAuthority){
		func(a *agentsdk.ConversationAuthority) { a.Known = false },
		func(a *agentsdk.ConversationAuthority) { a.WorkspaceID = "other-workspace" },
		func(a *agentsdk.ConversationAuthority) { a.UserID = "" },
	} {
		a := knowledgeAuthority()
		mutate(&a)
		if _, err := k.Search(t.Context(), "query", a); err == nil {
			t.Fatal("scope bypass")
		}
	}
	k.config.PermissionIDs = func(context.Context, agentsdk.ConversationAuthority) ([]string, error) {
		return nil, errors.New("private auth failure")
	}
	if _, err := k.Fetch(t.Context(), "doc", knowledgeAuthority()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("permission failure leaked or bypassed")
	}
	if calls.Load() != 2 {
		t.Fatalf("unauthorized upstream requests: %d", calls.Load())
	}
}

func TestKnowledgePersonalWorkspacesRequireCurrentHostAuthority(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Permissions []string `json:"permission_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Permissions) != 1 || (body.Permissions[0] != "document:a" && body.Permissions[0] != "document:b") {
			t.Error("permissions were not resolved for the current owner")
		}
		io.WriteString(w, `[]`)
	}))
	defer server.Close()
	k := newTestKnowledge(t, server)
	revoked := false
	k.config.AuthorizeWorkspace = func(_ context.Context, a agentsdk.ConversationAuthority) error {
		if revoked || a.RuntimeID != "runtime" || (a.UserID != "a" && a.UserID != "b") || a.WorkspaceID != "personal-"+a.UserID {
			return errors.New("denied")
		}
		return nil
	}
	k.config.PermissionIDs = func(_ context.Context, a agentsdk.ConversationAuthority) ([]string, error) {
		return []string{"document:" + a.UserID}, nil
	}
	for _, user := range []string{"a", "b"} {
		a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "personal-" + user, UserID: user}
		if _, err := k.Search(t.Context(), "test", a); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []agentsdk.ConversationAuthority{
		{Known: true, RuntimeID: "runtime", WorkspaceID: "personal-b", UserID: "a"},
		{Known: true, RuntimeID: "other", WorkspaceID: "personal-a", UserID: "a"},
		{Known: false, RuntimeID: "runtime", WorkspaceID: "personal-a", UserID: "a"},
	} {
		if _, err := k.Fetch(t.Context(), "doc", a); err == nil {
			t.Fatal("forged authority reached knowledge service")
		}
	}
	revoked = true
	if _, err := k.Search(t.Context(), "test", agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "personal-a", UserID: "a"}); err == nil {
		t.Fatal("revoked owner reached knowledge service")
	}
	if calls.Load() != 2 {
		t.Fatal("unauthorized upstream requests", calls.Load())
	}
}

func TestKnowledgeFailuresAreBoundedAndContentFree(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"auth", 403, "private-key", "access_denied"},
		{"rate", 429, "private-key", "rate_limited"},
		{"upstream", 503, "private-key", "unavailable"},
		{"invalid", 200, "not json private-key", "response_invalid"},
		{"null", 200, "null", "response_invalid"},
		{"scalar", 200, `"private-key"`, "response_invalid"},
		{"trailing", 200, `{} {}`, "response_invalid"},
		{"envelope", 200, `{"error":{"message":"private-key"}}`, "failed"},
		{"oversized", 200, `{}` + strings.Repeat(" ", 512*1024), "response_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); io.WriteString(w, tc.body) }))
			defer server.Close()
			_, err := newTestKnowledge(t, server).Search(t.Context(), "query", knowledgeAuthority())
			var failure *agentsdk.Error
			if !errors.As(err, &failure) || failure.Code != "agent.conversation.knowledge_"+tc.code || strings.Contains(err.Error(), "private-key") {
				t.Fatalf("unsafe or incorrect error: %v", err)
			}
		})
	}
}

func TestKnowledgeDoesNotFollowRedirectsAndHonorsCancellation(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	k := newTestKnowledge(t, server)
	if _, err := k.Search(t.Context(), "query", knowledgeAuthority()); err == nil || leaked.Load() {
		t.Fatal("redirect followed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := k.Search(ctx, "query", knowledgeAuthority()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestKnowledgeEnvironmentIsOptInAndValidatesPartialConfiguration(t *testing.T) {
	for _, key := range []string{"AGENT_KNOWLEDGE_BASE_URL", "AGENT_KNOWLEDGE_API_KEY", "AGENT_KNOWLEDGE_TEAM_ID", "AGENT_KNOWLEDGE_KB_ID", "AGENT_KNOWLEDGE_WORKSPACE_ID", "AGENT_KNOWLEDGE_TOP_K", "AGENT_PROVIDER_API_KEY"} {
		t.Setenv(key, "")
	}
	t.Setenv("AGENT_KNOWLEDGE_API_KEY", "configured-key")
	if k, err := NewKnowledge(KnowledgeConfigFromEnvironment()); err != nil || k != nil {
		t.Fatal("key alone enabled retrieval")
	}
	t.Setenv("AGENT_KNOWLEDGE_KB_ID", "kb")
	if _, err := NewKnowledge(KnowledgeConfigFromEnvironment()); err == nil {
		t.Fatal("partial config accepted")
	}
	t.Setenv("AGENT_KNOWLEDGE_BASE_URL", "https://kb.example.com")
	t.Setenv("AGENT_KNOWLEDGE_TEAM_ID", "team")
	t.Setenv("AGENT_KNOWLEDGE_WORKSPACE_ID", "workspace")
	k, err := NewKnowledge(KnowledgeConfigFromEnvironment())
	if err != nil || k.config.APIKey != "configured-key" || k.config.TopK != 5 {
		t.Fatal("invalid defaults", err)
	}
	missingOrigin := k.config
	missingOrigin.BaseURL = ""
	if _, err := NewKnowledge(missingOrigin); err == nil {
		t.Fatal("missing origin silently selected an unrelated knowledge service")
	}
	t.Setenv("AGENT_KNOWLEDGE_API_KEY", "kb-key")
	if KnowledgeConfigFromEnvironment().APIKey != "kb-key" {
		t.Fatal("key precedence")
	}
	for _, value := range []string{"invalid", "0", "-1", "21"} {
		t.Setenv("AGENT_KNOWLEDGE_TOP_K", value)
		if _, err := NewKnowledge(KnowledgeConfigFromEnvironment()); err == nil {
			t.Fatal("invalid top_k accepted")
		}
	}
}
