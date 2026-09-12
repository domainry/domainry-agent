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

	integration "github.com/domainry/domainry-integration-sdk"
)

func TestExternalAccountsBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_ACCOUNTS_BROWSER") != "1" {
		t.Skip("opt-in built account client acceptance")
	}
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	f := newAccountFixture(t)
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	user := &browser{t: t, handler: admin.handler, cookies: map[string]*http.Cookie{}}
	user.login("system_administrator@example.com", accountInitial)
	user.changePassword(accountInitial, accountChanged)
	userID := user.readSession()["user_id"].(string)
	f.grant(admin, userID, "", false)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/__acceptance/") {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			switch r.URL.Path {
			case "/__acceptance/authorize":
				var in struct{ URL, Outcome string }
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					w.WriteHeader(400)
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"callback": f.callback(in.URL, in.Outcome)})
				return
			case "/__acceptance/restart":
				f.close()
				f.open()
				handler = f.boundary(origin, files)
				admin.handler = f.boundary("http://127.0.0.1:8091", files)
				admin.login("admin@example.com", accountChanged)
			case "/__acceptance/revoke-list":
				f.grant(admin, "admin", integration.ActionIntegrationConnectionAccountsList, true)
			case "/__acceptance/restore":
				f.grant(admin, "admin", "", true)
			case "/__acceptance/state":
				f.mu.Lock()
				defer f.mu.Unlock()
				json.NewEncoder(w).Encode(map[string]int{"exchanges": f.exchanges, "probes": f.probes})
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
	node := os.Getenv("AGENT_NODE_BINARY")
	if node == "" {
		t.Fatal("AGENT_NODE_BINARY required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, node, filepath.Join(project, "frontend/tests/external-accounts.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("account browser acceptance failed", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exchanges != 3 || f.probes != 1 {
		t.Fatal("unexpected durable provider effects", f.exchanges, f.probes)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "real_identity": true, "provider": "google_workspace/google", "issuer": "local protocol fixture", "exchanges": f.exchanges, "probes": f.probes, "agent_and_integration_reopened": true}, "", "  ")
	if err = os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
