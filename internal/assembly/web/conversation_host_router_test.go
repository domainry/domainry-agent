package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	agentweb "github.com/domainry/domainry-agent/web"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestConversationBrowserUsesVerifiedScopeAndHostAdmission(t *testing.T) {
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Host-Router-Initial!2026")
	t.Setenv("AUTH_JWT_SECRET", "host-router-signing-secret-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "host-router-data-secret-32bytes-long")
	t.Setenv("APP_ENV", "development")
	host, err := Open(t.Context(), Options{DatabasePath: filepath.Join(t.TempDir(), "web.db"), RuntimeID: "router-runtime", WorkspaceID: "router-workspace", ApplicationKey: "router-app"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	called := 0
	var b *browser
	handler, err := agentweb.NewHandler(agentweb.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: "router-runtime", WorkspaceID: "router-workspace", ApplicationKey: "router-app", Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("test UI")}}, ConversationHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		_, ok := identitysdk.RequestIdentityFromContext(r.Context())
		if ok || r.Header.Get("Authorization") != "Bearer "+b.cookies["domainry_agent_access"].Value || r.Header.Get("X-Workspace-ID") != "router-workspace" || r.Header.Get("Cookie") != "" {
			t.Error("unverified browser identity forwarded")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"host.draining"}`))
	})})
	if err != nil {
		t.Fatal(err)
	}
	b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", "Host-Router-Initial!2026")
	b.changePassword("Host-Router-Initial!2026", "Host-Router-Changed!2026")
	r := httptest.NewRequest("POST", "http://127.0.0.1:8091/agent/conversations", strings.NewReader(`{"client_id":"blocked-by-host"}`))
	r.Header.Set("Origin", "http://127.0.0.1:8091")
	r.Header.Set("X-Agent-Scope", b.scope)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer untrusted-browser-header")
	r.Header.Set("X-Workspace-ID", "untrusted-workspace")
	for _, cookie := range b.cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if called != 1 || w.Code != 503 || !strings.Contains(w.Body.String(), "host.draining") {
		t.Fatal("host admission was bypassed", called, w.Code, w.Body.String())
	}
}
