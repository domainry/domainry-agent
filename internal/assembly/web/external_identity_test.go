//go:build external_identity

package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	webhttp "github.com/domainry/domainry-agent/web"
	bridgeconfig "github.com/domainry/domainry-identity-bridge/config"
	bridgemodule "github.com/domainry/domainry-identity-bridge/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

func TestExternalAccountAgentOwnershipAndRestart(t *testing.T) {
	const origin = "https://agent.example.com"
	var denied atomic.Bool
	var calls atomic.Int64
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input map[string]string
		if r.Method != "POST" || r.URL.Path != "/passport/token/validate" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || json.NewDecoder(r.Body).Decode(&input) != nil || len(input) != 1 {
			t.Error("incorrect upstream verification request")
			w.WriteHeader(400)
			return
		}
		if input["token"] != "account-a" && input["token"] != "account-b" {
			json.NewEncoder(w).Encode(map[string]any{"errCode": 100003, "data": nil})
			return
		}
		id := json.Number("9007199254740993")
		if input["token"] == "account-b" {
			id = json.Number("9007199254740995")
		}
		json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "data": map[string]any{"valid": !denied.Load(), "user_id": id, "email": input["token"] + "@example.com", "token_type": "access", "expires_at": time.Now().Add(time.Hour).Unix()}})
	}))
	defer provider.Close()
	raw, err := os.ReadFile("../../../../domainry-identity-bridge/examples/token-validation.config.json")
	if err != nil {
		t.Fatal(err)
	}
	var config bridgeconfig.Config
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	config.ApplicationKey = "agent-external-test"
	config.Provider.Verification.Endpoint = provider.URL + "/passport/token/validate"
	config.Browser.Credential.Name = "selected_account_cookie"
	config.Browser.AllowedOrigins = []string{origin}
	configFile := filepath.Join(t.TempDir(), "external.json")
	raw, _ = json.Marshal(config)
	if err = os.WriteFile(configFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	permissions := []identity.ProjectRolePermission{}
	for _, a := range agentsdk.ConversationToolActions() {
		permissions = append(permissions, identity.ProjectRolePermission{PermissionKey: a.Permission.Key, DataScope: identity.DataScopeOwner})
	}
	permissions = append(permissions, identity.ProjectRolePermission{PermissionKey: agentsdk.ConversationInteractionPermission().Key, DataScope: identity.DataScopeOwner})
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "agent.db"), RuntimeID: "external-agent", WorkspaceID: "installation", ApplicationKey: config.ApplicationKey, Agent: agentmodule.Options{ConversationProvider: testModel{}}, ExternalIdentity: bridgemodule.NewFactory(configFile, bridgemodule.Options{Transport: provider.Client().Transport}), ExternalRoles: []identity.ProjectRoleDefinition{{Key: config.PersonalWorkspace.InitialRoleKeys[0], Name: "Personal owner", Audience: "user", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: permissions}}}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Files: fstest.MapFS{"index.html": {Data: []byte("Agent")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { host.Close(context.Background()) }()
	call := func(token, scope, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", rand.Text())
		request.Header.Set("X-Agent-Scope", scope)
		if token != "" {
			request.AddCookie(&http.Cookie{Name: config.Browser.Credential.Name, Value: token})
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		if out.Code != status {
			t.Fatalf("%s %s: %d want %d; %s", method, path, out.Code, status, out.Body.String())
		}
		if strings.Contains(out.Body.String(), "selected_account_cookie") || strings.Contains(out.Body.String(), "access_token") || len(out.Result().Cookies()) != 0 {
			t.Fatal("external credentials leaked or local session issued")
		}
		return out
	}
	session := func(token string) map[string]string {
		out := call(token, "", "GET", "/app/session", "", 200)
		var body map[string]json.RawMessage
		json.Unmarshal(out.Body.Bytes(), &body)
		result := map[string]string{}
		for _, key := range []string{"workspace_id", "user_id", "scope"} {
			var value string
			json.Unmarshal(body[key], &value)
			result[key] = value
		}
		return result
	}
	call("", "", "GET", "/app/session", "", 401)
	call("bad", "", "GET", "/app/session", "", 401)
	call("", "", "GET", "/auth/external/config", "", 200)
	call("", "", "POST", "/auth/login", `{}`, 404)
	a, b := session("account-a"), session("account-b")
	if a["workspace_id"] == b["workspace_id"] || a["workspace_id"] == options.WorkspaceID || a["user_id"] == b["user_id"] {
		t.Fatal("external accounts share a workspace")
	}
	call("account-a", "", "GET", "/auth/external/session", "", 200)
	created := call("account-a", a["scope"], "POST", "/agent/conversations", `{"client_id":"external-login","title":"Private A"}`, 200)
	var conversation agentsdk.Conversation
	json.Unmarshal(created.Body.Bytes(), &conversation)
	if conversation.UserID != a["user_id"] || conversation.WorkspaceID != a["workspace_id"] {
		t.Fatal("conversation owner is not the verified account")
	}
	base := "/agent/conversations/" + conversation.ID
	call("account-b", b["scope"], "GET", base, "", 404)
	call("account-b", a["scope"], "POST", "/agent/conversations", `{"client_id":"stale"}`, 409)
	call("account-a", a["scope"], "PUT", "/agent/conversations/memories/private", `{"title":"A only","content":"Personal fact","enabled":true}`, 200)
	if strings.Contains(call("account-b", b["scope"], "GET", "/agent/conversations/memories", "", 200).Body.String(), "Personal fact") {
		t.Fatal("memory leaked across workspaces")
	}
	var run agentsdk.ConversationRun
	json.Unmarshal(call("account-a", a["scope"], "POST", base+"/messages", `{"client_message_id":"message-one","message":"hello"}`, 202).Body.Bytes(), &run)
	deadline := time.Now().Add(5 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		json.Unmarshal(call("account-a", a["scope"], "GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
	}
	if run.Status != "completed" {
		t.Fatalf("Agent run: %+v", run)
	}
	call("account-a", a["scope"], "GET", base+"/runs/"+run.ID+"/events/stream?scope="+a["scope"], "", 200)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: a["workspace_id"], UserID: a["user_id"], RoleKey: config.PersonalWorkspace.InitialRoleKeys[0]}
	if err := host.authorizeKnowledgeWorkspace(t.Context(), authority); err != nil {
		t.Fatal("personal knowledge scope denied", err)
	}
	for _, definition := range agentsdk.PersonalConversationTools() {
		out, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: authority, Definition: definition})
		if err != nil || !out.Granted {
			t.Fatalf("personal tool %s denied: %+v %v", definition.Key, out, err)
		}
	}
	authority.WorkspaceID = b["workspace_id"]
	if err := host.authorizeKnowledgeWorkspace(t.Context(), authority); err == nil {
		t.Fatal("forged personal knowledge scope allowed")
	}
	out, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: authority, Definition: agentsdk.PersonalConversationTools()[0]})
	if err == nil && out.Granted {
		t.Fatal("forged background workspace authorized")
	}
	denied.Store(true)
	call("account-a", a["scope"], "GET", base, "", 401)
	denied.Store(false)
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	open()
	restored := session("account-a")
	if restored["scope"] != a["scope"] {
		t.Fatal("workspace changed after restart")
	}
	call("account-a", a["scope"], "GET", base, "", 200)
	var workspaceCount, identityTables int
	host.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_web_workspaces`).Scan(&workspaceCount)
	if err = host.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name GLOB '_identity_*'`).Scan(&identityTables); err != nil {
		t.Fatal(err)
	}
	if workspaceCount != 2 || identityTables != 0 {
		t.Fatalf("workspaces=%d identity tables=%d", workspaceCount, identityTables)
	}
	if calls.Load() < 15 {
		t.Fatal("requests did not revalidate the external credential")
	}
}
