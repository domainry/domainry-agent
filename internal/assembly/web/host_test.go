package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type testModel struct{}

func (testModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{Content: "Identity module reply", Model: "test"}, nil
}

type browser struct {
	t       *testing.T
	handler http.Handler
	cookies map[string]*http.Cookie
	scope   string
}

type pausedStream struct {
	*httptest.ResponseRecorder
	first  chan struct{}
	resume chan struct{}
	paused bool
}

func (w *pausedStream) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if !w.paused {
		w.paused = true
		close(w.first)
		<-w.resume
	}
	return n, err
}

func (b *browser) call(method, path, body string, want int) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:8091"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://127.0.0.1:8091")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Agent-Scope", b.scope)
	r.Header.Set("Idempotency-Key", rand.Text())
	for _, cookie := range b.cookies {
		r.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	b.handler.ServeHTTP(out, r)
	for _, cookie := range out.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	if out.Code != want {
		b.t.Fatalf("%s %s: status %d, want %d, body %s", method, path, out.Code, want, out.Body.String())
	}
	return out
}
func (b *browser) login(login, password string) {
	b.t.Helper()
	raw, _ := json.Marshal(map[string]string{"login": login, "password": password})
	out := b.call("POST", "/auth/login", string(raw), 200)
	if strings.Contains(out.Body.String(), "access_token") || strings.Contains(out.Body.String(), "refresh_token") {
		b.t.Fatal("credentials leaked to JavaScript")
	}
	for _, name := range []string{"domainry_agent_access", "domainry_agent_refresh"} {
		cookie := b.cookies[name]
		if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
			b.t.Fatalf("invalid %s cookie", name)
		}
	}
	b.readSession()
}
func (b *browser) readSession() map[string]any {
	b.t.Helper()
	var state map[string]any
	if err := json.Unmarshal(b.call("GET", "/app/session", "", 200).Body.Bytes(), &state); err != nil {
		b.t.Fatal(err)
	}
	b.scope, _ = state["scope"].(string)
	return state
}
func (b *browser) changePassword(old, password string) {
	b.t.Helper()
	raw, _ := json.Marshal(map[string]string{"current_password": old, "new_password": password})
	b.call("POST", "/auth/password/change", string(raw), 200)
	b.readSession()
}

func TestIdentityModulesBrowserOwnershipAndRestart(t *testing.T) {
	const initial = "Initial-Agent-Test-Password!2"
	const changed = "Changed-Agent-Test-Password!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "web.db"), RuntimeID: "web-test", WorkspaceID: "workspace-one", ApplicationKey: "agent-test", Agent: agentmodule.Options{ConversationProvider: testModel{}}}
	open := func() (*Host, http.Handler) {
		t.Helper()
		h, err := Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: h.Identity, Agent: h.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "test", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			_ = h.Close(t.Context())
			t.Fatal(err)
		}
		return h, handler
	}
	host, handler := open()
	defer func() { _ = host.Close(context.Background()) }()
	if _, err := Open(t.Context(), options); err == nil {
		t.Fatal("second host acquired the same database")
	}
	a := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	a.call("GET", "/agent/conversations", "", 401)
	a.login("admin@example.com", initial)
	a.call("GET", "/agent/conversations", "", 403) // mandatory first password change
	a.changePassword(initial, changed)
	oldAccess := *a.cookies["domainry_agent_access"]
	created := a.call("POST", "/agent/conversations", `{"client_id":"identity-test-A","title":"A private conversation"}`, 200)
	var conversation agentsdk.Conversation
	if err := json.Unmarshal(created.Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	if conversation.UserID != "admin" || conversation.WorkspaceID != options.WorkspaceID {
		t.Fatalf("wrong owner: %#v", conversation)
	}
	base := "/agent/conversations/" + conversation.ID
	out := a.call("POST", base+"/messages", `{"client_message_id":"test-message-1","message":"remember A"}`, 202)
	var run agentsdk.ConversationRun
	if err := json.Unmarshal(out.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		_ = json.Unmarshal(a.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
	}
	if !run.Terminal() {
		t.Fatal("run did not finish")
	}
	a.call("PUT", "/agent/conversations/memories/test-memory", `{"title":"Private preference","content":"A only","enabled":true}`, 200)
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("system_administrator@example.com", initial)
	b.changePassword(initial, changed)
	if b.scope == a.scope {
		t.Fatal("two accounts share a draft scope")
	}
	for _, path := range []string{base, base + "/messages", base + "/runs/" + run.ID, base + "/runs/" + run.ID + "/events", base + "/runs/" + run.ID + "/events/stream?scope=" + b.scope} {
		b.call("GET", path, "", 404)
	}
	if body := b.call("GET", "/agent/conversations/memories", "", 200).Body.String(); strings.Contains(body, "A only") {
		t.Fatal("cross-user memory leak")
	}
	b.call("DELETE", "/agent/conversations/memories/test-memory?expected_revision=1", "", 409)
	if body := a.call("GET", "/agent/conversations/memories", "", 200).Body.String(); !strings.Contains(body, "A only") {
		t.Fatal("another user deleted the memory")
	}
	b.scope = a.scope
	b.call("POST", "/agent/conversations", `{"title":"stale tab"}`, 409)
	b.readSession()
	// Auth and conversation requests reject cross-origin and missing-origin writes.
	for _, origin := range []string{"", "http://evil.example"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8091/auth/logout", strings.NewReader(`{}`))
		r.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code != 403 {
			t.Fatal("unsafe origin accepted")
		}
	}
	refreshBefore := a.cookies["domainry_agent_refresh"].Value
	a.call("POST", "/auth/refresh", `{}`, 200)
	if a.cookies["domainry_agent_refresh"].Value == refreshBefore {
		t.Fatal("refresh cookie did not rotate")
	}
	// Revoke a session while its SSE response is open. Later persisted events
	// must not be delivered through an already-authenticated connection.
	stream := &pausedStream{ResponseRecorder: httptest.NewRecorder(), first: make(chan struct{}), resume: make(chan struct{})}
	streamRequest := httptest.NewRequest("GET", "http://127.0.0.1:8091"+base+"/runs/"+run.ID+"/events/stream?scope="+a.scope, nil)
	for _, cookie := range a.cookies {
		streamRequest.AddCookie(cookie)
	}
	finished := make(chan struct{})
	go func() { handler.ServeHTTP(stream, streamRequest); close(finished) }()
	select {
	case <-stream.first:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE did not open")
	}
	a.call("POST", "/auth/logout", `{}`, 204)
	close(stream.resume)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked SSE did not close")
	}
	if strings.Contains(stream.Body.String(), "run.completed") || strings.Contains(stream.Body.String(), "Identity module reply") {
		t.Fatal("revoked SSE delivered later events")
	}
	a.cookies["domainry_agent_access"] = &oldAccess
	a.call("GET", "/agent/conversations", "", 401) // signed but revoked token
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	host, handler = open()
	a.handler = handler
	a.login("admin@example.com", changed)
	a.call("GET", base, "", 200)
	// Same user identifier in a different workspace must not see the first one.
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	options.WorkspaceID = "workspace-two"
	host, handler = open()
	c := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	c.login("admin@example.com", initial)
	c.changePassword(initial, changed)
	c.call("GET", base, "", 404)
	if c.scope == a.scope {
		t.Fatal("workspace draft scope collision")
	}
}
