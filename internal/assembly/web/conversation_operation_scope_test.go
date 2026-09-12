package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type operationScopeWebModel struct{}

func (operationScopeWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{Content: "范围授权验证", Model: "scope-fixture"}, nil
}
func (operationScopeWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "scope-fixture", Fingerprint: "scope-fixture-v1"}
}
func (operationScopeWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	call := func(id, title string) agentsdk.ConversationToolCall {
		raw, _ := json.Marshal(map[string]any{"items": []any{map[string]string{"title": title, "timezone": "Asia/Shanghai"}}})
		return agentsdk.ConversationToolCall{ID: id, Name: "todo_create", Arguments: string(raw)}
	}
	if last.Role != "tool" {
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "scope-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call("scope-first", "核对合同付款条件"), call("scope-second", "整理本周项目进度")}}}, nil
	}
	if last.ToolCallID == "scope-second" {
		for _, m := range in.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "追加") {
				return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "scope-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call("scope-extra", "追加：发送项目汇总前复核")}}}, nil
			}
		}
	}
	return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "scope-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已按你确认的具体范围完成待办创建。"}}, nil
}

func TestListedOperationScopeThroughIdentityHTTPAndBrowser(t *testing.T) {
	const initial, changed = "Initial-Scope-Test!2", "Changed-Scope-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "operation-scope-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "operation-scope-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	gate := &executionAdmissionGate{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "scopes.db"), RuntimeID: "scope-runtime", WorkspaceID: "scope-workspace", ApplicationKey: "scope-app", Agent: agentmodule.Options{ConversationProvider: operationScopeWebModel{}, ConversationOptions: agentmodule.ConversationOptions{ExecutionAuthorizer: gate, Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}}}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		gate.host.Store(host)
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "scope-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
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
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		gate.mu.Lock()
		gate.entered, gate.release = nil, nil
		gate.mu.Unlock()
		open()
		b.handler = handler
		b.login("admin@example.com", changed)
	}
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"scope-http"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"scope-send","message":"创建两项并追加一项"}`, 202).Body.Bytes(), &run)
	path := base + "/runs/" + run.ID
	wait := func(status string) agentsdk.ConversationRun {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			var value agentsdk.ConversationRun
			_ = json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &value)
			if value.Status == status {
				return value
			}
			if value.Terminal() {
				t.Fatalf("scope HTTP expected %s got %s/%s", status, value.Status, value.ErrorCode)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("scope HTTP timeout")
		return agentsdk.ConversationRun{}
	}
	first := wait("waiting_confirmation")
	if first.Interaction == nil || len(first.Interaction.Operations) != 2 {
		t.Fatal("scope preview absent")
	}
	reopen()
	stored := wait("waiting_confirmation")
	if stored.Interaction.ID != first.Interaction.ID || stored.Interaction.Operations[1].Arguments != first.Interaction.Operations[1].Arguments {
		t.Fatal("scope changed on restart")
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: stored.Interaction.ID, ClientID: "approve-scope", ExpectedRevision: stored.Interaction.Revision, Decision: "approve", Scope: "listed_operations"}
	raw, _ := json.Marshal(response)
	b.call("POST", path+"/respond", string(raw), 200)
	b.call("POST", path+"/respond", string(raw), 200)
	next := wait("waiting_confirmation")
	if next.Interaction.CallID != "scope-extra" || len(next.Interaction.Operations) != 0 {
		t.Fatal("scope authorized a later model call")
	}
	var page agentsdk.ConversationTodoPage
	_ = json.Unmarshal(b.call("GET", "/agent/todos?source_conversation_id="+c.ID, "", 200).Body.Bytes(), &page)
	if len(page.Items) != 2 {
		t.Fatalf("grouped effects=%d", len(page.Items))
	}
	raw, _ = json.Marshal(agentsdk.ConversationInteractionResponse{InteractionID: next.Interaction.ID, ClientID: "reject-extra", ExpectedRevision: next.Interaction.Revision, Decision: "reject"})
	b.call("POST", path+"/respond", string(raw), 200)
	t.Log("Identity HTTP: exact two-item scope persisted across host restart; duplicate response has two effects; later call requires separate confirmation and rejection has no effect")
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){"restart_scope_host": reopen, "arm_scope_execution": gate.arm, "revoke_scope_tools": func() { grantPersonalTools(t, host, b, false) }, "restore_scope_tools": func() { grantPersonalTools(t, host, b, true) }})
}
