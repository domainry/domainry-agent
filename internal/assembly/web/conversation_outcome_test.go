package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

// Only the model is synthetic. Confirmation, Identity, effects, recovery,
// failure receipts and history reads use the product's actual services.
type outcomeWebModel struct {
	mu       sync.Mutex
	failures map[string]bool
}

func (*outcomeWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{Content: "结果验证", Model: "outcome-fixture"}, nil
}
func (*outcomeWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "outcome-fixture", Fingerprint: "outcome-fixture-v1"}
}
func (m *outcomeWebModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	user := ""
	for _, message := range in.Messages {
		if message.Role == "user" {
			user = message.Content
		}
	}
	calls := func(values ...agentsdk.ConversationToolCall) (agentsdk.ConversationStepResult, error) {
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "outcome-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: values}}, nil
	}
	todo := func(id, title, zone string) agentsdk.ConversationToolCall {
		raw, _ := json.Marshal(map[string]any{"items": []any{map[string]string{"title": title, "timezone": zone}}})
		return agentsdk.ConversationToolCall{ID: id, Name: "todo_create", Arguments: string(raw)}
	}
	if last.Role != "tool" {
		if strings.Contains(user, "请修复这次处理") {
			match := regexp.MustCompile(`来源会话：([^；]+)；来源处理记录：([^；]+)；`).FindStringSubmatch(user)
			if len(match) != 3 {
				return agentsdk.ConversationStepResult{}, errors.New("repair reference missing")
			}
			raw, _ := json.Marshal(agentsdk.ConversationExecutionRead{ConversationID: match[1], RunID: match[2], Limit: 5})
			return calls(agentsdk.ConversationToolCall{ID: "inspect-failure", Name: "execution_read", Arguments: string(raw)})
		}
		if strings.Contains(user, "回复中断") {
			return calls(todo("before-model-failure", "回复前已保存的待办", "Asia/Shanghai"))
		}
		return calls(todo("outcome-good", "核对合同付款条件", "Asia/Shanghai"), todo("outcome-bad", "整理本周项目进度", "Invalid/Fixture"))
	}
	if last.ToolCallID == "inspect-failure" {
		var result agentsdk.ConversationToolResult
		var evidence agentsdk.ConversationExecutionReadResult
		if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || json.Unmarshal(result.Content, &evidence) != nil || !evidence.Complete || evidence.Omitted {
			return agentsdk.ConversationStepResult{}, errors.New("repair evidence unavailable")
		}
		found := false
		for _, entry := range evidence.Items {
			found = found || entry.CallID == "outcome-bad" && entry.Status == "failed" && entry.ErrorCode == "todo_timezone_invalid"
		}
		if !found {
			return agentsdk.ConversationStepResult{}, errors.New("definite failure absent")
		}
		return calls(todo("repair-bad", "整理本周项目进度", "Asia/Shanghai"))
	}
	if last.ToolCallID == "before-model-failure" {
		m.mu.Lock()
		failed := m.failures[in.IdempotencyKey]
		m.failures[in.IdempotencyKey] = true
		m.mu.Unlock()
		if !failed {
			return agentsdk.ConversationStepResult{}, errors.New("fixture model unavailable after actual write")
		}
	}
	content := "已读取保存结果，完成本次处理。"
	if last.ToolCallID == "outcome-bad" {
		content = "第一项待办已保存；第二项时区无效，创建失败。"
	}
	return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "outcome-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: content}}, nil
}

