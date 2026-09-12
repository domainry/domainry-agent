package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	module "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

func newToolSettingsFixture(t *testing.T) *accountFixture {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
			http.Error(w, "invalid", 400)
			return
		}
		keys := []string{}
		calculate := false
		memory := false
		for _, v := range in.Tools {
			keys = append(keys, v.Function.Name)
			calculate = calculate || v.Function.Name == "calculate"
			memory = memory || v.Function.Name == "memory_save"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "tool" {
			write(map[string]any{"content": "已执行计算：" + last.Content}, "stop")
		} else if strings.Contains(last.Content, "保存记忆") && memory {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "settings-memory", "type": "function", "function": map[string]any{"name": "memory_save", "arguments": `{"title":"F01 settings","content":"Keep this isolated test memory","enabled":true,"expected_revision":0}`}}}}, "tool_calls")
		} else if strings.Contains(last.Content, "计算") && calculate {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "settings-calculation", "type": "function", "function": map[string]any{"name": "calculate", "arguments": `{"operation":"expression","expression":"0.1+0.2","unit":"CNY"}`}}}}, "tool_calls")
		} else {
			write(map[string]any{"content": "当前可用工具：" + strings.Join(keys, "、")}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	f := newAccountFixture(t)
	f.close()
	f.options.Agent = module.Options{ConversationURL: upstream.URL, ConversationModel: "settings-fixture", ConversationOptions: module.ConversationOptions{Poll: 5 * time.Millisecond, Agent: &sdk.AgentSchema{Key: "settings", Name: "Settings", Version: "1", Instructions: "Test deployment selection", Tools: []string{"calculate", "time_now"}}}}
	// Only the acceptance deployment routes time_now through an account preflight.
	// This exercises the F01 port without implementing or advertising an F02 tool.
	f.options.ToolAccountRequirements = map[string][]ToolAccountRequirement{"time_now": {{ConnectorKey: "google_workspace", ProviderKey: "google", Scopes: []string{accountScope}}}}
	f.open()
	return f
}
func (f *accountFixture) grantSettings(admin *browser, user string, calculate bool) {
	f.t.Helper()
	mutateTestRolePermissions(f.t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if !strings.HasPrefix(p.PermissionKey, "tools.preferences.") && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix) {
				out = append(out, p)
			}
		}
		for _, r := range tools.ToolSettingsRoutes() {
			out = append(out, identity.ProjectRolePermission{PermissionKey: r.Action.Permission.Key, DataScope: identity.DataScopeOwner})
		}
		for _, d := range sdk.PersonalConversationTools() {
			if d.Key != "calculate" || calculate {
				out = append(out, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
			}
		}
		return out
	}, user)
}
func settingList(t *testing.T, b *browser) map[string]tools.ToolSetting {
	t.Helper()
	value := accountDecode[struct {
		Items []tools.ToolSetting `json:"items"`
	}](t, b.call("GET", "/tools/preferences", "", 200))
	out := map[string]tools.ToolSetting{}
	for _, item := range value.Items {
		out[item.Key] = item
	}
	return out
}
func settingsRun(t *testing.T, b *browser, message string) string {
	t.Helper()
	_, _, answer := settingsRunReference(t, b, message)
	return answer
}
func settingsRunReference(t *testing.T, b *browser, message string) (string, string, string) {
	t.Helper()
	id := fmt.Sprintf("settings-%d", time.Now().UnixNano())
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: id}), 200))
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: id, Message: message}), 202))
	deadline := time.Now().Add(15 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		run = accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "completed" {
		t.Fatalf("run=%+v", run)
	}
	page := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200))
	return c.ID, run.ID, page.Items[len(page.Items)-1].Content
}
func TestToolSettingsCurrentIdentityCatalogConnectionAndRestart(t *testing.T) {
	f := newToolSettingsFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("settings")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.call("GET", "/tools/preferences", "", 401)
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("GET", "/tools/preferences", "", 403)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	if got := settingList(t, b); len(got) != 0 {
		t.Fatal("settings permission granted a tool", got)
	}
	f.grantSettings(b, "admin", true)
	got := settingList(t, b)
	if len(got) != 2 || !got["calculate"].Available || got["time_now"].Available || got["time_now"].State != "connection_unavailable" {
		t.Fatalf("selection or missing account ignored %+v", got)
	}
	if answer := settingsRun(t, b, "目录"); !strings.Contains(answer, "calculate") || strings.Contains(answer, "time_now") {
		t.Fatal(answer)
	}
	priorConversation, _, priorAnswer := settingsRunReference(t, b, "计算")
	if !strings.Contains(priorAnswer, `"value":"0.30"`) {
		t.Fatal("actual tool result missing", priorAnswer)
	}
	value := got["calculate"]
	input := tools.ToolSettingInput{Enabled: false, ToolVersion: value.Version}
	disabled := accountDecode[tools.ToolSetting](t, b.call("PUT", "/tools/preferences/calculate", accountJSON(input), 200))
	if disabled.Enabled || disabled.Available || disabled.Revision != 1 {
		t.Fatal(disabled)
	}
	b.call("PUT", "/tools/preferences/calculate", accountJSON(input), 409)
	prior := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+priorConversation+"/messages", "", 200))
	for _, message := range prior.Items {
		if message.Role == "assistant" && (message.AccessError == "" || strings.Contains(message.Content, `"value":"0.30"`)) {
			t.Fatalf("disabled personal result leaked: %+v", message)
		}
	}
	b.call("PUT", "/tools/preferences/memory_save", accountJSON(input), 403)
	b.call("PUT", "/tools/preferences/calculate", `{"enabled":true,"expected_revision":1,"tool_version":"1","user_id":"someone"}`, 400)
	if answer := settingsRun(t, b, "目录"); strings.Contains(answer, "calculate") {
		t.Fatal("disabled tool reached model", answer)
	}
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	if v := settingList(t, b)["calculate"]; v.Enabled || v.Revision != 1 {
		t.Fatal("restart reset preference", v)
	}
	f.grantSettings(b, "admin", false)
	if _, exists := settingList(t, b)["calculate"]; exists {
		t.Fatal("revoked tool remains in settings")
	}
	b.call("PUT", "/tools/preferences/calculate", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: 1, ToolVersion: value.Version}), 403)
	f.grantSettings(b, "admin", true)
	if v := settingList(t, b)["calculate"]; v.Enabled {
		t.Fatal("permission restoration reset preference")
	}
	b.call("PUT", "/tools/preferences/calculate", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: 1, ToolVersion: value.Version}), 200)
	if answer := settingsRun(t, b, "计算"); !strings.Contains(answer, `"value":"0.30"`) {
		t.Fatal(answer)
	}
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	app := integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{accountScope}, Enabled: true}
	b.call("PUT", "/integration/oauth-applications/work-google", accountJSON(app), 200)
	session := accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "work-google", Scope: integration.ConnectionAccountScopePersonal, Name: "personal", Scopes: []string{accountScope}}), 200))
	callback, _ := url.Parse(f.callback(session.AuthorizationURL, "connect"))
	done := accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: callback.Query().Get("state"), Code: callback.Query().Get("code")}), 200))
	if done.Account.Readiness == nil || done.Account.Readiness.Test == nil || done.Account.Readiness.Test.Allowed {
		t.Fatal("calendar-only grant incorrectly enables profile probe")
	}
	b.call("POST", "/integration/connection-accounts/"+done.Account.Key+"/test", `{}`, 400)
	if v := settingList(t, b)["time_now"]; !v.Available {
		t.Fatal("authorized grant not composed", v)
	}
	if answer := settingsRun(t, b, "目录"); !strings.Contains(answer, "time_now") {
		t.Fatal(answer)
	}
	a := tools.Authority{Known: true, RuntimeID: f.options.RuntimeID, WorkspaceID: f.options.WorkspaceID, UserID: "admin"}
	// A narrower grant cannot satisfy a broader tool requirement.
	f.host.toolAccountRequirements["time_now"][0].Scopes = []string{"scope-never-granted"}
	if available, err := f.host.ToolSettings.Availability().ConversationToolAvailable(context.Background(), a, "time_now"); err != nil || available {
		t.Fatal("requested scopes replaced actual grant", err)
	}
	f.host.toolAccountRequirements["time_now"][0].Scopes = []string{accountScope}
	b.call("POST", "/integration/connection-accounts/"+done.Account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": done.Account.UpdatedAt}), 200)
	if v := settingList(t, b)["time_now"]; v.Available {
		t.Fatal("revoked connection available", v)
	}
	// Workspace accounts are usable only with current shared account permission.
	user := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	user.login("system_administrator@example.com", accountInitial)
	user.changePassword(accountInitial, accountChanged)
	userID := user.readSession()["user_id"].(string)
	f.grantSettings(b, userID, true)
	f.grant(b, userID, "", false)
	if v := settingList(t, user)["time_now"]; v.Available {
		t.Fatal("foreign personal account visible", v)
	}
	shared := accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "work-google", Scope: integration.ConnectionAccountScopeWorkspace, Name: "shared", Scopes: []string{accountScope}}), 200))
	sharedCallback, _ := url.Parse(f.callback(shared.AuthorizationURL, "connect"))
	b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: sharedCallback.Query().Get("state"), Code: sharedCallback.Query().Get("code")}), 200)
	if v := settingList(t, user)["time_now"]; v.Available {
		t.Fatal("personal account permission opened shared connection", v)
	}
	f.grant(b, userID, "", true)
	if v := settingList(t, user)["time_now"]; !v.Available {
		t.Fatal("shared account grant unavailable", v)
	}
	f.grant(b, userID, integration.ActionIntegrationConnectionAccountsList, true)
	if v := settingList(t, user)["time_now"]; v.Available {
		t.Fatal("current account permission revocation ignored", v)
	}
	t.Log("PASS Identity/action/current catalog, Agent whitelist, actual calculation, disable/re-enable, CAS/lost response, owner input rejection, restart, permission revocation, OAuth actual grant, missing scope and account revoke")
}
