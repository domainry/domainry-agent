package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type outcomeExternalModel struct{ operationScopeWebModel }

func (outcomeExternalModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	message := agentsdk.ConversationStepMessage{Role: "assistant", Content: "已读取两个外部操作的确定回执。"}
	finish := "stop"
	if last.Role != "tool" {
		finish = "tool_calls"
		message.Content = ""
		message.ToolCalls = []agentsdk.ConversationToolCall{{ID: "external-known", Name: "create_fixture_record", Arguments: `{"title":"known"}`}, {ID: "external-unknown", Name: "create_fixture_record", Arguments: `{"title":"lose-response"}`}}
	}
	return agentsdk.ConversationStepResult{FinishReason: finish, Model: "external-outcome-fixture", Message: message}, nil
}

type outcomeHTTPToolHost struct {
	*confirmationWebFixture
	url string
}

func (f *outcomeHTTPToolHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if in.Confirmation == nil {
		return agentsdk.ConversationToolResult{}, errors.New("confirmation missing")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", f.url+"/records", bytes.NewBufferString(in.Call.Arguments))
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	req.Header.Set("Idempotency-Key", in.IdempotencyKey)
	// Keep one HTTP attempt per tool invocation. Go's transport otherwise
	// retries replayable POST bodies carrying Idempotency-Key on a lost socket.
	req.GetBody = nil
	return f.request(req)
}
func (f *outcomeHTTPToolHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", f.url+"/receipts", nil)
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	req.Header.Set("Idempotency-Key", in.IdempotencyKey)
	return f.request(req)
}
func (*outcomeHTTPToolHost) request(req *http.Request) (agentsdk.ConversationToolResult, error) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return agentsdk.ConversationToolResult{}, errors.New("receipt unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1024))
	if err != nil {
		return agentsdk.ConversationToolResult{}, err
	}
	var value struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &value) != nil || value.ID == "" {
		return agentsdk.ConversationToolResult{}, errors.New("invalid receipt")
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: raw, ResourceID: value.ID}, nil
}

// The external HTTP service is an isolated protocol fixture, not a real
// third-party API. It persists actual effects to files and drops the second
// POST response after commit. Recovery must GET the receipt with the same key.
func TestExecutionOutcomeExternalHTTPRecoveryAndBrowser(t *testing.T) {
	const initial, changed = "Initial-Outcome-Test!2", "Changed-Outcome-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "external-outcome-test-signing-secret-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "external-outcome-test-data-secret-32bytes-long")
	t.Setenv("APP_ENV", "development")
	dir := t.TempDir()
	var mu sync.Mutex
	posts := map[string]int{}
	gets := map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			http.Error(w, "missing key", 400)
			return
		}
		hash := sha256.Sum256([]byte(key))
		id := hex.EncodeToString(hash[:])
		path := filepath.Join(dir, id+".json")
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/records" {
			posts[key]++
			var args struct {
				Title string `json:"title"`
			}
			if json.NewDecoder(r.Body).Decode(&args) != nil {
				http.Error(w, "bad args", 400)
				return
			}
			raw, _ := json.Marshal(map[string]string{"id": "record-" + id[:16]})
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err == nil {
				_, err = file.Write(raw)
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
			} else if os.IsExist(err) {
				err = nil
			}
			if err != nil {
				t.Error(err)
				http.Error(w, "write failed", 500)
				return
			}
			if args.Title == "lose-response" {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/receipts" {
			gets[key]++
			raw, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, "unknown", 404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	fixture := &outcomeHTTPToolHost{confirmationWebFixture: &confirmationWebFixture{}, url: upstream.URL}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "external-outcomes.db"), RuntimeID: "external-outcome-runtime", WorkspaceID: "external-outcome-workspace", ApplicationKey: "external-outcome-app", Agent: agentmodule.Options{ConversationProvider: outcomeExternalModel{}, ConversationOptions: agentmodule.ConversationOptions{ToolHost: fixture, Poll: 5 * time.Millisecond}}}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		fixture.host = host
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { _ = host.Close(context.Background()) }()
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	permission := identitysdk.PermissionDefinition{PermissionKey: "agent.fixture_records.create", ResourceKey: "agent.fixture_records", OperationKey: "create", Label: "Isolated external outcome fixture", Category: "Acceptance test", SourceKind: "agent_tool"}
	registration, err := identitysdk.NewPermissionReconcileRequest(host.application, "agent:outcome_fixture", "", []identitysdk.PermissionDefinition{permission})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Identity.Permissions().Reconcile(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		return append(previous, identitysdk.ProjectRolePermission{PermissionKey: permission.PermissionKey, DataScope: identitysdk.DataScopeOwner})
	})
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		open()
		b.handler = handler
		b.login("admin@example.com", changed)
	}
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"external-outcome-http"}`, 200).Body.Bytes(), &c)
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", `{"client_message_id":"two","message":"创建两个外部记录"}`, 202).Body.Bytes(), &run)
	path := "/agent/conversations/" + c.ID + "/runs/" + run.ID
	outcomeApprove(t, b, path, outcomeWait(t, b, path, "waiting_confirmation"), "listed_operations")
	unknown := outcomeWait(t, b, path, "needs_reconciliation")
	if unknown.Steps[0].Calls[0].Status != "completed" || unknown.Steps[0].Calls[1].Status != "needs_reconciliation" {
		t.Fatal("partial unknown results missing", unknown.Steps)
	}
	reopen()
	b.call("POST", path+"/resume", "", 200)
	done := outcomeWait(t, b, path, "completed")
	if done.Steps[0].Calls[1].ResourceID == "" {
		t.Fatal("reconciled receipt absent")
	}
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){"restart_outcome_host": reopen})
	mu.Lock()
	defer mu.Unlock()
	if len(posts) < 2 || len(gets)*2 != len(posts) {
		t.Fatalf("expected two POSTs and one reconciliation per run: posts=%v gets=%v", posts, gets)
	}
	for key, count := range posts {
		if count != 1 {
			t.Fatalf("external effect retried with key %q: %d", key, count)
		}
	}
	for key := range gets {
		if posts[key] != 1 {
			t.Fatal("reconciliation changed idempotency key")
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != len(posts) {
		t.Fatal("external effect count mismatch", err)
	}
	t.Logf("actual loopback HTTP protocol: %d persisted external records, one POST per key, %d reconciled keys, response dropped after commit; no repeated POST after complete host restart", len(posts), len(gets))
}
