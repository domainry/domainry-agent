package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	webread "github.com/domainry/domainry-connector-sdk/web"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

const webFixtureBody = "WEB-BODY：发布说明标注 2026-09-10，支持导出 Markdown。未声明其他格式。"
const webFixtureURL = "https://example.com/releases"
const webFixtureQuery = "最新导出功能发布说明"

type webProductFixture struct {
	*accountFixture
	mode                    atomic.Int32 // 0 normal, 1 empty search, 2 empty page, 3 oversized page, 4 failure
	vendorCalls, modelCalls atomic.Int32
}

func newWebProductFixture(t *testing.T) *webProductFixture {
	t.Helper()
	f := &webProductFixture{accountFixture: newAccountFixture(t)}
	f.close()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.vendorCalls.Add(1)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer private-web-service-token" {
			t.Error("web service credential/method boundary")
			http.Error(w, "denied", 403)
			return
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid web request")
			w.WriteHeader(400)
			return
		}
		if f.mode.Load() == 4 {
			http.Error(w, "private-upstream-failure", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tool/web_search":
			if in["objective"] != webFixtureQuery {
				t.Errorf("changed query %v", in["objective"])
			}
			items := []any{}
			if f.mode.Load() != 1 {
				items = append(items, map[string]any{"url": webFixtureURL, "title": "功能发布说明", "excerpts": []string{"2026-09-10 发布：支持 Markdown 导出。"}})
			}
			json.NewEncoder(w).Encode(map[string]any{"search_id": "web-fixture-search", "results": items})
		case "/tool/web_fetch_jina":
			if in["url"] != webFixtureURL {
				t.Error("changed fetch URL")
			}
			body := webFixtureBody
			if f.mode.Load() == 2 {
				body = ""
			}
			if f.mode.Load() == 3 {
				body = strings.Repeat(webFixtureBody, 200)
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"url": webFixtureURL, "title": "功能发布说明", "content": body, "warning": "远端页面完整性未确认"}})
		default:
			t.Error("unexpected web service path")
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(vendor.Close)
	f.providerTransport = accountProviderHTTPTransport{vendor, "proxy.example.com", "/tool/"}
	model := httptest.NewServer(http.HandlerFunc(f.model))
	t.Cleanup(model.Close)
	f.options.WebTools = true
	f.options.WebConnectionKey = "public-web"
	f.options.Agent = agentmodule.Options{ConversationURL: model.URL, ConversationModel: "web-protocol-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 12, MaxToolCalls: 12}}
	f.open()
	return f
}

func (f *webProductFixture) connect() integration.ConnectionAccount {
	f.t.Helper()
	m := f.options.Integration.(integration.ManagementBinding).Management()
	secretKey := f.options.WebConnectionKey + "-token"
	if _, err := m.UpsertSecret(f.t.Context(), f.options.WorkspaceID, secretKey, "service-administrator", integration.SecretInput{Kind: "bearer_token", Value: "private-web-service-token"}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := m.UpsertConnection(f.t.Context(), f.options.WorkspaceID, f.options.WebConnectionKey, "service-administrator", integration.ConnectionInput{ConnectorKey: "web", ProviderKey: "llm_proxy", Status: "active", Name: "公开网页服务", Config: map[string]any{"base_url": "https://proxy.example.com", "allowed_source_hosts": []string{"example.com"}}, SecretRefs: map[string]string{"api_token": "secret:" + secretKey}}); err != nil {
		f.t.Fatal(err)
	}
	a, err := f.options.Integration.(integration.ConnectionAccountAdministrationBinding).ConnectionAccountAdministration().RegisterConnectionAccount(f.t.Context(), f.options.WorkspaceID, f.options.WebConnectionKey, "service-administrator", integration.ConnectionAccountRegistration{Scope: integration.ConnectionAccountScopeWorkspace})
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

func (f *webProductFixture) grantReader(admin *browser, user string) {
	f.t.Helper()
	mutateTestRolePermissions(f.t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		scopes := map[string]identity.DataScope{integration.ActionIntegrationConnectionAccountsList: identity.DataScopeAll, integration.ActionIntegrationConnectionAccountsRead: identity.DataScopeAll}
		for _, d := range append(toolmodule.WebDefinitions(), sdk.ArtifactConversationTools()...) {
			scopes[d.ActionKey] = identity.DataScopeOwner
		}
		for _, r := range tools.ToolSettingsRoutes() {
			scopes[r.Action.Permission.Key] = identity.DataScopeOwner
		}
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if _, ok := scopes[p.PermissionKey]; !ok {
				out = append(out, p)
			}
		}
		for key, scope := range scopes {
			out = append(out, identity.ProjectRolePermission{PermissionKey: key, DataScope: scope})
		}
		return out
	}, user)
}

