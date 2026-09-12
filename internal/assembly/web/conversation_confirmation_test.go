package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// Only this acceptance fixture mounts the write tool. Effects are confined to
// a temporary in-memory ledger. Production personal tools are unchanged.
type confirmationWebFixture struct {
	host                *Host
	mu                  sync.Mutex
	writes, reconciles  int
	correlationMismatch bool
	keys                map[string]bool
}

func (f *confirmationWebFixture) observeCorrelation(in agentsdk.ConversationToolRequest) {
	if in.RunID == "" {
		return
	}
	f.mu.Lock()
	f.correlationMismatch = f.correlationMismatch || in.CorrelationID != in.RunID
	f.mu.Unlock()
}

func confirmationFixtureDefinition() agentsdk.ConversationToolDefinition {
	return agentsdk.ConversationToolDefinition{Key: "create_fixture_record", Version: "1", Description: "Create a record in the isolated acceptance fixture after user confirmation.", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":128}},"required":["title"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`), ActionKey: "agent.fixture_records.create", Effect: "write", Idempotency: "reconcile", TimeoutMillis: 1000, MaxOutputBytes: 1024}
}
func (f *confirmationWebFixture) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	d := confirmationFixtureDefinition()
	auth, err := f.host.authorizeConversationAction(ctx, a, d.ActionKey)
	if err != nil {
		return nil, err
	}
	if !auth.Granted {
		return nil, nil
	}
	return []agentsdk.ConversationToolDefinition{d}, nil
}
func (f *confirmationWebFixture) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	f.observeCorrelation(in)
	auth, err := f.host.authorizeConversationAction(ctx, in.Authority, in.Definition.ActionKey)
	if in.ConversationID != "" {
		auth.ConfirmationRequired = in.Confirmation == nil
	}
	return auth, err
}
func (f *confirmationWebFixture) AuthorizeConversationInteraction(ctx context.Context, a agentsdk.ConversationAuthority, i agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return f.host.AuthorizeConversationInteraction(ctx, a, i)
}
func (f *confirmationWebFixture) InvokeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	f.observeCorrelation(in)
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.Confirmation == nil {
		return agentsdk.ConversationToolResult{}, fmt.Errorf("missing receipt")
	}
	if !f.keys[in.IdempotencyKey] {
		f.keys[in.IdempotencyKey] = true
		f.writes++
	}
	return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}, nil
}
func (f *confirmationWebFixture) ReconcileConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	f.observeCorrelation(in)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciles++
	if !f.keys[in.IdempotencyKey] {
		return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}, nil
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"fixture-record"}`), ResourceID: "fixture-record"}, nil
}

func confirmationModelService(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role != "tool" {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "fixture-write", "type": "function", "function": map[string]any{"name": "create_fixture_record", "arguments": `{"title":"周报验收事项"}`}}}}, "")
			write(map[string]any{}, "tool_calls")
		} else {
			if !strings.Contains(last.Content, "fixture-record") {
				t.Error("missing reconciled result")
			}
			write(map[string]any{"content": "已核查：验收事项已创建。"}, "")
			write(map[string]any{}, "stop")
		}
		usage, _ := json.Marshal(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
	}))
}

func TestConversationConfirmationThroughIdentityHTTPAndReconciliation(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	fixture := &confirmationWebFixture{keys: map[string]bool{}}
	upstream := confirmationModelService(t)
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "confirmation.db"), RuntimeID: "confirmation-runtime", WorkspaceID: "confirmation-workspace", ApplicationKey: "confirmation-app", Agent: agentmodule.Options{ConversationURL: upstream.URL, ConversationModel: "confirmation-fixture", ConversationOptions: agentmodule.ConversationOptions{ToolHost: fixture, Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	fixture.host = host
	permission := identitysdk.PermissionDefinition{PermissionKey: "agent.fixture_records.create", ResourceKey: "agent.fixture_records", OperationKey: "create", Label: "Create an isolated acceptance record", Category: "Acceptance test", SourceKind: "agent_tool"}
	registration, err := identitysdk.NewPermissionReconcileRequest(host.application, "agent:acceptance_fixture", "", []identitysdk.PermissionDefinition{permission})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Identity.Permissions().Reconcile(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "confirmation-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		return append(previous, identitysdk.ProjectRolePermission{PermissionKey: permission.PermissionKey, DataScope: identitysdk.DataScopeOwner})
	})
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"confirmation-http"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"one","message":"创建验收事项"}`, 202).Body.Bytes(), &run)
	path := base + "/runs/" + run.ID
	waitState := func(status string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			_ = json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &run)
			if run.Status == status {
				return
			}
			if run.Terminal() {
				t.Fatalf("expected %s got %+v", status, run)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("never reached %s", status)
	}
	waitState("waiting_confirmation")
	fixture.mu.Lock()
	if fixture.writes != 0 {
		t.Error("write before confirmation")
	}
	fixture.mu.Unlock()
	response := agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "approval", ExpectedRevision: run.Interaction.Revision, Decision: "approve"}
	raw, _ := json.Marshal(response)
	b.call("POST", path+"/respond", string(raw), 200)
	waitState("needs_reconciliation")
	b.call("POST", path+"/respond", string(raw), 200)
	b.call("POST", path+"/resume", "", 200)
	waitState("completed")
	fixture.mu.Lock()
	if fixture.writes != 1 || fixture.reconciles != 1 {
		t.Error("repeated external effect")
	}
	fixture.mu.Unlock()
	if run.Interaction.Status != "resolved" {
		t.Fatal("not resolved")
	}
	if run.CorrelationID != run.ID || run.StartedAt == nil || run.CompletedAt == nil || run.Metrics.ModelCalls != 2 || run.Metrics.ToolCalls != 1 || run.Metrics.AuthorizationChecks < 2 || run.Metrics.ConfirmationDecisions != 1 || run.DurationMilliseconds <= 0 || run.Usage["total_tokens"] != float64(20) {
		t.Fatalf("missing run audit detail %+v", run)
	}
	confirmation := run.Steps[0].Calls[0].Confirmation
	if confirmation == nil || confirmation.Status != "approved" || confirmation.RespondedBy == "" || confirmation.RespondedAt == nil {
		t.Fatalf("missing confirmation audit %+v", confirmation)
	}
	auditJSON, _ := json.Marshal(run.Audit)
	if strings.Contains(string(auditJSON), "周报验收事项") || strings.Contains(string(auditJSON), "fixture-record") {
		t.Fatalf("audit leaked execution payload %s", auditJSON)
	}
	sse := b.call("GET", path+"/events/stream?scope="+b.scope, "", 200).Body.String()
	for _, event := range []string{"run.waiting_confirmation", "run.needs_reconciliation", "interaction.resolved", "run.completed"} {
		if !strings.Contains(sse, event) {
			t.Error("missing event", event)
		}
	}
	servePersonalToolAcceptance(t, host, options)
}

func TestRunAuditBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_RUN_AUDIT_BROWSER") != "1" {
		t.Skip("opt-in built run audit acceptance")
	}
	project, _ := filepath.Abs("../../..")
	output := os.Getenv("AGENT_UI_TEST_OUTPUT")
	if output == "" {
		t.Fatal("AGENT_UI_TEST_OUTPUT required")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	modelService := confirmationModelService(t)
	defer modelService.Close()
	tool := &confirmationWebFixture{keys: map[string]bool{}}
	f := newAccountFixture(t)
	f.close()
	f.options.Agent = agentmodule.Options{ConversationURL: modelService.URL, ConversationModel: "run-audit-fixture", ConversationOptions: agentmodule.ConversationOptions{ToolHost: tool, Poll: 5 * time.Millisecond}}
	f.open()
	tool.host = f.host
	permission := identitysdk.PermissionDefinition{PermissionKey: "agent.fixture_records.create", ResourceKey: "agent.fixture_records", OperationKey: "create", Label: "Create an isolated acceptance record", Category: "Acceptance test", SourceKind: "agent_tool"}
	registration, err := identitysdk.NewPermissionReconcileRequest(f.host.application, "agent:run_audit_fixture", "", []identitysdk.PermissionDefinition{permission})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Identity.Permissions().Reconcile(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	admin := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", accountInitial)
	admin.changePassword(accountInitial, accountChanged)
	grantPersonalTools(t, f.host, admin, true)
	mutateTestRolePermissions(t, f.host, admin, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		return append(previous, identitysdk.ProjectRolePermission{PermissionKey: permission.PermissionKey, DataScope: identitysdk.DataScopeOwner})
	})

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	handler := f.boundary(origin, files)
	var gate sync.RWMutex
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__acceptance/restart" {
			gate.Lock()
			defer gate.Unlock()
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			f.close()
			f.open()
			tool.host = f.host
			handler = f.boundary(origin, files)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		gate.RLock()
		defer gate.RUnlock()
		handler.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/run-audit.browser.mjs"))
	command.Dir = project
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal("run audit browser", err)
	}
	tool.mu.Lock()
	defer tool.mu.Unlock()
	if tool.writes != 1 || tool.reconciles != 1 || tool.correlationMismatch {
		t.Fatalf("effects writes=%d reconciles=%d correlation_mismatch=%t", tool.writes, tool.reconciles, tool.correlationMismatch)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "agent_sqlite_restart": true, "model_protocol_usage": true, "external_effects": tool.writes, "reconciliations": tool.reconciles}, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "host-audit.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
