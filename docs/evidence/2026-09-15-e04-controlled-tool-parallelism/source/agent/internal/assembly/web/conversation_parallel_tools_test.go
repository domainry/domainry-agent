package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type parallelReadModel struct{ calls int }

func (parallelReadModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{Content: "parallel fixture", Model: "parallel-fixture"}, nil
}
func (parallelReadModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "parallel-fixture", Fingerprint: "parallel-fixture-v1"}
}
func (m parallelReadModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	if last.Role == "user" {
		values := []string{"one", "two", "three"}
		calls := make([]sdk.ConversationToolCall, m.calls)
		for index := range calls {
			calls[index] = sdk.ConversationToolCall{ID: "read-" + values[index], Name: "parallel_fixture_read", Arguments: `{"value":"` + values[index] + `"}`}
		}
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Model: "parallel-fixture", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}}, nil
	}
	if len(in.Messages) < m.calls+1 {
		return sdk.ConversationStepResult{}, errors.New("parallel results missing")
	}
	values := []string{"one", "two", "three"}
	for index, message := range in.Messages[len(in.Messages)-m.calls:] {
		if message.Role != "tool" || message.ToolCallID != "read-"+values[index] || !json.Valid([]byte(message.Content)) {
			return sdk.ConversationStepResult{}, errors.New("parallel results changed model order")
		}
	}
	return sdk.ConversationStepResult{FinishReason: "stop", Model: "parallel-fixture", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "两个独立读取均已完成。"}}, nil
}

type parallelReadHost struct {
	cancelMode bool
	parallel   bool
	started    chan struct{}
	startOnce  sync.Once
	mu         sync.Mutex
	active     int
	peak       int
	authorized map[string]bool
	authChecks atomic.Int32
	cancelled  atomic.Int32
}

