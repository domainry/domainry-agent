package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityremote "github.com/domainry/domainry-identity-sdk/remote"
	identitycapability "github.com/domainry/domainry-identity/capability"
)

// This opt-in acceptance builds and starts the actual standalone Identity
// command on loopback. No private Identity implementation is imported by Agent.
func TestSaaSConversationAgainstRealIdentityProcess(t *testing.T) {
	if os.Getenv("AGENT_IDENTITY_PROCESS_ACCEPTANCE") != "1" {
		t.Skip("set AGENT_IDENTITY_PROCESS_ACCEPTANCE=1 to run the isolated Identity process")
	}
	clearAgentEnvironment(t)
	const initial, changed = "Initial-C03-SaaS!2", "Changed-C03-SaaS!3"
	const credential = "c03-isolated-identity-application-credential"
	evidence := os.Getenv("AGENT_C03_EVIDENCE_DIR")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	identityRoot := filepath.Join(filepath.Dir(root), "domainry-identity")
	binary := filepath.Join(t.TempDir(), "identity-server")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/identity-server")
	build.Dir = identityRoot
	build.Env = append(os.Environ(), "GOWORK="+filepath.Join(root, "go.work"))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real Identity: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	issuer := "http://" + listener.Addr().String()
	_ = listener.Close()
	log, err := os.OpenFile(filepath.Join(evidence, "identity-process.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	process := exec.Command(binary)
	process.Dir = identityRoot
	// Only test-specific configuration enters this child; unrelated service
	// credentials from the developer environment are not inherited.
	environment := map[string]string{"PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME"), "TMPDIR": os.Getenv("TMPDIR"), "APP_ENV": "development", "DATABASE_DRIVER": "sqlite", "APP_DB_PATH": filepath.Join(t.TempDir(), "identity.db"), "TEMPLATE_MANIFEST": filepath.Join(identityRoot, "domainry.template.json"), "HTTP_BIND_HOST": "127.0.0.1", "PORT": port, "AUTH_ISSUER": issuer, "AUTH_AUDIENCE": "agent-runtime", "AUTH_DEFAULT_PASSWORD": initial, "AUTH_JWT_SECRET": "c03-isolated-signing-key-at-least-32-bytes", "IDENTITY_DATA_SECRET_KEY": "c03-isolated-data-encryption-key-32-bytes", "IDENTITY_WORKSPACE_ID": "workspace", "IDENTITY_APPLICATION_SERVICE_CREDENTIALS": "workspace/agent-runtime=" + credential}
	for key, value := range environment {
		process.Env = append(process.Env, key+"="+value)
	}
	process.Stdout, process.Stderr = log, log
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() {
		_ = process.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = process.Process.Kill()
			<-done
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	ready := false
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-done:
			done <- err
			t.Fatalf("Identity process exited: %v; see %s", err, filepath.Join(evidence, "identity-process.log"))
		default:
		}
		response, err := client.Get(issuer + "/identity/discovery")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("Identity process did not become ready")
	}
	capability, err := identitycapability.Open(identitycapability.Inputs{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := capability.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config := identityremote.Config{Endpoint: issuer, WorkspaceID: "workspace", Audience: "agent-runtime", Issuer: issuer, ServiceAccessToken: credential, CapabilityContractSHA256: summary.Identity.ContractSHA256}
	identity, err := identityremote.NewFactory(config).Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: "workspace", ApplicationKey: "agent-runtime"})
	if err != nil {
		t.Fatal("open real Identity SDK", err)
	}
	defer identity.Close(context.Background())
	scope := identitysdk.ApplicationRef{WorkspaceID: "workspace", ApplicationKey: "agent-runtime"}
	if _, err = identity.Applications().Register(t.Context(), identitysdk.ApplicationRegistration{Application: scope}); err != nil {
		t.Fatal("register test application", err)
	}
	login := func(account string) identitysdk.AuthSession {
		session, err := identity.Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{WorkspaceID: "workspace", ApplicationKey: "agent-runtime", Login: account, Password: initial})
		if err != nil {
			t.Fatal("login test account", err)
		}
		session, err = identity.Credentials().ChangePassword(t.Context(), identitysdk.ChangePasswordRequest{AccessToken: session.AccessToken, CurrentPassword: initial, NewPassword: changed, IdempotencyKey: account + "-change"})
		if err != nil {
			t.Fatal("change test password", err)
		}
		return session
	}
	admin, owner := login("admin@example.com"), login("system_administrator@example.com")
	setActive := func(active bool) {
		action := "disable"
		if active {
			action = "enable"
		}
		request, err := http.NewRequestWithContext(t.Context(), "POST", issuer+"/identity/users/"+owner.User.ID+"/"+action, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+admin.AccessToken)
		request.Header.Set("X-Workspace-ID", "workspace")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("%s-%d", action, time.Now().UnixNano()))
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 204 {
			var body map[string]any
			_ = json.NewDecoder(response.Body).Decode(&body)
			t.Fatalf("Identity %s: %d %v", action, response.StatusCode, body["code"])
		}
	}
	// A transport gate delays, but never substitutes, actual principal resolution.
	target, _ := url.Parse(issuer)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var resolutions atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity/principal/resolve" && resolutions.Add(1) == 2 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer proxyServer.Close()
	var modelCalls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Current owner verified.\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	for key, value := range map[string]string{"IDENTITY_ENDPOINT": proxyServer.URL, "IDENTITY_WORKSPACE_ID": "workspace", "IDENTITY_AUDIENCE": "agent-runtime", "IDENTITY_ISSUER": issuer, "IDENTITY_SERVICE_ACCESS_TOKEN": credential, "IDENTITY_CAPABILITY_CONTRACT_SHA256": summary.Identity.ContractSHA256, "AGENT_SAAS_API_KEY": "agent-service-key", "AGENT_SAAS_RUNTIME_ID": "runtime", "AGENT_CONVERSATION_PROVIDER": "gateway", "AGENT_CONVERSATION_PROTOCOL": "chat_completions", "AGENT_CONVERSATION_MODEL": "fixture", "AGENT_PROVIDER_API_KEY": "model-key", "AGENT_CONVERSATION_BASE_URL": model.URL} {
		t.Setenv(key, value)
	}
	store := executableConversationStore(t)
	var current atomic.Pointer[agentserver.Server]
	server, closeService, err := openService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { closeService() }()
	current.Store(server)
	agentHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current.Load().Handler().ServeHTTP(w, r) }))
	defer agentHTTP.Close()
	remote, err := agentremote.NewFactory(agentremote.Options{BaseURL: agentHTTP.URL, APIKey: "agent-service-key", Client: agentHTTP.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, executableRuntime("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close(context.Background())
	service := remote.(agentsdk.ConversationBinding).Conversations()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: owner.User.ID, RoleKey: owner.DefaultRole}
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "real-identity"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "authorized before queue"}, a)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("queued worker did not reach actual principal service")
	}
	setActive(false)
	close(release)
	wait := func(conversationID, runID string) agentsdk.ConversationRun {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			run, err := service.Run(t.Context(), conversationID, runID, a)
			if err != nil {
				t.Fatal(err)
			}
			if run.Terminal() {
				return run
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("real Identity run did not finish")
		return agentsdk.ConversationRun{}
	}
	failed := wait(c.ID, run.ID)
	if failed.Status != "failed" || modelCalls.Load() != 0 || failed.ErrorCode != "execution_access_denied" && failed.ErrorCode != "execution_authorization_unavailable" {
		t.Fatalf("disabled real account reached model: %+v", failed)
	}
	closeService()
	// Seed an expired running lease through the real repository to represent a
	// previous process interrupted after freezing its input, before model I/O.
	repo := agentstore.NewConversationStore(store)
	recovery, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "orphaned-run"}, a)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := repo.Enqueue(t.Context(), recovery.ID, agentsdk.ConversationSend{ClientMessageID: "orphan", Message: "frozen before restart"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), "runtime", "previous-process", 300*time.Millisecond)
	if err != nil || !ok {
		t.Fatal("seed previous lease", err)
	}
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{{Role: "user", Content: "frozen before restart"}}, Purpose: "reply", IdempotencyKey: "orphaned-identity-proof", MaxOutputBytes: 8192}
	if _, _, err = repo.ModelInput(t.Context(), claim, &input); err != nil {
		t.Fatal(err)
	}
	server, closeService, err = openService(store)
	if err != nil {
		t.Fatal(err)
	}
	current.Store(server)
	if denied := wait(recovery.ID, orphan.ID); denied.Status != "failed" || modelCalls.Load() != 0 {
		t.Fatalf("recovery reused old Identity: %+v", denied)
	}
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err == nil {
		t.Fatal("disabled owner explicitly resumed")
	}
	setActive(true)
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	final := wait(c.ID, run.ID)
	if final.Status != "completed" || final.Attempt != 2 || modelCalls.Load() != 1 {
		t.Fatalf("restored real owner cannot resume: %+v", final)
	}
	messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 2 {
		t.Fatal("incorrect real-service message history", err)
	}
	t.Logf("real Identity process: disabled after enqueue, expired frozen lease rechecked on service restart, explicit resume after enable; final run=%s attempt=%d model_calls=%d principal_resolutions=%d", final.ID, final.Attempt, modelCalls.Load(), resolutions.Load())
}