// The model protocol fixture chooses each step from real tool feedback. It does
// not evaluate a real model's reasoning, freshness judgment or prompt resistance.
func (f *webProductFixture) model(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tools    []struct{ Function struct{ Name string } }
		Messages []struct {
			Role, Content string
			CallID        string `json:"tool_call_id"`
		}
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
		w.WriteHeader(400)
		return
	}
	f.modelCalls.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(delta any, finish string) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
		w.(http.Flusher).Flush()
	}
	answer := func(s string) { write(map[string]any{"content": s}, "stop") }
	defer fmt.Fprint(w, "data: [DONE]\n\n")
	available := map[string]bool{}
	for _, v := range in.Tools {
		available[v.Function.Name] = true
	}
	call := func(id, key string, args any) {
		if !available[key] {
			answer("当前没有可用的网页服务或读取权限。")
			return
		}
		write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": key, "arguments": accountJSON(args)}}}}, "tool_calls")
	}
	data := map[string]json.RawMessage{}
	times := map[string]string{}
	for _, m := range in.Messages {
		if m.Role != "tool" {
			continue
		}
		var result sdk.ConversationToolResult
		if json.Unmarshal([]byte(m.Content), &result) != nil || result.Status != "completed" {
			answer("网页读取失败，未形成资料结论或保存报告。")
			return
		}
		if strings.HasPrefix(m.CallID, "web-") {
			var env struct {
				Data   json.RawMessage
				ReadAt string `json:"read_at"`
			}
			json.Unmarshal(result.Content, &env)
			data[m.CallID] = env.Data
			times[m.CallID] = env.ReadAt
		} else {
			data[m.CallID] = result.Content
		}
	}
	last := in.Messages[len(in.Messages)-1]
	if last.Role == "user" {
		call("web-search", "web_search", map[string]any{"query": webFixtureQuery, "limit": 2})
		return
	}
	var search webread.SearchResult
	json.Unmarshal(data["web-search"], &search)
	var page webread.Page
	json.Unmarshal(data["web-fetch"], &page)
	var artifact sdk.ConversationArtifactVersion
	for _, key := range []string{"report-create", "report-read", "report-edit"} {
		if raw, ok := data[key]; ok {
			json.Unmarshal(raw, &artifact)
		}
	}
	switch last.CallID {
	case "web-search":
		if len(search.Items) == 0 {
			answer("没有返回匹配来源，无法确认最新功能。")
			return
		}
		call("web-fetch", "web_fetch", map[string]any{"url": search.Items[0].URL, "max_content_bytes": 1024})
	case "web-fetch":
		if page.Content == "" {
			answer("未取得可读正文，无法据此判断网页为空或确认最新功能。未保存报告。")
			return
		}
		if page.Truncated {
			answer("网页正文已裁剪，仅取得部分内容；无法形成完整报告，未保存。")
			return
		}
		text := fmt.Sprintf("# 网页资料报告\n\n摘录：%s\n\n查询：%s\n来源：%s\n请求 URL：%s\n读取时间：%s\n页面完整性：%s\n提示：%s\n\n源文标注发布日期 2026-09-10；读取时间并非发布时间。排名搜索不穷尽来源，其他功能未证实。\n", page.Content, webFixtureQuery, page.URL, page.RequestedURL, times["web-fetch"], page.SourceCompleteness, strings.Join(page.Warnings, "；"))
		call("report-create", "artifact_create", map[string]any{"title": "网页资料报告", "content": sdk.ConversationArtifactContent{Kind: "markdown", Markdown: text}})
	case "report-create":
		call("report-read", "artifact_read", map[string]any{"id": artifact.Artifact.ID, "version": artifact.Artifact.Version})
	case "report-read":
		call("report-edit", "artifact_edit", map[string]any{"id": artifact.Artifact.ID, "expected_version": artifact.Artifact.Version, "patch": map[string]any{"text": []any{map[string]string{"find": "其他功能未证实。", "replace": "其他功能未证实；使用前再次核对发布页。"}}}})
	case "report-edit":
		call("report-export", "artifact_export", map[string]any{"id": artifact.Artifact.ID, "version": artifact.Artifact.Version, "format": "markdown"})
	case "report-export":
		var receipt struct {
			Export sdk.ConversationArtifactExport
		}
		json.Unmarshal(data["report-export"], &receipt)
		if receipt.Export.ID == "" || artifact.Artifact.ID == "" {
			answer("报告回执缺失。")
			return
		}
		answer(fmt.Sprintf("资料摘要：%s\n\n来源：%s；读取时间：%s。页面完整性未知，排名结果不代表全部来源。\n\n已保存网页资料报告，版本 %d，并导出 Markdown。报告 ID：%s。", page.Content, page.URL, times["web-fetch"], artifact.Artifact.Version, artifact.Artifact.ID))
	default:
		answer("网页处理未完成。")
	}
}

func webRun(t *testing.T, b *browser, message string, write bool) (string, sdk.ConversationRun, string) {
	t.Helper()
	id := fmt.Sprintf("web-%d", time.Now().UnixNano())
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: id}), 200))
	input := sdk.ConversationSend{ClientMessageID: id, Message: message}
	if write {
		input.WriteScope = &sdk.ConversationWriteScope{PersonalArtifacts: true}
	}
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(input), 202))
	deadline := time.Now().Add(2 * time.Minute)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		run = accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "completed" {
		t.Fatalf("web run %s error=%s steps=%+v", run.Status, run.ErrorCode, run.Steps)
	}
	messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200))
	for _, m := range messages.Items {
		if m.Role == "assistant" {
			return c.ID, run, m.Content
		}
	}
	t.Fatal("web answer missing")
	return "", run, ""
}