func newParallelReadHost(cancelMode, parallel bool) *parallelReadHost {
	return &parallelReadHost{cancelMode: cancelMode, parallel: parallel, started: make(chan struct{}), authorized: map[string]bool{}}
}
func (h *parallelReadHost) definition() sdk.ConversationToolDefinition {
	parallelism := ""
	if h.parallel {
		parallelism = toolsdk.ToolParallelismIndependentRead
	}
	return sdk.ConversationToolDefinition{Key: "parallel_fixture_read", Version: "1", Description: "Read one isolated fixture value.", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`), ActionKey: "fixture.parallel.read", Effect: "read", Idempotency: "natural", Parallelism: parallelism, TimeoutMillis: 5000, MaxOutputBytes: 1024}
}
func (h *parallelReadHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	return []sdk.ConversationToolDefinition{h.definition()}, nil
}
func (h *parallelReadHost) AuthorizeConversationTool(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	if in.Call.ID != "" {
		h.mu.Lock()
		h.authorized[in.Call.ID] = true
		h.mu.Unlock()
		h.authChecks.Add(1)
	}
	return sdk.ConversationToolAuthorization{Granted: true, Revision: "fixture-r1"}, nil
}
func (h *parallelReadHost) InvokeConversationTool(ctx context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	h.mu.Lock()
	if !h.authorized[in.Call.ID] {
		h.mu.Unlock()
		return sdk.ConversationToolResult{}, errors.New("invoked before authorization")
	}
	h.active++
	h.peak = max(h.peak, h.active)
	if h.parallel && h.active == 2 {
		h.startOnce.Do(func() { close(h.started) })
	}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.active--
		h.mu.Unlock()
	}()
	if h.parallel {
		select {
		case <-h.started:
		case <-ctx.Done():
			h.cancelled.Add(1)
			return sdk.ConversationToolResult{}, ctx.Err()
		}
	}
	if h.cancelMode {
		<-ctx.Done()
		h.cancelled.Add(1)
		return sdk.ConversationToolResult{}, ctx.Err()
	}
	var input struct {
		Value string `json:"value"`
	}
	if json.Unmarshal([]byte(in.Call.Arguments), &input) != nil {
		return sdk.ConversationToolResult{}, errors.New("invalid fixture input")
	}
	time.Sleep(25 * time.Millisecond)
	raw, _ := json.Marshal(input)
	return sdk.ConversationToolResult{Status: "completed", Content: raw, ResourceID: input.Value}, nil
}
func (h *parallelReadHost) ReconcileConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return sdk.ConversationToolResult{}, errors.New("read reconciliation is invalid")
}

func TestIndependentReadToolsRunConcurrentlyInOrderAndCancelTogether(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		calls, limit, peak, batches int
		parallel, cancel            bool
	}{{"complete", 2, 2, 2, 1, true, false}, {"bounded", 3, 2, 2, 2, true, false}, {"legacy_serial", 2, 2, 1, 0, false, false}, {"cancel", 2, 2, 2, 1, true, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			const initial, changed = "Initial-Parallel-Tools!2", "Changed-Parallel-Tools!3"
			t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
			t.Setenv("AUTH_JWT_SECRET", "parallel-tools-test-signing-secret-long-enough")
			t.Setenv("IDENTITY_DATA_SECRET_KEY", "parallel-tools-test-data-secret-long-enough")
			t.Setenv("APP_ENV", "development")
			fixture := newParallelReadHost(scenario.cancel, scenario.parallel)
			options := Options{DatabasePath: filepath.Join(t.TempDir(), "parallel.db"), RuntimeID: "parallel-runtime", WorkspaceID: "parallel-workspace", ApplicationKey: "parallel-app", Agent: agentmodule.Options{ConversationProvider: parallelReadModel{calls: scenario.calls}, ConversationOptions: agentmodule.ConversationOptions{ToolHost: fixture, Poll: 5 * time.Millisecond, MaxParallelTools: scenario.limit}}}
			host, err := Open(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("parallel")}}})
			if err != nil {
				t.Fatal(err)
			}
			browser := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
			browser.login("admin@example.com", initial)
			browser.changePassword(initial, changed)
			var conversation sdk.Conversation
			_ = json.Unmarshal(browser.call("POST", "/agent/conversations", `{"client_id":"parallel-read"}`, 200).Body.Bytes(), &conversation)
			var run sdk.ConversationRun
			_ = json.Unmarshal(browser.call("POST", "/agent/conversations/"+conversation.ID+"/messages", `{"client_message_id":"parallel-read","message":"读取两个相互独立的值"}`, 202).Body.Bytes(), &run)
			path := "/agent/conversations/" + conversation.ID + "/runs/" + run.ID
			if scenario.parallel {
				select {
				case <-fixture.started:
				case <-time.After(20 * time.Second):
					t.Fatal("explicit reads did not overlap")
				}
			}
			if scenario.cancel {
				browser.call("POST", path+"/cancel", "", 200)
			}
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
				_ = json.Unmarshal(browser.call("GET", path, "", 200).Body.Bytes(), &run)
				if run.Terminal() && (!scenario.cancel || fixture.cancelled.Load() == int32(scenario.calls)) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			fixture.mu.Lock()
			peak := fixture.peak
			fixture.mu.Unlock()
			executionMode := ""
			parallelCalls := 0
			if scenario.parallel {
				executionMode, parallelCalls = "parallel_read", scenario.calls
			}
			if peak != scenario.peak || fixture.authChecks.Load() < int32(scenario.calls) || len(run.Steps) == 0 || len(run.Steps[0].Calls) != scenario.calls || run.Steps[0].Calls[0].ID != "read-one" || run.Steps[0].Calls[1].ID != "read-two" || run.Steps[0].ToolExecution != executionMode || run.Metrics.ParallelToolBatches != scenario.batches || run.Metrics.ParallelToolCalls != parallelCalls || run.Metrics.PeakParallelTools != max(0, scenario.peak*boolInt(scenario.parallel)) || run.Metrics.AuthorizationChecks != scenario.calls {
				t.Fatalf("parallel run=%+v peak=%d checks=%d", run, peak, fixture.authChecks.Load())
			}
			if scenario.cancel {
				if run.Status != "cancelled" || fixture.cancelled.Load() != int32(scenario.calls) {
					t.Fatalf("cancel did not converge: run=%+v cancelled=%d", run, fixture.cancelled.Load())
				}
			} else if run.Status != "completed" || run.Steps[0].Calls[0].ResourceID != "one" || run.Steps[0].Calls[1].ResourceID != "two" {
				t.Fatalf("ordered independent results=%+v", run)
			}
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
