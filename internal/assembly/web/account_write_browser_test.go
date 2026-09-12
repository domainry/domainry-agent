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

func TestAccountWritesBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_ACCOUNT_WRITE_BROWSER") != "1" {
		t.Skip("opt-in built account write acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	f := newAccountWriteProductFixture(t)
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	admin.call("POST", "/app/product/account-setup", `{}`, 200)
	admin.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	grantAccountWriteConfirmation(t, f.host, admin)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// These controls only exist in the isolated httptest deployment.
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/connect":
				f.connect(admin)
			case "/__acceptance/restart":
				f.close()
				f.open()
				handler = f.boundary(origin, files)
				admin.handler = f.boundary("http://127.0.0.1:8091", files)
				admin.login("admin@example.com", accountChanged)
			case "/__acceptance/revoke-write":
				mutateTestRolePermissions(t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
					out := []identity.ProjectRolePermission{}
					for _, p := range prior {
						if p.PermissionKey != integration.ActionIntegrationConnectionAccountsWrite {
							out = append(out, p)
						}
					}
					return out
				})
			case "/__acceptance/restore-write":
				admin.call("POST", "/app/product/account-setup", `{}`, 200)
			case "/__acceptance/audit":
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"effects": f.counts(), "vendor_http_requests": f.requests.Load(), "model_http_requests": f.modelRequests.Load()})
				return
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
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/account-write.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("account write browser", err)
	}
	f.mu.Lock()
	exchanges := f.exchanges
	f.mu.Unlock()
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "real_identity_agent_tools_integration_sqlite": true, "actual_google_provider": true, "oauth_protocol_exchanges": exchanges, "vendor_http_requests": f.requests.Load(), "model_http_requests": f.modelRequests.Load(), "effects": f.counts(), "real_vendor_account": false, "real_language_model": false}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
