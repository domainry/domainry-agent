package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type catalogConnectionFixture struct {
	disconnected, calculationDisabled, failed atomic.Bool
}

func (s *catalogConnectionFixture) ConversationToolAvailable(_ context.Context, a agentsdk.ConversationAuthority, key string) (bool, error) {
	if a.RuntimeID != "catalog-runtime" || a.WorkspaceID != "catalog-workspace" || a.UserID != "admin" || !a.Known {
		return false, nil
	}
	if s.failed.Load() {
		return false, errors.New("connector lookup failed: secret-catalog-fixture-credential")
	}
	return !(strings.HasPrefix(key, "knowledge_") && s.disconnected.Load()) && !(key == "calculate" && s.calculationDisabled.Load()), nil
}

func TestConversationCatalogIdentityHTTPConnectionsAndBrowser(t *testing.T) {
	const initial, changed = "Initial-Catalog-Test!2", "Changed-Catalog-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "catalog-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "catalog-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	state := &catalogConnectionFixture{}
	readStarted, readStopped := make(chan struct{}), make(chan struct{})
	var startRead, stopRead sync.Once
	var interruptedReads atomic.Int32
	var modelCalls, knowledgeCalls atomic.Int32
	knowledge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		knowledgeCalls.Add(1)
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid", 400)
			return
		}
		if body.Query == "B06-wait-cancel" {
			startRead.Do(func() { close(readStarted) })
			<-r.Context().Done()
			interruptedReads.Add(1)
			stopRead.Do(func() { close(readStopped) })
			return
		}
		fmt.Fprint(w, `{"hits":[{"doc_id":"catalog-policy","title":"工具目录验收资料","body":"CATALOG-KNOWLEDGE-EVIDENCE"}]}`)
	}))
	defer knowledge.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role, Content string
				CallID        string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
			t.Error("invalid catalog model request")
			http.Error(w, "invalid", 400)
			return
		}
		modelCalls.Add(1)
		keys := []string{}
		for _, tool := range in.Tools {
			keys = append(keys, tool.Function.Name)
			if strings.HasPrefix(tool.Function.Name, "artifact_") || strings.HasPrefix(tool.Function.Name, "business_") {
				t.Error("ungranted/unmounted capability reached model")
			}
		}
		slices.Sort(keys)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		answer := func(body string) { write(map[string]any{"content": body}, ""); write(map[string]any{}, "stop") }
		call := func(key, args string) {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "catalog-call", "type": "function", "function": map[string]any{"name": key, "arguments": args}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		last := in.Messages[len(in.Messages)-1]
		switch {
		case last.Role == "user" && strings.Contains(last.Content, "B06取消"):
			write(map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "b06-first", "type": "function", "function": map[string]any{"name": "calculate", "arguments": `{"operation":"expression","expression":"0.1+0.2","unit":"CNY"}`}},
				map[string]any{"index": 1, "id": "b06-read", "type": "function", "function": map[string]any{"name": "knowledge_search", "arguments": `{"query":"B06-wait-cancel"}`}},
				map[string]any{"index": 2, "id": "b06-third", "type": "function", "function": map[string]any{"name": "calculate", "arguments": `{"operation":"expression","expression":"0.1+0.2","unit":"CNY"}`}},
			}}, "")
			write(map[string]any{}, "tool_calls")
		case last.Role == "tool":
			if strings.Contains(last.Content, "CATALOG-KNOWLEDGE-EVIDENCE") {
				answer("已通过当前连接取得验收资料。")
			} else {
				if !strings.Contains(last.Content, `"value":"0.30"`) {
					t.Error("actual calculation result missing")
				}
				answer("计算已完成：0.30 元。")
			}
		case strings.Contains(last.Content, "执行前停用"):
			if !slices.Contains(keys, "calculate") {
				t.Error("initial enabled calculation missing")
			}
			state.calculationDisabled.Store(true)
			call("calculate", `{"operation":"expression","expression":"0.1+0.2","unit":"CNY"}`)
		case strings.Contains(last.Content, "计算"):
			if slices.Contains(keys, "calculate") {
				call("calculate", `{"operation":"expression","expression":"0.1+0.2","unit":"CNY"}`)
			} else {
				answer("当前工具目录不提供计算。")
			}
		case strings.Contains(last.Content, "检索"):
			if slices.Contains(keys, "knowledge_search") {
				call("knowledge_search", `{"query":"工具目录验收资料"}`)
			} else {
				answer("当前工具目录不提供资料检索。")
			}
		default:
			answer("当前可用工具：" + strings.Join(keys, "、"))
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "catalog.db"), RuntimeID: "catalog-runtime", WorkspaceID: "catalog-workspace", ApplicationKey: "catalog-app", Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "catalog-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, ToolAvailability: state}, Knowledge: agentmodule.KnowledgeConfig{BaseURL: knowledge.URL, APIKey: "isolated-catalog-test-key", TeamID: "team", KBID: "catalog", WorkspaceID: "catalog-workspace"}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	if _, legacy := any(host).(modulehost.ApplicationHost); legacy {
		t.Fatal("web conversations unexpectedly depend on legacy application ports")
	}
	defer func() { _ = host.Close(context.Background()) }()
	b := &browser{t: t, cookies: map[string]*http.Cookie{}}
	bindHTTP := func() {
		var err error
		b.handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "catalog-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("catalog")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	bindHTTP()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grant := func(calculate bool) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix) && p.PermissionKey != agentsdk.ConversationInteractionPermission().Key {
					out = append(out, p)
				}
			}
			for _, d := range append(agentsdk.PersonalConversationTools(), agentsdk.KnowledgeConversationTools()...) {
				if d.Key != "calculate" || calculate {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			out = append(out, identitysdk.ProjectRolePermission{PermissionKey: agentsdk.ConversationInteractionPermission().Key, DataScope: identitysdk.DataScopeOwner})
			return out
		})
	}
	grant(true)
	runToEnd := func(c string, run agentsdk.ConversationRun) agentsdk.ConversationRun {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if err := json.Unmarshal(b.call("GET", "/agent/conversations/"+c+"/runs/"+run.ID, "", 200).Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Terminal() {
				return run
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("catalog run did not complete: %+v", run)
		return run
	}
	round := func(id, message string) (string, agentsdk.ConversationRun, string) {
		started := time.Now()
		raw, _ := json.Marshal(agentsdk.ConversationCreate{ClientID: id, Title: id})
		var c agentsdk.Conversation
		_ = json.Unmarshal(b.call("POST", "/agent/conversations", string(raw), 200).Body.Bytes(), &c)
		raw, _ = json.Marshal(agentsdk.ConversationSend{ClientMessageID: id, Message: message})
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c.ID+"/messages", string(raw), 202).Body.Bytes(), &run)
		run = runToEnd(c.ID, run)
		t.Logf("catalog round=%s status=%s error=%s elapsed=%s model_calls=%d", id, run.Status, run.ErrorCode, time.Since(started), modelCalls.Load())
		var page agentsdk.ConversationMessagePage
		_ = json.Unmarshal(b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200).Body.Bytes(), &page)
		return c.ID, run, page.Items[len(page.Items)-1].Content
	}
	_, run, answer := round("catalog-ready", "目录")
	if run.Status != "completed" || !strings.Contains(answer, "knowledge_search") || !strings.Contains(answer, "calculate") || !strings.Contains(answer, "memory_save") {
		t.Fatalf("ready=%+v %s", run, answer)
	}
	state.disconnected.Store(true)
	_, run, answer = round("catalog-disconnected", "目录")
	if run.Status != "completed" || strings.Contains(answer, "knowledge_") || !strings.Contains(answer, "time_now") {
		t.Fatalf("disconnected=%+v %s", run, answer)
	}
	before := knowledgeCalls.Load()
	_, run, answer = round("catalog-no-search", "检索")
	if run.Status != "completed" || !strings.Contains(answer, "不提供资料检索") || knowledgeCalls.Load() != before {
		t.Fatalf("disconnected check: run=%+v answer=%q knowledge_before=%d knowledge_after=%d", run, answer, before, knowledgeCalls.Load())
	}
	state.disconnected.Store(false)
	grant(false)
	_, run, answer = round("catalog-denied", "目录")
	if run.Status != "completed" || strings.Contains(answer, "calculate") || !strings.Contains(answer, "knowledge_search") {
		t.Fatal("availability overrode Identity denial")
	}
	grant(true)
	state.failed.Store(true)
	before = modelCalls.Load()
	c, run, answer := round("catalog-failed-check", "目录")
	if run.Status != "failed" || run.ErrorCode != "tool_availability_failed" || modelCalls.Load() != before || strings.Contains(answer, "secret-catalog") {
		t.Fatalf("failed check=%+v", run)
	}
	state.failed.Store(false)
	_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c+"/runs/"+run.ID+"/resume", `{}`, 200).Body.Bytes(), &run)
	if run = runToEnd(c, run); run.Status != "completed" {
		t.Fatalf("recovered check=%+v", run)
	}
	searched, run, _ := round("catalog-search-restored", "检索")
	if run.Status != "completed" || knowledgeCalls.Load() == 0 {
		t.Fatal("restored connection did not execute actual Connector")
	}
	state.disconnected.Store(true)
	before = knowledgeCalls.Load()
	var hidden agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", "/agent/conversations/"+searched+"/messages", "", 200).Body.Bytes(), &hidden)
	if len(hidden.Items) != 2 || hidden.Items[1].AccessError == "" || strings.Contains(hidden.Items[1].Content, "取得验收资料") || knowledgeCalls.Load() != before {
		t.Fatal("historical source revalidation bypassed disconnected tool policy")
	}
	state.disconnected.Store(false)
	c, run, _ = round("catalog-execution-switch", "执行前停用计算")
	if run.Status != "failed" || run.ErrorCode != "tool_access_denied" {
		t.Fatalf("disabled after model=%+v", run)
	}
	state.calculationDisabled.Store(false)
	_ = json.Unmarshal(b.call("POST", "/agent/conversations/"+c+"/runs/"+run.ID+"/resume", `{}`, 200).Body.Bytes(), &run)
	if run = runToEnd(c, run); run.Status != "completed" || len(run.Steps[0].Calls) != 1 || run.Steps[0].Calls[0].Status != "completed" {
		t.Fatalf("resumed computation=%+v", run)
	}
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		bindHTTP()
	}
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){
		"disconnect_knowledge": func() { state.disconnected.Store(true) },
		"disable_calculate":    func() { state.calculationDisabled.Store(true) },
		"restore_connections": func() {
			state.disconnected.Store(false)
			state.calculationDisabled.Store(false)
			state.failed.Store(false)
		},
		"availability_error":          func() { state.failed.Store(true) },
		"revoke_calculate":            func() { grant(false) },
		"restore_catalog_permissions": func() { grant(true) },
		"restart_catalog_host":        reopen,
		"wait_cancellation_read": func() {
			select {
			case <-readStarted:
			case <-time.After(5 * time.Second):
				t.Error("cancellation read did not reach HTTP server")
			}
		},
		"assert_cancellation_observed": func() {
			select {
			case <-readStopped:
			case <-time.After(5 * time.Second):
				t.Error("knowledge HTTP request was not cancelled")
			}
		},
	})
	for _, table := range []string{"_agent_task_runs", "_agent_task_definitions", "_agent_interactive_runs"} {
		var count int
		if err := host.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("conversation-only host wrote legacy table %s: count=%d error=%v", table, count, err)
		}
	}
	t.Log("conversation-only web binding: legacy Task, TaskDefinition and Interactive rows=0")
	if interruptedReads.Load() > 0 {
		t.Logf("B06: official knowledge HTTP requests stopped=%d", interruptedReads.Load())
	}
	t.Logf("catalog acceptance completed: model HTTP requests=%d, knowledge HTTP requests=%d", modelCalls.Load(), knowledgeCalls.Load())
}
