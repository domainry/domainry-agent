package product

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	gateway "github.com/domainry/domainry-agent-sdk/browsergateway"
	"github.com/domainry/domainry-foundation/modulecapability"
	integration "github.com/domainry/domainry-integration-sdk"
)

func TestIntegrationEnvironmentRequiresCompleteConfiguration(t *testing.T) {
	for _, key := range []string{"INTEGRATION_SAAS_BASE_URL", "INTEGRATION_SAAS_TOKEN", "INTEGRATION_SAAS_CONTRACT_SHA256"} {
		t.Setenv(key, "")
	}
	if binding, err := OpenIntegrationFromEnvironment(t.Context(), "product"); err != nil || binding != nil {
		t.Fatal("optional unconfigured service", err)
	}
	t.Setenv("INTEGRATION_SAAS_BASE_URL", "http://127.0.0.1:1")
	if _, err := OpenIntegrationFromEnvironment(t.Context(), "product"); err == nil {
		t.Fatal("partial service configuration accepted")
	}
}

// This test consumes a separately built production Integration executable. It
// does not access vendor accounts: it exercises configuration, consent start,
// denial and durable receipts over SaaS with the actual product Identity host.
func TestIntegrationSaaSProductionProcessWithProductIdentity(t *testing.T) {
	binary := os.Getenv("AGENT_INTEGRATION_TEST_BINARY")
	if binary == "" {
		t.Skip("opt-in production Integration process")
	}
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Initial-SaaS-Account!2")
	t.Setenv("AUTH_JWT_SECRET", "saas-accounts-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "saas-accounts-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	root := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	base := "http://" + address
	t.Setenv("INTEGRATION_SAAS_BASE_URL", base)
	t.Setenv("INTEGRATION_SAAS_TOKEN", "fixture-service-token")
	var child *exec.Cmd
	var done chan error
	var binding integration.Binding
	var host *Host
	var handler http.Handler
	cookies := map[string]*http.Cookie{}
	scope := ""
	stop := func() {
		if host != nil {
			host.Close(context.Background())
			host = nil
		}
		if binding != nil {
			binding.Close(context.Background())
			binding = nil
		}
		if child != nil {
			child.Process.Signal(os.Interrupt)
			select {
			case err := <-done:
				if err != nil {
					t.Error("Integration process shutdown", err)
				}
			case <-time.After(10 * time.Second):
				child.Process.Kill()
				<-done
				t.Error("Integration shutdown timeout")
			}
			child = nil
		}
	}
	t.Cleanup(stop)
	start := func() {
		t.Helper()
		child = exec.Command(binary)
		child.Env = append(os.Environ(), "INTEGRATION_RUNTIME_ID=accounts-service", "INTEGRATION_SERVICE_TOKEN=fixture-service-token", "INTEGRATION_MASTER_KEY="+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s", 32))), "INTEGRATION_SQLITE_PATH="+filepath.Join(root, "integration.db"), "INTEGRATION_HTTP_ADDRESS="+address)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan error, 1)
		go func(cmd *exec.Cmd) { done <- cmd.Wait() }(child)
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(10 * time.Second)
		var summary modulecapability.ModuleSummary
		for {
			req, _ := http.NewRequest("GET", base+modulecapability.SummaryPath, nil)
			req.Header.Set("Authorization", "Bearer fixture-service-token")
			req.Header.Set("X-Domainry-Runtime-ID", "accounts-product")
			response, err := client.Do(req)
			if err == nil {
				err = json.NewDecoder(response.Body).Decode(&summary)
				response.Body.Close()
				if err == nil && response.StatusCode == 200 {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("Integration process did not become ready")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Setenv("INTEGRATION_SAAS_CONTRACT_SHA256", strings.Repeat("0", 64))
		if _, err := OpenIntegrationFromEnvironment(t.Context(), "accounts-product"); err == nil {
			t.Fatal("mismatched service contract accepted")
		}
		t.Setenv("INTEGRATION_SAAS_CONTRACT_SHA256", summary.Identity.ContractSHA256)
		binding, err = OpenIntegrationFromEnvironment(t.Context(), "accounts-product")
		if err != nil {
			t.Fatal(err)
		}
		host, err = Open(t.Context(), Options{DatabasePath: filepath.Join(root, "agent.db"), RuntimeID: "accounts-product", WorkspaceID: "accounts-workspace", ApplicationKey: "accounts-product", Integration: binding})
		if err != nil {
			t.Fatal(err)
		}
		adapters, err := host.IntegrationAdapters()
		if err != nil {
			t.Fatal(err)
		}
		handler, err = gateway.NewHandler(gateway.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: "accounts-product", WorkspaceID: "accounts-workspace", ApplicationKey: "accounts-product", Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("product")}, "oauth-callback.html": {Data: []byte("static callback")}}, ModuleAdapters: adapters, ApplicationRoutes: host.AccountSetupRoutes(), NavigationFiles: map[string]string{"/oauth/callback": "oauth-callback.html"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(input)
		request := httptest.NewRequest(method, "http://127.0.0.1:8091"+path, strings.NewReader(string(raw)))
		request.Header.Set("Origin", "http://127.0.0.1:8091")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Agent-Scope", scope)
		request.Header.Set("Idempotency-Key", rand.Text())
		for _, c := range cookies {
			request.AddCookie(c)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, request)
		for _, c := range out.Result().Cookies() {
			cookies[c.Name] = c
		}
		if out.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, out.Code, out.Body.String())
		}
		return out.Body.Bytes()
	}
	login := func(password string) {
		call("POST", "/auth/login", map[string]string{"login": "admin@example.com", "password": password}, 200)
		var s struct{ Scope string }
		json.Unmarshal(call("GET", "/app/session", nil, 200), &s)
		scope = s.Scope
	}
	start()
	login("Initial-SaaS-Account!2")
	call("POST", "/auth/password/change", map[string]string{"current_password": "Initial-SaaS-Account!2", "new_password": "Changed-SaaS-Account!3"}, 200)
	call("POST", "/app/product/account-setup", map[string]any{}, 200)
	app := integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google SaaS", ClientID: "fixture-client", ClientSecret: "fixture-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{"https://www.googleapis.com/auth/calendar.readonly"}, Enabled: true}
	call("PUT", "/integration/oauth-applications/google", app, 200)
	var session integration.OAuthAuthorizationSession
	json.Unmarshal(call("POST", "/integration/oauth-authorizations", integration.OAuthAuthorizationInput{ApplicationKey: "google", Scope: integration.ConnectionAccountScopePersonal, Scopes: app.Scopes}, 200), &session)
	target, err := url.Parse(session.AuthorizationURL)
	if err != nil || target.Host != "accounts.google.com" || target.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("invalid official SaaS consent request")
	}
	stop()
	start()
	login("Changed-SaaS-Account!3")
	var restored integration.OAuthAuthorizationSession
	json.Unmarshal(call("GET", "/integration/oauth-authorizations/"+session.ID, nil, 200), &restored)
	if restored.Status != "pending" || restored.AuthorizationURL != "" {
		t.Fatal("unsafe or missing SaaS receipt")
	}
	json.Unmarshal(call("POST", "/integration/oauth-authorizations/callback", integration.OAuthAuthorizationCallback{State: target.Query().Get("state"), Error: "access_denied"}, 200), &restored)
	if restored.Status != "rejected" {
		t.Fatal("SaaS denial not persisted")
	}
	stop()
	start()
	login("Changed-SaaS-Account!3")
	json.Unmarshal(call("GET", "/integration/oauth-authorizations/"+session.ID, nil, 200), &restored)
	if restored.Status != "rejected" {
		t.Fatal("SaaS receipt changed across restart")
	}
	call("GET", "/integration/secrets", nil, 404)
}
