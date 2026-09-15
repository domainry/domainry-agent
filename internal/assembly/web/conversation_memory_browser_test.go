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
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type memoryBrowserModel struct {
	recalled    atomic.Bool
	crossScoped atomic.Bool
}

func (*memoryBrowserModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "memory-browser", Fingerprint: "memory-browser-v1"}
}

func (*memoryBrowserModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{}, errors.New("memory browser fixture requires the step protocol")
}

func (m *memoryBrowserModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	text := ""
	for _, message := range in.Messages {
		text += "\n" + message.Content
	}
	reply := "已记录当前消息。"
	switch {
	case strings.Contains(text, "继续 Alpha 存储架构"):
		for _, expected := range []string{"项目 Alpha 生产环境使用 PostgreSQL", `"kind":"project_fact"`, `"kind":"conversation"`, `"message_id":`, `"correction":`, "旧内容只覆盖开发环境"} {
			if !strings.Contains(text, expected) {
				return sdk.ConversationStepResult{}, errors.New("relevant scoped memory lost " + expected)
			}
		}
		m.recalled.Store(true)
		reply = "已使用当前会话的 Alpha 项目事实。"
	case strings.Contains(text, "完全无关的 Beta 事项"):
		if strings.Contains(text, "项目 Alpha 生产环境使用 PostgreSQL") || strings.Contains(text, "旧内容只覆盖开发环境") {
			return sdk.ConversationStepResult{}, errors.New("conversation-scoped memory leaked into another conversation")
		}
		m.crossScoped.Store(true)
		reply = "Beta 会话没有收到 Alpha 的私有任务资料。"
	}
	return sdk.ConversationStepResult{FinishReason: "stop", Model: "memory-browser", Message: sdk.ConversationStepMessage{Role: "assistant", Content: reply}}, nil
}

func TestScopedMemoryBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_K02_BROWSER") != "1" || os.Getenv("AGENT_NODE_BINARY") == "" || os.Getenv("AGENT_UI_TEST_OUTPUT") == "" {
		t.Skip("opt-in K02 scoped-memory browser acceptance")
	}
	const initial, changed = "Memory-Browser-Initial!22", "Memory-Browser-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "memory-browser-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "memory-browser-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &memoryBrowserModel{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "memory-browser.db"), RuntimeID: "memory-browser-runtime", WorkspaceID: "memory-browser-workspace", ApplicationKey: "memory-browser-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
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
	seed, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "memory-browser", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: seed, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "memory-browser", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()
	runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(runCtx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/scoped-memory.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+os.Getenv("AGENT_UI_TEST_OUTPUT"), "AGENT_UI_PASSWORD="+changed)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("scoped memory browser acceptance", err)
	}
	if !model.recalled.Load() || !model.crossScoped.Load() {
		t.Fatalf("memory model observations recalled=%v cross_scoped=%v", model.recalled.Load(), model.crossScoped.Load())
	}
	service := host.Agent.(sdk.ConversationBinding).Conversations()
	authority := sdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	memories, err := service.Memories(t.Context(), authority)
	if err != nil || len(memories) != 0 {
		t.Fatalf("browser deletion did not forget current memory: %+v %v", memories, err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "real_sqlite": true, "relevant_recall": model.recalled.Load(), "cross_conversation_excluded": model.crossScoped.Load()}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), "host-scoped-memory.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
