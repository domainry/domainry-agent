package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCalendarToolsBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_CALENDAR_BROWSER") != "1" {
		t.Skip("opt-in built calendar conversation acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	f := newCalendarProductFixture(t)
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	admin.call("POST", "/app/product/account-setup", `{}`, 200)
	admin.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	other := &browser{t: t, handler: admin.handler, cookies: map[string]*http.Cookie{}}
	other.login("system_administrator@example.com", accountInitial)
	other.changePassword(accountInitial, accountChanged)
	f.grantCalendarReader(admin, other.readSession()["user_id"].(string))
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	var account integration.ConnectionAccount
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/connect":
				account = f.connect(admin)
			case "/__acceptance/partial":
				f.partial.Store(true)
			case "/__acceptance/restart":
				f.close()
				f.open()
				handler = f.boundary(origin, files)
				admin.handler = f.boundary("http://127.0.0.1:8091", files)
				admin.login("admin@example.com", accountChanged)
			case "/__acceptance/revoke-read":
				mutateTestRolePermissions(t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
					out := []identity.ProjectRolePermission{}
					for _, p := range prior {
						if p.PermissionKey != integration.ActionIntegrationConnectionAccountsRead {
							out = append(out, p)
						}
					}
					return out
				})
			case "/__acceptance/restore-read":
				admin.call("POST", "/app/product/account-setup", `{}`, 200)
			case "/__acceptance/revoke-account":
				current := accountDecode[integration.ConnectionAccount](t, admin.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
				admin.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
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
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/calendar-tools.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("calendar browser", err)
	}
	f.mu.Lock()
	exchanges := f.exchanges
	f.mu.Unlock()
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "real_identity": true, "real_agent_tools_integration_sqlite": true, "actual_google_provider": true, "vendor_http_requests": f.vendorCalls.Load(), "model_http_requests": f.modelCalls.Load(), "oauth_protocol_exchanges": exchanges, "reopened": true, "real_vendor_account": false, "real_language_model": false}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
