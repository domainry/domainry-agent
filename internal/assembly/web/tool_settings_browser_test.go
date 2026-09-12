package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
)

func TestToolSettingsBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_TOOL_SETTINGS_BROWSER") != "1" {
		t.Skip("opt-in built Tools client acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	f := newToolSettingsFixture(t)
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	f.grantSettings(admin, "admin", true)
	mutateTestRolePermissions(t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if !strings.HasPrefix(p.PermissionKey, "tools.preferences.") {
				out = append(out, p)
			}
		}
		return out
	})
	user := &browser{t: t, handler: admin.handler, cookies: map[string]*http.Cookie{}}
	user.login("system_administrator@example.com", accountInitial)
	user.changePassword(accountInitial, accountChanged)
	userID := user.readSession()["user_id"].(string)
	f.grantSettings(admin, userID, true)
	f.grant(admin, userID, "", false)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	var account *integration.ConnectionAccount
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/restart":
				f.close()
				f.open()
				handler = f.boundary(origin, files)
				admin.handler = f.boundary("http://127.0.0.1:8091", files)
				admin.login("admin@example.com", accountChanged)
			case "/__acceptance/revoke-tool":
				f.grantSettings(admin, "admin", false)
			case "/__acceptance/restore-tool":
				f.grantSettings(admin, "admin", true)
			case "/__acceptance/connect":
				admin.call("POST", "/app/product/account-setup", `{}`, 200)
				admin.call("PUT", "/integration/oauth-applications/work-google", accountJSON(integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{accountScope}, Enabled: true}), 200)
				session := accountDecode[integration.OAuthAuthorizationSession](t, admin.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "work-google", Scope: integration.ConnectionAccountScopePersonal, Name: "personal", Scopes: []string{accountScope}}), 200))
				target, _ := url.Parse(f.callback(session.AuthorizationURL, "connect"))
				done := accountDecode[integration.OAuthAuthorizationSession](t, admin.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: target.Query().Get("state"), Code: target.Query().Get("code")}), 200))
				account = done.Account
			case "/__acceptance/revoke-account":
				if account == nil {
					w.WriteHeader(409)
					return
				}
				admin.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": account.UpdatedAt}), 200)
			default:
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(204)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/tool-settings.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("tool settings browser", err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "real_identity": true, "real_tools_store": true, "real_integration_store": true, "oauth_exchanges": f.exchanges, "provider_probes": f.probes, "reopened": true}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
