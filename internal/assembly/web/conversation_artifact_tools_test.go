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

const artifactReportFixture = "# 周报\n\n## 第一节：本周进展\n完成需求访谈，整理项目事项。\n\n## 第二节：费用核对\n待核对。\n\n## 第三节：下周计划\n提交修订稿。\n"

// The protocol fixture uses only the actual model input and returned tool IDs.
// Identity, persistence, source checks, HTTP streaming and UI remain real.
func artifactModelFixture(t *testing.T) *httptest.Server {
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
			t.Error("invalid artifact model request")
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
		intent := ""
		for _, message := range payload.Messages {
			if message.Role == "user" {
				intent = message.Content
			}
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role != "tool" {
			if strings.Contains(intent, "第二节") || strings.Contains(intent, "下载") {
				tool("artifact_list", "artifact-find", map[string]any{"query": "验收周报", "limit": 20})
			} else {
				tool("artifact_create", "artifact-create", map[string]any{"title": "验收周报", "content": agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: artifactReportFixture}})
			}
		} else {
			var envelope agentsdk.ConversationToolResult
			if err := json.Unmarshal([]byte(last.Content), &envelope); err != nil || envelope.Status != "completed" {
				t.Errorf("artifact tool did not complete: %s", last.Content)
				write(map[string]any{"content": "操作未完成，请核对执行记录。"}, "stop")
			} else {
				switch last.ToolCallID {
				case "artifact-find":
					var page agentsdk.ConversationArtifactPage
					if err := json.Unmarshal(envelope.Content, &page); err != nil || len(page.Items) == 0 {
						t.Error("artifact lookup empty", err)
						write(map[string]any{"content": "未找到周报。"}, "stop")
						break
					}
					item := page.Items[0]
					if strings.Contains(intent, "下载") {
						tool("artifact_export", "artifact-export", map[string]any{"id": item.ID, "version": 1, "format": "markdown"})
					} else {
						tool("artifact_read", "artifact-read", map[string]any{"id": item.ID, "version": item.Version})
					}
				case "artifact-read":
					var value agentsdk.ConversationArtifactReadResult
					if err := json.Unmarshal(envelope.Content, &value); err != nil || !strings.Contains(value.Markdown, "待核对。") {
						t.Error("original report section missing", err)
						write(map[string]any{"content": "请核对原始版本。"}, "stop")
						break
					}
					tool("artifact_edit", "artifact-edit", map[string]any{"id": value.Artifact.ID, "expected_version": value.Artifact.Version, "patch": map[string]any{"text": []any{map[string]string{"find": "待核对。", "replace": "已核对各部门费用，统计口径保持一致。"}}}})
				default:
					write(map[string]any{"content": "周报已保存，可从工具执行记录查看对应版本，也可在“我的成果”中修改和下载。"}, "")
					write(map[string]any{}, "stop")
				}
			}
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestArtifactToolsThroughIdentityHTTP(t *testing.T) {
	const initial, changed = "Initial-Artifact-Tool-Test!2", "Changed-Artifact-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "artifact-tool-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "artifact-tool-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := artifactModelFixture(t)
	defer model.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "artifact-tools.db"), RuntimeID: "artifact-tools-runtime", WorkspaceID: "artifact-tools-workspace", ApplicationKey: "artifact-tools-app", Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "artifact-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "artifact-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grant := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix+"artifact_") {
					out = append(out, p)
				}
			}
			for _, tool := range agentsdk.ArtifactConversationTools() {
				if tool.Key != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	grant("")
	var conversation agentsdk.Conversation
	if err = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"artifact-http"}`, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	base := "/agent/conversations/" + conversation.ID
	wait := func(id, status string) agentsdk.ConversationRun {
		t.Helper()
		var run agentsdk.ConversationRun
		// Race instrumentation includes Identity and transitive source reads on
		// every public snapshot. Keep the functional deadline independent of a
		// normal-build latency expectation and avoid competing with the worker.
		deadline := time.Now().Add(60 * time.Second)
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
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("run did not reach %s: status=%s error=%s steps=%d", status, run.Status, run.ErrorCode, len(run.Steps))
		return run
	}
	send := func(key, message string, scoped bool) agentsdk.ConversationRun {
		input := agentsdk.ConversationSend{ClientMessageID: key, Message: message}
		if scoped {
			input.WriteScope = &agentsdk.ConversationWriteScope{PersonalArtifacts: true}
		}
		raw, _ := json.Marshal(input)
		var run agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	created := send("create", "生成一份周报", true)
	created = wait(created.ID, "completed")
	var page agentsdk.ConversationArtifactPage
	if err = json.Unmarshal(b.call("GET", "/agent/artifacts", "", 200).Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatal("artifact create missing", err)
	}
	if page.Items[0].SourceConversationID != conversation.ID || page.Items[0].SourceRunID != created.ID {
		t.Fatalf("artifact source relation missing: %+v", page.Items[0])
	}
	path := "/agent/artifacts/" + page.Items[0].ID
	edit := send("edit", "把刚才周报的第二节改为已核对", false)
	edit = wait(edit.ID, "waiting_confirmation")
	if edit.Interaction == nil || edit.Interaction.Tool != "artifact_edit" || !strings.Contains(edit.Interaction.Arguments, "待核对。") {
		t.Fatal("exact edit confirmation missing")
	}
	// Restart the actual host with the pending confirmation intact.
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	restored := wait(edit.ID, "waiting_confirmation")
	if restored.Interaction.ID != edit.Interaction.ID || restored.Interaction.Arguments != edit.Interaction.Arguments {
		t.Fatal("restart changed the confirmation")
	}
	response, _ := json.Marshal(agentsdk.ConversationInteractionResponse{InteractionID: edit.Interaction.ID, ClientID: "approve-edit", ExpectedRevision: edit.Interaction.Revision, Decision: "approve"})
	b.call("POST", base+"/runs/"+edit.ID+"/respond", string(response), 200)
	wait(edit.ID, "completed")
	b.call("POST", base+"/runs/"+edit.ID+"/respond", string(response), 200)
	var original, latest agentsdk.ConversationArtifactVersion
	_ = json.Unmarshal(b.call("GET", path+"?version=1", "", 200).Body.Bytes(), &original)
	_ = json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &latest)
	if original.Content.Markdown != artifactReportFixture || latest.Artifact.Version != 2 || !strings.Contains(latest.Content.Markdown, "已核对各部门费用") {
		t.Fatal("old version or edited section changed")
	}
	exported := send("export", "下载周报的第一版", true)
	exported = wait(exported.ID, "completed")
	var file agentsdk.ConversationArtifactExport
	for _, step := range exported.Steps {
		for _, call := range step.Calls {
			if call.Name == "artifact_export" {
				var result struct {
					Export agentsdk.ConversationArtifactExport `json:"export"`
				}
				if err := json.Unmarshal([]byte(call.ResultPreview), &result); err != nil {
					t.Fatal(err)
				}
				file = result.Export
			}
		}
	}
	if file.ID == "" || file.Version != 1 {
		t.Fatal("export did not select the first version")
	}
	downloaded := b.call("GET", "/agent/artifact-exports/"+file.ID+"/download", "", 200)
	if downloaded.Body.String() != artifactReportFixture {
		t.Fatal("tool export downloaded different bytes")
	}
	grant("artifact_read")
	b.call("GET", path, "", 403)
	var restricted agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("GET", base+"/runs/"+created.ID, "", 200).Body.Bytes(), &restricted)
	if restricted.AccessError == "" || len(restricted.Steps) != 0 {
		t.Fatal("revoked artifact still exposed in execution history")
	}
	grant("")
	// Seed additional content types for the optional real browser acceptance.
	b.call("POST", "/agent/artifacts", `{"client_id":"table-fixture","title":"费用精度表","content":{"kind":"table","table":{"columns":[{"key":"name","label":"项目","type":"text"},{"key":"amount","label":"金额","type":"number"}],"rows":[["=SUM(A1:A2)","9007199254740993.01"],["访谈费用","12.30"]]}}}`, 200)
	b.call("POST", "/agent/artifacts", `{"client_id":"chart-fixture","title":"项目趋势图","content":{"kind":"chart","table":{"columns":[{"key":"week","label":"周次","type":"text"},{"key":"count","label":"完成事项","type":"number"}],"rows":[["第一周","3"],["第二周","7"],["第三周","5"]]},"chart":{"type":"bar","x_column":"week","y_columns":["count"]}}}`, 200)
	b.call("POST", "/agent/artifacts", `{"client_id":"safe-render-fixture","title":"安全渲染验证","content":{"kind":"markdown","markdown":"# 安全渲染\\n\\n<script>window.__artifactExecuted=true</script>\\n\\n![远程图片](https://example.invalid/private.png)\\n\\n[危险链接](javascript:window.__artifactExecuted=true)\\n\\n[公开链接](https://example.com/)"}}`, 200)
	servePersonalToolAcceptance(t, host, options, map[string]func(){"revoke-artifact-read": func() { grant("artifact_read") }, "restore-artifact-read": func() { grant("") }})
}
