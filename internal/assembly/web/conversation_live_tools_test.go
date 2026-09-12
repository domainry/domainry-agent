package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// Opt-in: sends synthetic work to a real configured model. Identity, HTTP,
// SQLite and artifacts are real and isolated from the user's deployment.
// Assertions inspect stored outcomes, not an exact model tool-call sequence.
func TestLiveDailyWorkThroughIdentityHTTP(t *testing.T) {
	if os.Getenv("AGENT_DAILY_WORK_LIVE") != "1" {
		t.Skip("set AGENT_DAILY_WORK_LIVE=1 with the model configuration to run real daily-work acceptance")
	}
	config := provider.ConversationModelConfigFromEnvironment()
	model, err := provider.NewConversationModel(config)
	if err != nil {
		t.Fatal(err)
	}
	const initial, changed = "Initial-Live-Work-Test!2", "Changed-Live-Work-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "live-work-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "live-work-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "live-work.db"), RuntimeID: "live-work-runtime", WorkspaceID: "live-work-workspace", ApplicationKey: "live-work-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationModel: config.Model, ConversationTimezone: "Asia/Shanghai", ConversationOptions: agentmodule.ConversationOptions{Poll: 20 * time.Millisecond}}}
	var host *Host
	var b *browser
	open := func() {
		t.Helper()
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, e := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: config.Model, Files: fstest.MapFS{"index.html": {Data: []byte("live acceptance")}}})
		if e != nil {
			t.Fatal(e)
		}
		b = &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	open()
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		for _, tool := range agentsdk.ArtifactConversationTools() {
			previous = append(previous, identitysdk.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identitysdk.DataScopeOwner})
		}
		return previous
	})
	decode := func(raw []byte, out any) {
		t.Helper()
		if e := json.Unmarshal(raw, out); e != nil {
			t.Fatal(e)
		}
	}
	record := func(phase string, run agentsdk.ConversationRun) {
		t.Helper()
		if directory := os.Getenv("AGENT_LIVE_EVIDENCE_DIR"); directory != "" {
			raw, e := json.MarshalIndent(run, "", "  ")
			if e == nil {
				e = os.WriteFile(filepath.Join(directory, phase+".json"), raw, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	create := func(key, title string) agentsdk.Conversation {
		t.Helper()
		raw, _ := json.Marshal(agentsdk.ConversationCreate{ClientID: key, Title: title, MemoryEnabled: true})
		var conversation agentsdk.Conversation
		decode(b.call("POST", "/agent/conversations", string(raw), 200).Body.Bytes(), &conversation)
		return conversation
	}
	send := func(conversation agentsdk.Conversation, phase, message string, scope *agentsdk.ConversationWriteScope) agentsdk.ConversationRun {
		t.Helper()
		started := time.Now()
		t.Logf("phase=%s started model=%s", phase, config.Model)
		base := "/agent/conversations/" + conversation.ID
		raw, _ := json.Marshal(agentsdk.ConversationSend{ClientMessageID: phase, Message: message, WriteScope: scope})
		var run agentsdk.ConversationRun
		decode(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run)
		deadline := started.Add(6 * time.Minute)
		lastState := ""
		resumes := 0
		for time.Now().Before(deadline) {
			decode(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
			calls := []string{}
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					state := call.Name + ":" + call.Status
					if call.ErrorCode != "" {
						state += "(" + call.ErrorCode + ")"
					}
					calls = append(calls, state)
				}
			}
			state := run.Status + " " + strings.Join(calls, ",")
			if state != lastState {
				t.Logf("phase=%s elapsed=%s state=%s", phase, time.Since(started).Round(time.Second), state)
				lastState = state
			}
			if run.Status == "completed" {
				record(phase, run)
				if len(run.Steps) == 0 || strings.TrimSpace(run.Steps[len(run.Steps)-1].Text) == "" {
					t.Fatalf("phase=%s completed without an answer", phase)
				}
				return run
			}
			if run.Status == "failed" && resumes == 0 && (run.ErrorCode == "provider_timeout" || run.ErrorCode == "provider_unavailable") {
				// Exercise the public same-run recovery path for one transient
				// model failure. Do not resend the user message or retry uncertain
				// writes, permission failures, bad arguments or assertion failures.
				record(phase+"-before-resume", run)
				resumes++
				t.Logf("phase=%s resuming the same run after %s", phase, run.ErrorCode)
				decode(b.call("POST", base+"/runs/"+run.ID+"/resume", "", 200).Body.Bytes(), &run)
				continue
			}
			if run.Terminal() || run.Interaction != nil {
				record(phase, run)
				t.Fatalf("phase=%s stopped status=%s error=%s; explicit input should permit completing this step", phase, run.Status, run.ErrorCode)
			}
			select {
			case <-t.Context().Done():
				t.Fatal("live acceptance cancelled")
			case <-time.After(500 * time.Millisecond):
			}
		}
		record(phase, run)
		t.Fatalf("phase=%s timed out status=%s", phase, run.Status)
		return run
	}
	hasCall := func(run agentsdk.ConversationRun, name string) bool {
		for _, step := range run.Steps {
			for _, call := range step.Calls {
				if call.Name == name && call.Status == "completed" {
					return true
				}
			}
		}
		return false
	}
	history := create("live-history", "青禾发布事项讨论")
	send(history, "discussion", "青禾发布事项：1. 整理访谈记录；2. 核对部门费用；3. 提交发布周报。三个事项目前都没完成，也没有截止日期。这里只讨论，不创建待办、不保存偏好；回复一句收到即可。", nil)
	work := create("live-work", "真实模型日常工作验收")
	created := send(work, "create-todos", "找出我们另一段会话里讨论的“青禾发布事项”，读取原始讨论，把其中三项按原顺序建成一批个人待办，标题保留原文，时区是 Asia/Shanghai。暂不设置截止日期。", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	if !hasCall(created, "history_search") || !hasCall(created, "history_read") || !hasCall(created, "todo_create") {
		t.Fatal("real model did not retrieve original history and create the todos")
	}
	var todos agentsdk.ConversationTodoPage
	decode(b.call("GET", "/agent/todos?source_conversation_id="+work.ID, "", 200).Body.Bytes(), &todos)
	if !todos.Complete || len(todos.Items) != 3 {
		t.Fatal("expected exactly three stored todos")
	}
	sort.Slice(todos.Items, func(i, j int) bool { return todos.Items[i].Position < todos.Items[j].Position })
	for i, title := range []string{"整理访谈记录", "核对部门费用", "提交发布周报"} {
		item := todos.Items[i]
		if item.Title != title || item.Position != i+1 || item.BatchID != todos.Items[0].BatchID || item.Status != "open" || item.DueDate != "" || item.Timezone != "Asia/Shanghai" {
			t.Fatalf("stored todo at position %d does not match the original discussion", i+1)
		}
	}
	zone, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Now().In(zone)
	daysUntilFriday := (int(time.Friday) - int(now.Weekday()) + 7) % 7
	if daysUntilFriday == 0 {
		daysUntilFriday = 7
	}
	friday := now.AddDate(0, 0, daysUntilFriday).Format("2006-01-02")
	updated := send(work, "reschedule-second", "把刚才第二项改到接下来的周五，即上海日期 "+friday+"。仍先调用可信时间核对今天的上海日期，只改这一项的截止日期。", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	if !hasCall(updated, "time_now") || !hasCall(updated, "todo_update") {
		t.Fatal("model did not resolve the date and update the todo")
	}
	var second agentsdk.ConversationTodo
	decode(b.call("GET", "/agent/todos/"+todos.Items[1].ID, "", 200).Body.Bytes(), &second)
	if second.DueDate != friday || second.Revision != todos.Items[1].Revision+1 {
		t.Fatalf("second todo deadline=%s expected=%s revision=%d", second.DueDate, friday, second.Revision)
	}
	saved := send(work, "remember-format", "请记住我的周报格式偏好：固定按“本周进展、风险、下周计划”三节组织，每节使用简短要点。将这条偏好保存为个人记忆。", &agentsdk.ConversationWriteScope{PersonalMemory: true})
	if !hasCall(saved, "memory_save") {
		t.Fatal("model did not save the requested preference")
	}
	var memories []agentsdk.ConversationMemory
	decode(b.call("GET", "/agent/conversations/memories", "", 200).Body.Bytes(), &memories)
	if len(memories) != 1 || !memories[0].Enabled || !strings.Contains(memories[0].Content, "下周计划") {
		t.Fatal("stored weekly report preference missing")
	}
	generated := send(work, "create-report", "先读取这批待办的最新状态，再根据实际状态和我保存的格式偏好生成一份 Markdown 成果，标题为“青禾验收周报”。三节依次使用二级标题“本周进展”“风险”“下周计划”。第一节逐项列出三个原始待办标题及其状态；未完成的明确写“未完成”，不能写成已完成。未知风险写待核对。保存该成果。", &agentsdk.ConversationWriteScope{PersonalArtifacts: true})
	if !hasCall(generated, "todo_list") && !hasCall(generated, "todo_get") {
		t.Fatal("model did not read current todo state before generating the report")
	}
	var artifacts agentsdk.ConversationArtifactPage
	decode(b.call("GET", "/agent/artifacts", "", 200).Body.Bytes(), &artifacts)
	if len(artifacts.Items) != 1 || artifacts.Items[0].Title != "青禾验收周报" {
		t.Fatal("real model did not save exactly one report")
	}
	artifactPath := "/agent/artifacts/" + artifacts.Items[0].ID
	var original agentsdk.ConversationArtifactVersion
	decode(b.call("GET", artifactPath+"?version=1", "", 200).Body.Bytes(), &original)
	for _, heading := range []string{"## 本周进展", "## 风险", "## 下周计划"} {
		if !strings.Contains(original.Content.Markdown, heading) {
			t.Fatal("report did not follow the saved section format")
		}
	}
	progressSection, _, _ := strings.Cut(original.Content.Markdown, "## 风险")
	for _, item := range todos.Items {
		correct := false
		for _, line := range strings.Split(progressSection, "\n") {
			if strings.Contains(line, item.Title) && strings.Contains(line, "未完成") && !strings.Contains(line, "已完成") {
				correct = true
			}
		}
		if !correct {
			t.Fatalf("report did not faithfully state the stored open status for %s", item.Title)
		}
	}
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host = nil
	open()
	b.login("admin@example.com", changed)
	t.Log("host fully closed and reopened with the same SQLite database and Identity")
	var restored agentsdk.ConversationTodoPage
	decode(b.call("GET", "/agent/todos", "", 200).Body.Bytes(), &restored)
	if len(restored.Items) != 3 {
		t.Fatal("todos did not survive host restart")
	}
	continuation := create("live-continuation", "重启后继续工作")
	continued := send(continuation, "continue-work", "继续处理之前的“青禾发布事项”：我刚完成了那批的第二项，请找到原待办并标记完成。回复它的名称、截止日期，以及我保存的周报格式偏好。不要另建待办。", &agentsdk.ConversationWriteScope{PersonalTodos: true})
	continuedText := continued.Steps[len(continued.Steps)-1].Text
	if !hasCall(continued, "todo_update") || !strings.Contains(continuedText, "核对部门费用") || !strings.Contains(continuedText, "下周计划") {
		t.Fatal("model failed to continue the original work and saved preference after restart")
	}
	decode(b.call("GET", "/agent/todos/"+second.ID, "", 200).Body.Bytes(), &second)
	if second.Status != "completed" || second.DueDate != friday {
		t.Fatal("restart continuation changed the wrong todo or lost its deadline")
	}
	send(continuation, "edit-report", "找到之前保存的“青禾验收周报”，把第二节“风险”的正文替换为“费用已核对，暂无新增风险。”，保留该节标题、第一节和第三节的原文。修改同一份成果，不另建文件。", &agentsdk.ConversationWriteScope{PersonalArtifacts: true})
	var latest, reread agentsdk.ConversationArtifactVersion
	decode(b.call("GET", artifactPath, "", 200).Body.Bytes(), &latest)
	decode(b.call("GET", artifactPath+"?version=1", "", 200).Body.Bytes(), &reread)
	beforeRisk, _, _ := strings.Cut(original.Content.Markdown, "## 风险")
	_, afterRisk, _ := strings.Cut(original.Content.Markdown, "## 下周计划")
	if latest.Artifact.Version != 2 || !strings.Contains(latest.Content.Markdown, "费用已核对，暂无新增风险。") || !strings.HasPrefix(latest.Content.Markdown, beforeRisk) || !strings.HasSuffix(latest.Content.Markdown, afterRisk) || reread.Content.Markdown != original.Content.Markdown {
		t.Fatal("report edit changed unrelated sections, duplicated versions or modified the original")
	}
	exported := send(continuation, "export-original", "将“青禾验收周报”的第一版导出为 Markdown 供我下载。务必导出版本 1，不是刚修改后的版本。", &agentsdk.ConversationWriteScope{PersonalArtifacts: true})
	var file agentsdk.ConversationArtifactExport
	for _, step := range exported.Steps {
		for _, call := range step.Calls {
			if call.Name == "artifact_export" && call.Status == "completed" {
				var result struct {
					Export agentsdk.ConversationArtifactExport `json:"export"`
				}
				decode([]byte(call.ResultPreview), &result)
				file = result.Export
			}
		}
	}
	if file.ID == "" || file.Version != 1 {
		t.Fatal("model did not export original version")
	}
	if got := b.call("GET", "/agent/artifact-exports/"+file.ID+"/download", "", 200).Body.String(); got != original.Content.Markdown {
		t.Fatal("downloaded bytes differ from the original saved report")
	}
	decode(b.call("GET", "/agent/todos", "", 200).Body.Bytes(), &restored)
	if !restored.Complete || len(restored.Items) != 3 {
		t.Fatal("continuation created or removed unrelated todos")
	}
	for _, item := range restored.Items {
		if item.ID == second.ID {
			continue
		}
		if item.Status != "open" || item.DueDate != "" || item.Revision != 1 {
			t.Fatal("unrelated todo changed")
		}
	}
	decode(b.call("GET", "/agent/artifacts", "", 200).Body.Bytes(), &artifacts)
	if len(artifacts.Items) != 1 || artifacts.Items[0].ID != original.Artifact.ID || artifacts.Items[0].Version != 2 {
		t.Fatal("report was duplicated or had unexpected edits")
	}
	stream := b.call("GET", "/agent/conversations/"+continuation.ID+"/runs/"+exported.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(stream, "tool.completed") || !strings.Contains(stream, file.ID) {
		t.Fatal("real export missing from persisted SSE")
	}
	t.Log("verified real-model history retrieval, ordered todos, deadline, memory, full host restart, same-item continuation, report versions and original download bytes")
	servePersonalToolAcceptance(t, host, options)
}
