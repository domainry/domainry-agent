package producttest

import (
	"context"
	"encoding/json"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/definition"
	module "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-agent/testsupport/databasetest"
	"github.com/domainry/domainry-agent/webhost"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	toolmodule "github.com/domainry/domainry-tools/module"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

// VerifyRecordProduct exercises real Identity, browser routes, the streaming
// model adapter, selected tools, all three databases, source receipts and restart.
func VerifyRecordProduct(t *testing.T, newProduct func() (webhost.Product, error), kind, tool string, initial knowledgemodule.RecordWrite) {
	databasetest.ForEach(t, func(t *testing.T, database databasetest.Options) {
		verifyRecordProduct(t, newProduct, kind, tool, initial, database)
	})
}
func verifyRecordProduct(t *testing.T, newProduct func() (webhost.Product, error), kind, tool string, initial knowledgemodule.RecordWrite, database databasetest.Options) {
	t.Helper()
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Product-Initial-Test-Password!2")
	t.Setenv("AUTH_JWT_SECRET", "product-test-signing-key-32-bytes-long")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "product-test-data-key-32-bytes-long")
	t.Setenv("APP_ENV", "development")
	var modelCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role, Content string
				CallID        string `json:"tool_call_id"`
			}
			Tools []struct{ Function struct{ Name string } }
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid model request")
			w.WriteHeader(500)
			return
		}
		selected := false
		for _, d := range input.Tools {
			selected = selected || d.Function.Name == tool
			if d.Function.Name == "time_now" {
				t.Error("unselected tool reached model")
			}
			if strings.HasPrefix(tool, "pm_") && strings.HasPrefix(d.Function.Name, "work_") || strings.HasPrefix(tool, "work_") && strings.HasPrefix(d.Function.Name, "pm_") {
				t.Error("product tools leaked across profiles")
			}
		}
		if !selected {
			t.Error("product tool missing")
		}
		if len(input.Messages) == 0 || !strings.Contains(input.Messages[0].Content, "Skill ") {
			t.Error("Skill instructions missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		modelCalls.Add(1)
		if input.Messages[len(input.Messages)-1].Role != "tool" {
			args := map[string]any{"expected_revision": 0, "title": "Agent saved record", "status": initial.Status, "data": initial.Data}
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "product-write", "type": "function", "function": map[string]any{"name": tool, "arguments": string(raw)}}}}, "tool_calls")
		} else if input.Messages[len(input.Messages)-1].CallID == "product-write" {
			var receipt struct {
				Status  string
				Content struct{ ID string }
			}
			if json.Unmarshal([]byte(input.Messages[len(input.Messages)-1].Content), &receipt) != nil || receipt.Status != "completed" || receipt.Content.ID == "" {
				t.Error("source record receipt missing")
			}
			args, _ := json.Marshal(map[string]any{"items": []any{map[string]string{"title": "跟进已保存的业务记录", "description": "来源记录：" + receipt.Content.ID, "timezone": "Asia/Shanghai"}}})
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "product-todo", "type": "function", "function": map[string]any{"name": "todo_create", "arguments": string(args)}}}}, "tool_calls")
		} else {
			if !strings.Contains(input.Messages[len(input.Messages)-1].Content, `"status":"completed"`) {
				t.Error("completed business receipt missing")
			}
			write(map[string]any{"content": "已保存业务记录。"}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	options := webhost.ProductOptions{Host: webhost.Options{DatabasePath: database.Path, DatabaseDriver: database.Driver, DatabaseDSN: database.DSN, DatabaseSchema: database.Schema, StoragePath: database.StoragePath, RuntimeID: "product-runtime", WorkspaceID: "product-workspace", Agent: module.Options{ConversationURL: upstream.URL, ConversationModel: "test-product-model", ConversationOptions: module.ConversationOptions{Poll: 5 * time.Millisecond}}}, Origin: "http://127.0.0.1:8181", Files: fstest.MapFS{"index.html": {Data: []byte("product")}}}
	p, err := newProduct()
	if err != nil {
		t.Fatal(err)
	}
	if !p.CalendarTools || !p.MailTools || !p.WebTools || !p.CalendarWriteTools || !p.MailWriteTools || !p.ReportTools {
		t.Fatal("product did not select the configured account tool families")
	}
	available := append(sdk.PersonalConversationTools(), sdk.ArtifactConversationTools()...)
	available = append(available, sdk.KnowledgeConversationTools()...)
	available = append(available, sdk.AttachmentConversationTools()...)
	available = append(available, sdk.KnowledgeLibraryCatalogTool(), sdk.KnowledgeExtractionTool())
	businessDefinitions := append(sdk.BusinessConversationTools(), sdk.BusinessRelationConversationTools()...)
	businessDefinitions = append(businessDefinitions, sdk.BusinessActionConversationTools()...)
	businessDefinitions = append(businessDefinitions, sdk.BusinessWorkflowConversationTools()...)
	available = append(available, businessDefinitions...)
	available = append(available, p.Tools...)
	accountDefinitions := append(toolmodule.CalendarDefinitions(), toolmodule.MailDefinitions()...)
	accountDefinitions = append(accountDefinitions, toolmodule.WebDefinitions()...)
	accountDefinitions = append(accountDefinitions, toolmodule.CalendarWriteDefinitions()...)
	accountDefinitions = append(accountDefinitions, toolmodule.MailWriteDefinitions()...)
	available = append(available, accountDefinitions...)
	available = append(available, toolmodule.ReportDefinitions()...)
	keys := []string{}
	for _, d := range available {
		keys = append(keys, d.Key)
	}
	if _, err := definition.CompileProfile(p.Agent, p.Skills, keys); err != nil {
		t.Fatal("default product profile", err)
	}
	for _, d := range append(accountDefinitions, businessDefinitions...) {
		selected := false
		for _, key := range p.Agent.Tools {
			selected = selected || key == d.Key
		}
		if !selected {
			t.Fatalf("default product omitted configured tool %s", d.Key)
		}
	}
	open := func() *webhost.ProductHost {
		filtered := []string{}
		for _, key := range p.Agent.Tools {
			if key != "time_now" {
				filtered = append(filtered, key)
			}
		}
		p.Agent.Tools = filtered
		// This scenario deliberately removes time_now. Also deselect Skills
		// requiring it, keeping production profile validation strict.
		skillKeys := []string{}
		for _, key := range p.Agent.SkillKeys {
			requiresTime := false
			for _, skill := range p.Skills {
				if skill.Key == key {
					for _, tool := range skill.AllowedTools {
						requiresTime = requiresTime || tool == "time_now"
					}
				}
			}
			if !requiresTime {
				skillKeys = append(skillKeys, key)
			}
		}
		p.Agent.SkillKeys = skillKeys
		options.Host.ApplicationKey = p.Key
		h, e := webhost.OpenProduct(t.Context(), p, options)
		if e != nil {
			t.Fatal(e)
		}
		return h
	}
	h := open()
	defer func() { h.Close(context.Background()) }()
	verifyMetadataNaturalKey(t, h)
	cookies := map[string]*http.Cookie{}
	scope := ""
	call := func(method, path string, value any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if value != nil {
			raw, _ = json.Marshal(value)
		}
		r := httptest.NewRequest(method, options.Origin+path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", options.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Scope", scope)
		r.Header.Set("Idempotency-Key", fmt.Sprintf("test-%d", time.Now().UnixNano()))
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.Handler.ServeHTTP(w, r)
		for _, c := range w.Result().Cookies() {
			if c.MaxAge < 0 {
				delete(cookies, c.Name)
			} else {
				cookies[c.Name] = c
			}
		}
		if w.Code != want {
			t.Fatalf("%s %s status %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	session := func() {
		var state struct{ Scope string }
		if err := json.Unmarshal(call("GET", "/app/session", nil, 200).Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		scope = state.Scope
	}
	login := func(password string) {
		call("POST", "/auth/login", map[string]string{"login": "admin@example.com", "password": password}, 200)
		session()
		prefix := strings.ReplaceAll(options.Host.ApplicationKey, "-", "_")
		if cookies[prefix+"_access"] == nil || cookies[prefix+"_refresh"] == nil {
			t.Fatal("product session cookies are not isolated")
		}
	}
	call("GET", "/app/product/records/"+kind, nil, 401)
	login("Product-Initial-Test-Password!2")
	call("GET", "/app/product/config", nil, 403)
	call("POST", "/auth/password/change", map[string]string{"current_password": "Product-Initial-Test-Password!2", "new_password": "Product-Changed-Test-Password!3"}, 200)
	session()
	call("POST", "/app/product/setup", map[string]any{}, 200)
	call("POST", "/auth/refresh", map[string]any{}, 200)
	session()
	path := "/app/product/records/" + kind
	var record knowledgemodule.Record
	first := call("POST", path, initial, 200)
	json.Unmarshal(first.Body.Bytes(), &record)
	var replay knowledgemodule.Record
	json.Unmarshal(call("POST", path, initial, 200).Body.Bytes(), &replay)
	if record.ID != replay.ID || replay.Revision != 1 {
		t.Fatal("duplicate save")
	}
	edited := initial
	edited.ID = record.ID
	edited.ClientID = "edit"
	edited.ExpectedRevision = 1
	edited.Title = "Browser revised record"
	call("POST", path, edited, 200)
	edited.ClientID = "stale"
	call("POST", path, edited, 409)
	call("GET", path+"/"+record.ID+"?revision=1", nil, 200)
	wrong := scope
	scope = "wrong-user"
	call("GET", path, nil, 409)
	scope = wrong
	var conversation sdk.Conversation
	json.Unmarshal(call("POST", "/agent/conversations", map[string]any{"client_id": "product-conversation", "title": "Product flow"}, 200).Body.Bytes(), &conversation)
	var run sdk.ConversationRun
	base := "/agent/conversations/" + conversation.ID
	json.Unmarshal(call("POST", base+"/messages", map[string]any{"client_message_id": "product-message", "message": "创建一条业务记录，并为它创建一项跟进待办", "write_scope": map[string]bool{"personal_todos": true}}, 202).Body.Bytes(), &run)
	deadline := time.Now().Add(12 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		json.Unmarshal(call("GET", base+"/runs/"+run.ID, nil, 200).Body.Bytes(), &run)
	}
	if run.Status != "completed" || run.AccessError != "" || modelCalls.Load() < 3 {
		t.Fatalf("model/product integration failed: %#v calls=%d", run, modelCalls.Load())
	}
	var page knowledgemodule.RecordPage
	json.Unmarshal(call("GET", path, nil, 200).Body.Bytes(), &page)
	if len(page.Items) != 2 {
		t.Fatalf("tool result did not persist: %+v", page)
	}
	var todos sdk.ConversationTodoPage
	json.Unmarshal(call("GET", "/agent/todos", nil, 200).Body.Bytes(), &todos)
	if len(todos.Items) != 1 || todos.Items[0].SourceConversationID != conversation.ID || !strings.Contains(todos.Items[0].Description, "rec_") {
		t.Fatalf("Agent-to-Todo integration missing: %+v", todos)
	}
	call("POST", "/agent/todos", map[string]any{"client_id": "office-todo", "items": []any{map[string]string{"title": "跟进业务记录", "timezone": "Asia/Shanghai"}}}, 200)
	call("GET", "/agent/knowledge-libraries", nil, 200)
	h.Close(context.Background())
	h = open()
	cookies = map[string]*http.Cookie{}
	scope = ""
	login("Product-Changed-Test-Password!3")
	call("GET", path+"/"+record.ID, nil, 200)
	call("GET", "/agent/todos", nil, 200)
	call("POST", "/auth/logout", map[string]any{}, 204)
	call("GET", path, nil, 401)
}
