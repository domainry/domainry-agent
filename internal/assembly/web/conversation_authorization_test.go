package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
)

// Only scheduling is synthetic. Admission is delegated to the real host's
// current Identity resolver after the test changes account status over HTTP.
type executionAdmissionGate struct {
	host             atomic.Pointer[Host]
	mu               sync.Mutex
	entered, release chan struct{}
	last             agentsdk.ConversationExecutionAuthorizationRequest
}

func (g *executionAdmissionGate) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entered, g.release = make(chan struct{}), make(chan struct{})
}

func (g *executionAdmissionGate) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	g.mu.Lock()
	var release chan struct{}
	if in.Stage == "execute" && g.entered != nil {
		g.last = in
		select {
		case <-g.entered:
		default:
			close(g.entered)
		}
		release = g.release
	}
	g.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	host := g.host.Load()
	if host == nil {
		return false, nil
	}
	return host.AuthorizeConversationExecution(ctx, in)
}

type admissionModel struct{ calls atomic.Int32 }

func (m *admissionModel) GenerateConversation(_ context.Context, _ agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.calls.Add(1)
	return agentsdk.ConversationModelResult{Content: "当前身份已核验，继续完成资料整理。", Model: "admission-fixture"}, nil
}

func TestConversationCurrentIdentityAdmissionAndBrowser(t *testing.T) {
	const initial, changed = "Initial-Admission-Test!2", "Changed-Admission-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "admission-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "admission-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	gate := &executionAdmissionGate{}
	model := &admissionModel{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "admission.db"), RuntimeID: "admission-runtime", WorkspaceID: "admission-workspace", ApplicationKey: "admission-test", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{ExecutionAuthorizer: gate, Poll: 5 * time.Millisecond}}}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		gate.host.Store(host)
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "admission-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { _ = host.Close(context.Background()) }()
	admin := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", initial)
	admin.changePassword(initial, changed)
	owner := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	owner.login("system_administrator@example.com", initial)
	owner.changePassword(initial, changed)
	ownerID, _ := owner.readSession()["user_id"].(string)
	if ownerID == "" || ownerID == "admin" {
		t.Fatal("fixture owner identity missing")
	}
	setActive := func(active bool) {
		mux := http.NewServeMux()
		for _, adapter := range host.Identity.(identityhttpapi.Provider).HTTPAdapters() {
			for _, route := range adapter.Routes() {
				mux.Handle(route.Pattern(), adapter.Handler())
			}
		}
		operation := "disable"
		if active {
			operation = "enable"
		}
		r := httptest.NewRequest("POST", "/identity/users/"+ownerID+"/"+operation, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+admin.cookies["domainry_agent_access"].Value)
		r.Header.Set("X-Workspace-ID", string(host.application.WorkspaceID))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", fmt.Sprintf("admission-%s-%d", operation, time.Now().UnixNano()))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 204 {
			t.Fatalf("Identity %s owner: %d %s", operation, w.Code, w.Body.String())
		}
	}
	waitRun := func(request agentsdk.ConversationExecutionAuthorizationRequest) agentsdk.ConversationRun {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			run, err := host.Agent.(agentsdk.ConversationBinding).Conversations().Run(t.Context(), request.ConversationID, request.RunID, request.Authority)
			if err != nil {
				t.Fatal(err)
			}
			if run.Terminal() {
				return run
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("admission run did not finish")
		return agentsdk.ConversationRun{}
	}
	disableAndRelease := func() {
		gate.mu.Lock()
		entered := gate.entered
		gate.mu.Unlock()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not reach admission")
		}
		before := model.calls.Load()
		setActive(false)
		gate.mu.Lock()
		request := gate.last
		close(gate.release)
		gate.entered = nil
		gate.mu.Unlock()
		run := waitRun(request)
		if run.Status != "failed" || run.ErrorCode != "execution_access_denied" || model.calls.Load() != before {
			t.Fatalf("disabled principal reached model: %+v", run)
		}
		t.Logf("disabled live Identity principal: run=%s status=%s code=%s model_calls_while_disabled=0", run.ID, run.Status, run.ErrorCode)
	}
	created := owner.call("POST", "/agent/conversations", `{"client_id":"admission-preflight","title":"身份撤销验收"}`, 200)
	var conversation agentsdk.Conversation
	if err := json.Unmarshal(created.Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	gate.arm()
	base := "/agent/conversations/" + conversation.ID
	var run agentsdk.ConversationRun
	if err := json.Unmarshal(owner.call("POST", base+"/messages", `{"client_message_id":"send","message":"开始整理"}`, 202).Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	disableAndRelease()
	owner.call("GET", base, "", 401)
	setActive(true)
	owner.login("system_administrator@example.com", changed)
	owner.call("POST", base+"/runs/"+run.ID+"/resume", `{}`, 200)
	gate.mu.Lock()
	request := gate.last
	gate.mu.Unlock()
	if final := waitRun(request); final.Status != "completed" || model.calls.Load() != 1 {
		t.Fatalf("current owner cannot resume: %+v", final)
	}
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		open()
		admin.handler, owner.handler = handler, handler
		admin.login("admin@example.com", changed)
	}
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){
		"arm_admission":             gate.arm,
		"disable_and_release_owner": disableAndRelease,
		"restore_owner":             func() { setActive(true) },
		"restart_admission_host":    reopen,
	})
}