func outcomeWait(t *testing.T, b *browser, path, status string) agentsdk.ConversationRun {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		var value agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if value.Status == status {
			return value
		}
		if value.Terminal() {
			t.Fatalf("expected %s got %s/%s", status, value.Status, value.ErrorCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("outcome wait timed out")
	return agentsdk.ConversationRun{}
}
func outcomeApprove(t *testing.T, b *browser, path string, run agentsdk.ConversationRun, scope string) {
	t.Helper()
	if run.Interaction == nil {
		t.Fatal("missing confirmation")
	}
	raw, _ := json.Marshal(agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ExpectedRevision: run.Interaction.Revision, ClientID: "approve-" + run.Interaction.ID, Decision: "approve", Scope: scope})
	b.call("POST", path+"/respond", string(raw), 200)
}

func TestExecutionOutcomeThroughIdentityHTTPAndBrowser(t *testing.T) {
	const initial, changed = "Initial-Outcome-Test!2", "Changed-Outcome-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "outcome-test-signing-secret-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "outcome-test-data-secret-32bytes-long")
	t.Setenv("APP_ENV", "development")
	model := &outcomeWebModel{failures: map[string]bool{}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "outcomes.db"), RuntimeID: "outcome-runtime", WorkspaceID: "outcome-workspace", ApplicationKey: "outcome-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
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
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		open()
		b.handler = handler
		b.login("admin@example.com", changed)
	}
	start := func(id, message string) (agentsdk.Conversation, string) {
		var c agentsdk.Conversation
		_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"`+id+`"}`, 200).Body.Bytes(), &c)
		var run agentsdk.ConversationRun
		raw, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: id, Message: message})
		_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", string(raw), 202).Body.Bytes(), &run)
		return c, "/agent/conversations/" + c.ID + "/runs/" + run.ID
	}
	todos := func(c agentsdk.Conversation) []agentsdk.ConversationTodo {
		var page agentsdk.ConversationTodoPage
		_ = json.Unmarshal(b.call("GET", "/agent/todos?source_conversation_id="+c.ID, "", 200).Body.Bytes(), &page)
		return page.Items
	}
	c, path := start("outcome-http", "创建两项待办")
	waiting := outcomeWait(t, b, path, "waiting_confirmation")
	outcomeApprove(t, b, path, waiting, "listed_operations")
	done := outcomeWait(t, b, path, "completed")
	if len(todos(c)) != 1 || done.Steps[0].Calls[0].Status != "completed" || done.Steps[0].Calls[1].Status != "failed" || done.Steps[0].Calls[1].ErrorCode != "todo_timezone_invalid" {
		t.Fatal("partial actual outcome missing", done.Steps)
	}
	reopen()
	stored := outcomeWait(t, b, path, "completed")
	if stored.Steps[0].Calls[1].Status != "failed" {
		t.Fatal("failed receipt lost")
	}
	b.call("POST", path+"/resume", "", 409)
	var repair agentsdk.ConversationRun
	raw, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: "repair", Message: "请修复这次处理第 1 步。来源会话：" + c.ID + "；来源处理记录：" + done.ID + "；步骤：0；调用：outcome-bad。"})
	_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", string(raw), 202).Body.Bytes(), &repair)
	repairPath := "/agent/conversations/" + c.ID + "/runs/" + repair.ID
	outcomeApprove(t, b, repairPath, outcomeWait(t, b, repairPath, "waiting_confirmation"), "")
	if fixed := outcomeWait(t, b, repairPath, "completed"); len(todos(c)) != 2 || fixed.ID == done.ID {
		t.Fatal("repair did not create only missing item")
	}
	if previous := outcomeWait(t, b, path, "completed"); previous.Steps[0].Calls[1].Status != "failed" {
		t.Fatal("repair overwrote original receipt")
	}
	c, path = start("outcome-model-http", "创建待办后回复中断")
	outcomeApprove(t, b, path, outcomeWait(t, b, path, "waiting_confirmation"), "")
	failed := outcomeWait(t, b, path, "failed")
	if len(todos(c)) != 1 || len(failed.Steps) != 2 || failed.Steps[1].Status != "interrupted" {
		t.Fatal("model failure hid saved effect", failed.Steps)
	}
	reopen()
	b.call("POST", path+"/resume", "", 200)
	recovered := outcomeWait(t, b, path, "completed")
	if len(todos(c)) != 1 || recovered.Attempt != failed.Attempt+1 {
		t.Fatal("model recovery repeated write")
	}
	t.Logf("actual Identity/SQLite: definite partial failure repaired in new run %s; model failure resumes same run %s after full host restart without repeating effects", repair.ID, recovered.ID)
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){"restart_outcome_host": reopen, "revoke_outcome_tools": func() { grantPersonalTools(t, host, b, false) }, "restore_outcome_tools": func() { grantPersonalTools(t, host, b, true) }})
}
