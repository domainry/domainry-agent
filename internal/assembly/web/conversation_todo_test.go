package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// Only model decisions are deterministic fixtures. The fixture consumes actual
// tool results: it never accesses the database or invents resource identifiers.
// This verifies orchestration, not an external model's language understanding.
func personalTodoModelFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Messages) == 0 {
			t.Error("invalid todo model request")
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		tool := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		last := payload.Messages[len(payload.Messages)-1]
		intent, answer := "", ""
		var page agentsdk.ConversationTodoPage
		for _, message := range payload.Messages {
			if message.Role == "user" {
				if strings.Contains(message.Content, "整理成待办") || strings.Contains(message.Content, "第二项") {
					intent = message.Content
				}
				if message.Content == "较早那批" || message.Content == "最新那批" {
					answer = message.Content
				}
			}
			if message.Role == "tool" && message.ToolCallID == "todo-find" {
				var result agentsdk.ConversationToolResult
				if err := json.Unmarshal([]byte(message.Content), &result); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal(result.Content, &page); err != nil {
					t.Error(err)
				}
			}
		}
		switch {
		case last.Role == "user" && len(page.Items) > 0 && answer != "":
			tool("time_now", "todo-time", map[string]any{"timezone": "Asia/Shanghai"})
		case last.Role != "tool":
			if strings.Contains(intent, "整理成待办") {
				prefix := ""
				if strings.Contains(intent, "另一批") {
					prefix = "另一批："
				}
				tool("todo_create", "todo-create", map[string]any{"items": []agentsdk.ConversationTodoInput{
					{Title: prefix + "整理访谈记录", Description: "保留客户问题和来源", Timezone: "Asia/Shanghai"},
					{Title: prefix + "核对费用", Description: "核对各部门费用并注明统计口径", Timezone: "Asia/Shanghai"},
					{Title: prefix + "提交周报", Timezone: "Asia/Shanghai"},
				}})
			} else {
				tool("todo_list", "todo-find", map[string]any{"scope": "current_conversation", "status": "open", "limit": 20})
			}
		case last.ToolCallID == "todo-find":
			batches := map[string]bool{}
			for _, item := range page.Items {
				batches[item.BatchID] = true
			}
			if !page.Complete || len(batches) == 0 {
				t.Error("fixture requires a complete nonempty todo list")
				write(map[string]any{"content": "请先核对事项范围。"}, "stop")
			} else if len(batches) > 1 {
				tool("ask_user", "todo-which-batch", map[string]any{"question": "你说的第二项属于哪批待办？", "choices": []string{"最新那批", "较早那批"}})
			} else {
				tool("time_now", "todo-time", map[string]any{"timezone": "Asia/Shanghai"})
			}
		case last.ToolCallID == "todo-time":
			var envelope struct {
				Content struct {
					Date string `json:"date"`
				} `json:"content"`
			}
			_ = json.Unmarshal([]byte(last.Content), &envelope)
			date, err := time.Parse("2006-01-02", envelope.Content.Date)
			if err != nil || len(page.Items) == 0 {
				t.Error("time/list result missing", err)
				return
			}
			batch := page.Items[0].BatchID
			if answer == "较早那批" {
				batch = page.Items[len(page.Items)-1].BatchID
			}
			var target agentsdk.ConversationTodo
			for _, item := range page.Items {
				if item.BatchID == batch && item.Position == 2 {
					target = item
				}
			}
			if target.ID == "" {
				t.Error("original second item missing")
				return
			}
			friday := date.AddDate(0, 0, (int(time.Friday)-int(date.Weekday())+7)%7).Format("2006-01-02")
			tool("todo_update", "todo-change", map[string]any{"id": target.ID, "expected_revision": target.Revision, "patch": map[string]string{"due_date": friday}})
		default:
			var result agentsdk.ConversationToolResult
			if err := json.Unmarshal([]byte(last.Content), &result); err != nil || result.Status != "completed" {
				t.Errorf("todo tool failed: %s", last.Content)
			}
			message := "事项已更新，保留原来的批次与序号。"
			if last.ToolCallID == "todo-create" {
				message = "已创建：1. 整理访谈记录；2. 核对费用；3. 提交周报。"
			}
			write(map[string]any{"content": message}, "")
			write(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestPersonalTodosThroughIdentityHTTP(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	model := personalTodoModelFixture(t)
	defer model.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "todos.db"), RuntimeID: "todo-runtime", WorkspaceID: "todo-workspace", ApplicationKey: "todo-app", Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "todo-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "todo-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"todo-http"}`, 200).Body.Bytes(), &conversation)
	base := "/agent/conversations/" + conversation.ID
	send := func(key, text string, scope *agentsdk.ConversationWriteScope) agentsdk.ConversationRun {
		raw, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: key, Message: text, WriteScope: scope})
		var run agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	wait := func(id, status string) agentsdk.ConversationRun {
		t.Helper()
		var run agentsdk.ConversationRun
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if err := json.Unmarshal(b.call("GET", base+"/runs/"+id, "", 200).Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Status == status {
				return run
			}
			if run.Terminal() {
				t.Fatalf("expected %s, got %s: %s", status, run.Status, run.ErrorCode)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("never reached %s: %+v", status, run)
		return run
	}
	list := func() []agentsdk.ConversationTodo {
		var page agentsdk.ConversationTodoPage
		if err := json.Unmarshal(b.call("GET", "/agent/todos?source_conversation_id="+conversation.ID, "", 200).Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if !page.Complete {
			t.Fatal("unexpected incomplete test list")
		}
		return page.Items
	}
	get := func(id string) agentsdk.ConversationTodo {
		var item agentsdk.ConversationTodo
		if err := json.Unmarshal(b.call("GET", "/agent/todos/"+id, "", 200).Body.Bytes(), &item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	respond := func(run agentsdk.ConversationRun, decision, answer string, status int) string {
		raw, _ := json.Marshal(agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ExpectedRevision: run.Interaction.Revision, ClientID: "response-" + run.ID, Decision: decision, Answer: answer})
		b.call("POST", base+"/runs/"+run.ID+"/respond", string(raw), status)
		return string(raw)
	}

	created := send("create", "将访谈整理、费用核对、提交周报整理成待办", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	created = wait(created.ID, "completed")
	items := list()
	if len(items) != 3 || created.Interaction != nil {
		t.Fatal("batch creation failed or repeated confirmation")
	}
	for i, item := range items {
		if item.Position != i+1 || item.SourceConversationID != conversation.ID || item.SourceRunID != created.ID || item.Revision != 1 || item.Status != "open" || item.BatchID != items[0].BatchID {
			t.Fatalf("bad source/order: %+v", item)
		}
	}
	first, second, third := items[0], items[1], items[2]
	// The direct UI and Agent operate on the same records and revision rules.
	patch := `{"client_id":"complete-first","expected_revision":1,"patch":{"status":"completed"}}`
	b.call("PATCH", "/agent/todos/"+first.ID, patch, 200)
	b.call("PATCH", "/agent/todos/"+first.ID, patch, 200)
	if got := get(first.ID); got.Revision != 2 || got.CompletedAt == nil {
		t.Fatal("completion receipt was not idempotent")
	}

	// A different resource's write scope neither grants todo writes nor inherits
	// the previous request's todo grant. The first open item is original item 2.
	update := send("update", "把第二项改到周五", &agentsdk.ConversationWriteScope{PersonalMemory: true})
	update = wait(update.ID, "waiting_confirmation")
	var change struct {
		ID    string `json:"id"`
		Patch struct {
			DueDate string `json:"due_date"`
		} `json:"patch"`
	}
	if err := json.Unmarshal([]byte(update.Interaction.Arguments), &change); err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", change.Patch.DueDate)
	if err != nil || date.Weekday() != time.Friday || change.ID != second.ID || get(second.ID).Revision != 1 || get(third.ID).Revision != 1 {
		t.Fatal("ordinal/scope/date resolution failed")
	}
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	restored := wait(update.ID, "waiting_confirmation")
	if restored.Interaction.ID != update.Interaction.ID || restored.Interaction.Arguments != update.Interaction.Arguments {
		t.Fatal("confirmation changed on host restart")
	}
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if p.PermissionKey != agentsdk.ConversationToolActionPrefix+"todo_update" {
				out = append(out, p)
			}
		}
		return out
	})
	respond(restored, "approve", "", 403)
	b.call("PATCH", "/agent/todos/"+second.ID, `{"client_id":"denied-ui","expected_revision":1,"patch":{"status":"completed"}}`, 403)
	if get(second.ID).Revision != 1 {
		t.Fatal("revoked permission mutated todo")
	}
	grantPersonalTools(t, host, b, true)
	raw := respond(restored, "approve", "", 200)
	wait(update.ID, "completed")
	b.call("POST", base+"/runs/"+update.ID+"/respond", raw, 200)
	if got := get(second.ID); got.Revision != 2 || got.DueDate != change.Patch.DueDate || got.Position != 2 {
		t.Fatal("approved update was lost or repeated")
	}
	b.call("PATCH", "/agent/todos/"+second.ID, `{"client_id":"stale-ui","expected_revision":1,"patch":{"title":"stale replacement"}}`, 409)

	other := send("create-other", "另一批工作也整理成待办", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	wait(other.ID, "completed")
	items = list()
	if len(items) != 6 || items[0].BatchID == first.BatchID {
		t.Fatal("new batch not distinguishable")
	}
	newSecond := items[1]
	ambiguous := send("ambiguous", "把第二项改到周五", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	ambiguous = wait(ambiguous.ID, "waiting_user")
	if ambiguous.Interaction.Tool != "ask_user" || len(ambiguous.Interaction.Choices) != 2 || get(newSecond.ID).Revision != 1 || get(second.ID).Revision != 2 {
		t.Fatal("ambiguous target was guessed")
	}
	answer := respond(ambiguous, "answer", "较早那批", 200)
	wait(ambiguous.ID, "completed")
	b.call("POST", base+"/runs/"+ambiguous.ID+"/respond", answer, 200)
	if get(second.ID).Revision != 3 || get(newSecond.ID).Revision != 1 {
		t.Fatal("answer did not select original second item exactly once")
	}
	var messages agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &messages)
	answers := 0
	for _, message := range messages.Items {
		if message.Role == "user" && message.Content == "较早那批" {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("persisted user answers = %d", answers)
	}
	sse := b.call("GET", base+"/runs/"+ambiguous.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	for _, event := range []string{"run.waiting_user", "interaction.responded", "tool.completed", "run.completed"} {
		if !strings.Contains(sse, event) {
			t.Errorf("SSE missing %s", event)
		}
	}
	deleteBody := `{"client_id":"delete-first","expected_revision":2}`
	b.call("DELETE", "/agent/todos/"+first.ID, deleteBody, 200)
	b.call("DELETE", "/agent/todos/"+first.ID, deleteBody, 200)
	if len(list()) != 5 || get(second.ID).Position != 2 {
		t.Fatal("deletion renumbered remaining todos")
	}
	servePersonalToolAcceptance(t, host, options)
}

func TestPersonalTodosWithoutModelAndAcrossUsers(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "todos-ui.db"), RuntimeID: "todo-ui-runtime", WorkspaceID: "todo-ui-workspace", ApplicationKey: "todo-ui-app"}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	a := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	a.call("GET", "/agent/todos", "", 401)
	a.login("admin@example.com", initial)
	a.changePassword(initial, changed)
	a.call("GET", "/agent/todos", "", 403)
	grantPersonalTools(t, host, a, true)
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(a.call("POST", "/agent/conversations", `{"client_id":"todo-ui-source"}`, 200).Body.Bytes(), &conversation)
	raw, _ := json.Marshal(agentsdk.ConversationTodoCreate{ClientID: "create-ui", SourceConversationID: conversation.ID, Items: []agentsdk.ConversationTodoInput{{Title: "A 的私有工作", Description: "只能由 A 读取", Timezone: "Asia/Shanghai", DueAt: "2026-09-11T09:00:00+08:00"}}})
	created := a.call("POST", "/agent/todos", string(raw), 200).Body.String()
	if replay := a.call("POST", "/agent/todos", string(raw), 200).Body.String(); replay != created {
		t.Fatal("UI create receipt changed")
	}
	var batch agentsdk.ConversationTodoBatch
	if err := json.Unmarshal([]byte(created), &batch); err != nil || len(batch.Items) != 1 {
		t.Fatal("bad batch", err)
	}
	item := batch.Items[0]
	path := "/agent/todos/" + item.ID
	if item.SourceRunID != "" || item.SourceConversationID != conversation.ID {
		t.Fatal("UI create has incorrect provenance")
	}

	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("system_administrator@example.com", initial)
	b.changePassword(initial, changed)
	secondUserID, ok := b.readSession()["user_id"].(string)
	if !ok || secondUserID == "" {
		t.Fatal("second authenticated subject missing")
	}
	mutateTestRolePermissions(t, host, a, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix) {
				out = append(out, p)
			}
		}
		for _, tool := range agentsdk.PersonalConversationTools() {
			out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
		}
		return out
	}, secondUserID)
	b.call("GET", path, "", 404)
	b.call("PATCH", path, `{"client_id":"cross-user-update","expected_revision":1,"patch":{"title":"overwrite"}}`, 404)
	// Delete is a compare-and-swap: no owned matching revision is a conflict,
	// irrespective of whether another user's record exists under that ID.
	b.call("DELETE", path, `{"client_id":"cross-user-delete","expected_revision":1}`, 409)
	b.call("POST", "/agent/todos", string(raw), 404) // A's source is not B's source.
	if body := b.call("GET", "/agent/todos", "", 200).Body.String(); strings.Contains(body, item.Title) || strings.Contains(body, item.ID) {
		t.Fatal("cross-user list leak")
	}
	if body := a.call("GET", path, "", 200).Body.String(); !strings.Contains(body, item.Title) {
		t.Fatal("cross-user write changed owner record")
	}

	// Editing the title from the webpage preserves a precise deadline. Reusing
	// the same mutation ID with a different patch is a conflict, not another edit.
	patch := `{"client_id":"edit-ui","expected_revision":1,"patch":{"title":"已核对的私有工作","timezone":"Asia/Shanghai"}}`
	var updated agentsdk.ConversationTodo
	_ = json.Unmarshal(a.call("PATCH", path, patch, 200).Body.Bytes(), &updated)
	if updated.Revision != 2 || updated.DueAt != item.DueAt || updated.DueDate != "" {
		t.Fatal("unrelated UI edit damaged deadline")
	}
	a.call("PATCH", path, patch, 200)
	a.call("PATCH", path, strings.Replace(patch, "已核对的私有工作", "different", 1), 409)
	a.call("DELETE", fmt.Sprintf("/agent/conversations/%s?expected_revision=%d", conversation.ID, conversation.Revision), "", 200)
	a.call("GET", path, "", 200)
	if replay := a.call("POST", "/agent/todos", string(raw), 200).Body.String(); replay != created {
		t.Fatal("conversation deletion lost UI mutation receipt")
	}
	a.call("DELETE", path, `{"client_id":"delete-ui","expected_revision":2}`, 200)
	a.call("DELETE", path, `{"client_id":"delete-ui","expected_revision":2}`, 200)
	a.call("GET", path, "", 404)
}
