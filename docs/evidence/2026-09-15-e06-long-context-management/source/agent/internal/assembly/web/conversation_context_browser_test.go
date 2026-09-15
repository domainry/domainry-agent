package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type contextBrowserSource struct {
	definition sdk.ConversationContextSourceDefinition
	revokedOld *atomic.Bool
	reads      atomic.Int32
	authorizes atomic.Int32
}

func (s *contextBrowserSource) ConversationContextSourceDefinition() sdk.ConversationContextSourceDefinition {
	return s.definition
}

func (s *contextBrowserSource) ReadConversationContext(_ context.Context, request sdk.ConversationContextSourceRequest) (sdk.ConversationContextSourceContent, error) {
	if !request.Authority.Known || request.Authority.RuntimeID != "context-browser-runtime" || request.Authority.WorkspaceID != "context-browser-workspace" || request.Authority.UserID == "" || request.ConversationID == "" || request.RunID == "" || request.CurrentInput == "" {
		return sdk.ConversationContextSourceContent{}, errors.New("context source received an unbound request")
	}
	s.reads.Add(1)
	version, content := "project-v1", strings.Repeat("Keep the current user correction and cite current evidence. ", 12)
	switch s.definition.Kind {
	case sdk.ConversationContextKindBusinessRecord:
		base := 1
		if s.revokedOld.Load() {
			base = 4
		}
		if request.Purpose == "step" && request.Step > 0 {
			base += request.Step
		}
		version = fmt.Sprintf("record-v%d", base)
		content = fmt.Sprintf("quarter=Q%d; version=%s; rows=%s", base, version, strings.Repeat("current-record ", 35))
	case sdk.ConversationContextKindFileReference:
		version = "file-v1"
		if s.revokedOld.Load() {
			version = "file-v2"
		}
		content = `{"library_id":"current-library","document_id":"current-document","revision":"` + version + `"}`
	}
	return sdk.ConversationContextSourceContent{Version: version, Content: content, UpdatedAt: time.Date(2026, 9, 15, 10, 0, 0, int(s.reads.Load()), time.UTC)}, nil
}

func (s *contextBrowserSource) AuthorizeConversationContext(_ context.Context, request sdk.ConversationContextSourceRequest, reference sdk.ConversationContextSourceReference) error {
	s.authorizes.Add(1)
	if request.Authority.UserID == "" || reference.Key != s.definition.Key || reference.DefinitionHash == "" || reference.Version == "" {
		return errors.New("context authorization was not bound to the frozen source")
	}
	if s.definition.Kind == sdk.ConversationContextKindBusinessRecord && s.revokedOld.Load() && (reference.Version == "record-v1" || reference.Version == "record-v2" || reference.Version == "record-v3") {
		return &sdk.Error{Class: "forbidden", Code: "agent.conversation.context_source_revoked"}
	}
	return nil
}

type contextBrowserModel struct {
	compactionSeen atomic.Bool
	historyMasked  atomic.Bool
}

func (*contextBrowserModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{Content: "context fixture", Model: "context-fixture"}, nil
}

func (*contextBrowserModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: provider.ConversationProtocolChat, Model: "context-fixture", Fingerprint: "context-fixture-v1"}
}

func (m *contextBrowserModel) ConversationStepInputBytes(in sdk.ConversationStepRequest) (int, error) {
	return provider.ConversationStepInputBytes(provider.ConversationModelConfig{Provider: "fixture", Protocol: provider.ConversationProtocolChat, Model: "context-fixture", MaxOutputTokens: 1024}, in)
}

func (m *contextBrowserModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	text := ""
	toolIntervals := 0
	for _, message := range in.Messages {
		text += "\n" + message.Content
		if len(message.ToolCalls) > 0 {
			toolIntervals++
		}
	}
	usage := func(input, read, creation int) map[string]any {
		return map[string]any{"input_tokens": input, "output_tokens": 10, "cache_read_input_tokens": read, "cache_creation_input_tokens": creation}
	}
	if strings.Contains(text, `"version":"record-v4"`) {
		if !strings.Contains(text, "这条历史回复的资料来源当前无法验证，内容暂不提供。") || strings.Contains(text, "first context run completed") {
			return sdk.ConversationStepResult{}, errors.New("revoked historical context remained in the next model input")
		}
		m.historyMasked.Store(true)
		return sdk.ConversationStepResult{FinishReason: "stop", Model: "context-fixture", Usage: usage(80, 50, 0), Message: sdk.ConversationStepMessage{Role: "assistant", Content: "旧来源已撤回，已使用当前 record-v4 上下文。"}}, nil
	}
	if strings.Contains(text, "archived_execution_interval") {
		if !strings.Contains(text, `"version":"record-v3"`) || !strings.Contains(text, "保留这条用户修正") || toolIntervals != 1 {
			return sdk.ConversationStepResult{}, errors.New("compacted context lost its current source, user correction or latest interval")
		}
		m.compactionSeen.Store(true)
		return sdk.ConversationStepResult{FinishReason: "stop", Model: "context-fixture", Usage: usage(140, 100, 0), Message: sdk.ConversationStepMessage{Role: "assistant", Content: "first context run completed"}}, nil
	}
	call := "first"
	cacheRead, cacheCreation := 0, 100
	if toolIntervals == 1 {
		call, cacheRead, cacheCreation = "second", 80, 0
	} else if toolIntervals != 0 {
		return sdk.ConversationStepResult{}, errors.New("execution interval compaction did not occur")
	}
	return sdk.ConversationStepResult{
		FinishReason: "tool_calls", Model: "context-fixture", Usage: usage(100+20*toolIntervals, cacheRead, cacheCreation),
		Message: sdk.ConversationStepMessage{
			Role:          "assistant",
			Content:       call + " context read",
			ToolCalls:     []sdk.ConversationToolCall{{ID: call + "-read", Name: "context_fixture_read", Arguments: `{"key":"` + call + `"}`}},
			ProviderState: json.RawMessage(`{"reasoning_content":"` + strings.Repeat("frozen-provider-state-", 400) + `"}`),
		},
	}, nil
}

