package module

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestLibraryKnowledgeConfigurationIsolation(t *testing.T) {
	t.Setenv("TEST_LIBRARY_API_KEY", "fixture-key")
	raw := `[{"library_id":"lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workspace_id":"w","base_url":"https://knowledge.example.test","team_id":"team","kb_id":"kb","api_key_env":"TEST_LIBRARY_API_KEY"}]`
	values, err := knowledgeLibraryEnvironment(raw)
	if err != nil || len(values) != 1 || values[0].Knowledge.APIKey != "fixture-key" {
		t.Fatal("key reference not resolved", err)
	}
	for _, invalid := range []string{raw + `{}`, strings.Replace(raw, `"api_key_env":"TEST_LIBRARY_API_KEY"`, `"api_key":"secret"`, 1), strings.Replace(raw, `TEST_LIBRARY_API_KEY`, `bad-name`, 1)} {
		if _, err := knowledgeLibraryEnvironment(invalid); err == nil {
			t.Fatal("invalid binding config accepted")
		}
	}
	var options ConversationOptions
	if err := assembleLibraryKnowledge(&options, nil, raw, KnowledgeConfig{}, "r"); err != nil || len(options.LibraryKnowledge) != 1 {
		t.Fatal(err)
	}
	options = ConversationOptions{}
	if err := assembleLibraryKnowledge(&options, append(values, values...), "", KnowledgeConfig{}, "r"); err == nil {
		t.Fatal("same remote KB shared by two bindings")
	}
	options = ConversationOptions{}
	legacy := values[0].Knowledge
	legacy.BaseURL = "https://KNOWLEDGE.example.test:443/"
	if err := assembleLibraryKnowledge(&options, values, "", legacy, "r"); err == nil {
		t.Fatal("default source bypasses library membership")
	}
}

func TestLibraryKnowledgePrivateRetrievalPolicy(t *testing.T) {
	t.Setenv("TEST_LIBRARY_API_KEY", "fixture-key")
	var called atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		var in struct {
			TeamID string   `json:"team_id"`
			KBID   string   `json:"kb_id"`
			IDs    []string `json:"permission_ids"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.TeamID != "team" || in.KBID != "private-kb" || !slices.Equal(in.IDs, []string{"library:private:read"}) {
			t.Error("library retrieval did not use its trusted permission scope")
		}
		_, _ = w.Write([]byte(`{"err_code":0,"data":{"hits":[]}}`))
	}))
	defer upstream.Close()
	raw, _ := json.Marshal([]map[string]any{{"library_id": "lib_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "workspace_id": "w", "base_url": upstream.URL, "team_id": "team", "kb_id": "private-kb", "api_key_env": "TEST_LIBRARY_API_KEY", "permission_ids": []string{"library:private:read"}}})
	var options ConversationOptions
	if err := assembleLibraryKnowledge(&options, nil, string(raw), KnowledgeConfig{}, "r"); err != nil {
		t.Fatal(err)
	}
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "r", WorkspaceID: "w", UserID: "current-user"}
	if _, err := options.LibraryKnowledge[0].Source.SearchKnowledge(t.Context(), "query", a); err != nil || !called.Load() {
		t.Fatal("private retrieval was not sent through the Connector", err)
	}
	called.Store(false)
	a.WorkspaceID = "other"
	if _, err := options.LibraryKnowledge[0].Source.SearchKnowledge(t.Context(), "query", a); err == nil || called.Load() {
		t.Fatal("library policy bypassed workspace isolation")
	}
}

func TestLibraryKnowledgePermissionPolicyValidationAndImmutability(t *testing.T) {
	for _, ids := range [][]string{{""}, {" reader"}, {"reader "}, {"reader\n"}, {"reader\x7f"}, {string([]byte{0xff})}, {strings.Repeat("界", 43)}, slices.Repeat([]string{"reader"}, 101)} {
		binding := KnowledgeLibraryConfig{PermissionIDs: ids}
		if err := bindLibraryPermissions(&binding, "r"); err == nil || binding.Knowledge.PermissionIDs != nil {
			t.Fatal("invalid opaque permissions accepted")
		}
	}
	for _, binding := range []KnowledgeLibraryConfig{
		{ManageDocuments: true, Knowledge: KnowledgeConfig{PermissionIDs: func(context.Context, agentsdk.ConversationAuthority) ([]string, error) { return nil, nil }}},
		{PermissionIDs: []string{"reader"}, Knowledge: KnowledgeConfig{PermissionIDs: func(context.Context, agentsdk.ConversationAuthority) ([]string, error) { return nil, nil }}},
	} {
		if err := bindLibraryPermissions(&binding, "r"); err == nil {
			t.Fatal("ambiguous or unsupported write policy accepted")
		}
	}
	original := []string{"z:read", "a:read", "z:read"}
	binding := KnowledgeLibraryConfig{PermissionIDs: original}
	if err := bindLibraryPermissions(&binding, "r"); err != nil {
		t.Fatal(err)
	}
	original[0] = "broader:scope"
	first := binding.Knowledge.DocumentPermissionIDs
	if !slices.Equal(first, []string{"a:read", "z:read"}) {
		t.Fatal("caller mutation changed startup policy")
	}
}

func TestManagedLibraryDefaultsToPrivateScope(t *testing.T) {
	base := KnowledgeLibraryConfig{LibraryID: "library-a", ManageDocuments: true, Knowledge: KnowledgeConfig{WorkspaceID: "workspace"}}
	first := base
	if err := bindLibraryPermissions(&first, "runtime"); err != nil {
		t.Fatal(err)
	}
	ids := first.Knowledge.DocumentPermissionIDs
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "scope:agent:library:") || first.Knowledge.PermissionIDs != nil {
		t.Fatal("managed default not private and fixed")
	}
	second := base
	if err := bindLibraryPermissions(&second, "runtime"); err != nil || !slices.Equal(ids, second.Knowledge.DocumentPermissionIDs) {
		t.Fatal("restart changed library ACL", err)
	}
	readOnly := base
	readOnly.ManageDocuments = false
	if err := bindLibraryPermissions(&readOnly, "runtime"); err != nil || !slices.Equal(ids, readOnly.Knowledge.DocumentPermissionIDs) {
		t.Fatal("disabling writes changed private read scope", err)
	}
	for _, tc := range []struct{ runtime, workspace, library string }{{"other", "workspace", "library-a"}, {"runtime", "other", "library-a"}, {"runtime", "workspace", "library-b"}} {
		x := base
		x.LibraryID = tc.library
		x.Knowledge.WorkspaceID = tc.workspace
		if err := bindLibraryPermissions(&x, tc.runtime); err != nil || slices.Equal(ids, x.Knowledge.DocumentPermissionIDs) {
			t.Fatal("separate library authority reused private ACL", err)
		}
	}
	explicit := base
	explicit.PermissionIDs = []string{"trusted:private"}
	if err := bindLibraryPermissions(&explicit, "runtime"); err != nil || !slices.Equal(explicit.Knowledge.DocumentPermissionIDs, explicit.PermissionIDs) {
		t.Fatal("explicit ACL lost", err)
	}
	explicit.PermissionIDs[0] = "mutated"
	if explicit.Knowledge.DocumentPermissionIDs[0] != "trusted:private" {
		t.Fatal("caller can mutate fixed policy")
	}
}
