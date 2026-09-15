package web

import (
	"context"
	"encoding/json"
	"errors"
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

type trajectoryBrowserModel struct {
	calls       atomic.Int32
	forkContext atomic.Bool
}

func (*trajectoryBrowserModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: provider.ConversationProtocolChat, Model: "trajectory-fixture", Fingerprint: "trajectory-fixture-v1"}
}

func (*trajectoryBrowserModel) ConversationStepInputBytes(in sdk.ConversationStepRequest) (int, error) {
	return provider.ConversationStepInputBytes(provider.ConversationModelConfig{Provider: "fixture", Protocol: provider.ConversationProtocolChat, Model: "trajectory-fixture", MaxOutputTokens: 1024}, in)
}

func (*trajectoryBrowserModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{}, errors.New("trajectory browser fixture requires the step protocol")
}

func (m *trajectoryBrowserModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	call := m.calls.Add(1)
	var joined strings.Builder
	for _, message := range in.Messages {
		joined.WriteString("\n")
		joined.WriteString(message.Role)
		joined.WriteString(":")
		joined.WriteString(message.Content)
	}
	text := joined.String()
	content := "原方案已完成并保存。"
	switch call {
	case 1:
		if !strings.Contains(text, "记录原方案并形成稳定边界") {
			return sdk.ConversationStepResult{}, errors.New("source request missing from initial model input")
		}
	case 2:
		if !strings.Contains(text, "Forked completed-run context follows") || !strings.Contains(text, "记录原方案并形成稳定边界") || !strings.Contains(text, "原方案已完成并保存") || !strings.Contains(text, "沿另一条路线继续") {
			return sdk.ConversationStepResult{}, errors.New("fork did not receive the authorized completed-run snapshot and current input")
		}
		m.forkContext.Store(true)
		content = "分叉已基于受权历史上下文采用另一条路线。"
	default:
		return sdk.ConversationStepResult{}, errors.New("display, export, comparison or fixture replay invoked the model")
	}
	return sdk.ConversationStepResult{FinishReason: "stop", Model: "trajectory-fixture", Usage: map[string]any{"input_tokens": 40, "output_tokens": 12}, Message: sdk.ConversationStepMessage{Role: "assistant", Content: content}}, nil
}

func TestConversationTrajectoryForkReplayBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_E09_BROWSER") != "1" || os.Getenv("AGENT_NODE_BINARY") == "" || os.Getenv("AGENT_UI_TEST_OUTPUT") == "" {
		t.Skip("opt-in E09 conversation trajectory browser acceptance")
	}
	const initial, changed = "Trajectory-Browser-Initial!22", "Trajectory-Browser-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "trajectory-browser-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "trajectory-browser-data-key-long-enough")
	t.Setenv("APP_ENV", "development")

	model := &trajectoryBrowserModel{}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "trajectory-browser.db"), RuntimeID: "trajectory-browser-runtime", WorkspaceID: "trajectory-browser-workspace", ApplicationKey: "trajectory-browser-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}},
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
	seed, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "trajectory-fixture", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: seed, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "trajectory-fixture", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__acceptance/trajectory-state" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"model_calls": model.calls.Load(), "fork_context_seen": model.forkContext.Load()})
			return
		}
		ui.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/conversation-trajectory.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+os.Getenv("AGENT_UI_TEST_OUTPUT"), "AGENT_UI_PASSWORD="+changed)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("conversation trajectory browser acceptance", err)
	}
	if model.calls.Load() != 2 || !model.forkContext.Load() {
		t.Fatalf("model calls=%d fork context=%v", model.calls.Load(), model.forkContext.Load())
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "real_sqlite": true, "model_calls": model.calls.Load(), "fork_context_seen": model.forkContext.Load(), "historical_effects_executed_by_replay": false}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), "host-conversation-trajectory.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