type contextBrowserToolHost struct {
	authorizations atomic.Int32
	invocations    atomic.Int32
}

func contextBrowserToolDefinition() sdk.ConversationToolDefinition {
	return sdk.ConversationToolDefinition{
		Key: "context_fixture_read", Version: "1", Description: "Read one immutable fixture record.", ActionKey: "fixture.context.read", Effect: "read", Idempotency: "natural",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		TimeoutMillis: 5000, MaxOutputBytes: 32768,
	}
}

func (h *contextBrowserToolHost) ConversationTools(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationToolDefinition, error) {
	return []sdk.ConversationToolDefinition{contextBrowserToolDefinition(), sdk.ConversationToolResultReadDefinition()}, nil
}

func (h *contextBrowserToolHost) AuthorizeConversationTool(_ context.Context, request sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	if request.Call.ID != "" {
		h.authorizations.Add(1)
	}
	return sdk.ConversationToolAuthorization{Granted: true, Revision: "context-policy-v1"}, nil
}

func (h *contextBrowserToolHost) InvokeConversationTool(_ context.Context, request sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if request.Call.Name != "context_fixture_read" {
		return sdk.ConversationToolResult{}, errors.New("unexpected fixture tool")
	}
	h.invocations.Add(1)
	return sdk.ConversationToolResult{Status: "completed", ResourceID: request.Call.ID, Content: json.RawMessage(`{"value":"` + strings.Repeat("immutable-result-", 75) + `"}`)}, nil
}

func (*contextBrowserToolHost) ReconcileConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	return sdk.ConversationToolResult{}, errors.New("read reconciliation is invalid")
}

func TestLongContextManagementBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_E06_BROWSER") != "1" || os.Getenv("AGENT_NODE_BINARY") == "" || os.Getenv("AGENT_UI_TEST_OUTPUT") == "" {
		t.Skip("opt-in E06 long-context browser acceptance")
	}
	const initial, changed = "Context-Browser-Initial!22", "Context-Browser-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "context-browser-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "context-browser-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	revokedOld := &atomic.Bool{}
	sources := []*contextBrowserSource{
		{definition: sdk.ConversationContextSourceDefinition{Key: "project.rules", Kind: sdk.ConversationContextKindProjectInstructions, Scope: sdk.ConversationContextScopeWorkspace, Refresh: sdk.ConversationContextRefreshRun, Trust: sdk.ConversationContextTrustInstruction, Order: 0, MaxBytes: 2048, StablePrefix: true}, revokedOld: revokedOld},
		{definition: sdk.ConversationContextSourceDefinition{Key: "business.current", Kind: sdk.ConversationContextKindBusinessRecord, Scope: sdk.ConversationContextScopeConversation, Refresh: sdk.ConversationContextRefreshStep, Trust: sdk.ConversationContextTrustData, Order: 10, MaxBytes: 2048}, revokedOld: revokedOld},
		{definition: sdk.ConversationContextSourceDefinition{Key: "file.current", Kind: sdk.ConversationContextKindFileReference, Scope: sdk.ConversationContextScopeConversation, Refresh: sdk.ConversationContextRefreshRun, Trust: sdk.ConversationContextTrustReference, Order: 20, MaxBytes: 1024}, revokedOld: revokedOld},
	}
	registered := make([]sdk.ConversationContextSource, len(sources))
	for index := range sources {
		registered[index] = sources[index]
	}
	model := &contextBrowserModel{}
	tools := &contextBrowserToolHost{}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "context-browser.db"), RuntimeID: "context-browser-runtime", WorkspaceID: "context-browser-workspace", ApplicationKey: "context-browser-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{ToolHost: tools, ContextSources: registered, ContextBytes: 20 * 1024, MaxInputBytes: 4 * 1024, SummaryBytes: 1024, Poll: 5 * time.Millisecond}},
	}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	files := os.DirFS(filepath.Join(project, "frontend/dist"))
	seed, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "context-fixture", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: seed, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "context-fixture", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__acceptance/revoke-old-context" && r.Method == http.MethodPost {
			revokedOld.Store(true)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ui.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/context-management.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+os.Getenv("AGENT_UI_TEST_OUTPUT"), "AGENT_UI_PASSWORD="+changed)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("long-context browser acceptance", err)
	}
	if !model.compactionSeen.Load() || !model.historyMasked.Load() || tools.invocations.Load() != 2 || tools.authorizations.Load() < 4 {
		t.Fatalf("compaction=%v masked=%v invocations=%d authorizations=%d", model.compactionSeen.Load(), model.historyMasked.Load(), tools.invocations.Load(), tools.authorizations.Load())
	}
	for _, source := range sources {
		if source.reads.Load() < 2 || source.authorizes.Load() < source.reads.Load() {
			t.Fatalf("source %s reads=%d authorizations=%d", source.definition.Key, source.reads.Load(), source.authorizes.Load())
		}
	}
	raw, _ := json.MarshalIndent(map[string]any{
		"complete": true, "compiled_frontend": true, "real_identity_http": true, "real_sqlite": true,
		"interval_compaction_observed": model.compactionSeen.Load(), "revoked_history_masked": model.historyMasked.Load(),
		"tool_invocations": tools.invocations.Load(), "tool_authorization_checks": tools.authorizations.Load(),
	}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), "host-context-management.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
