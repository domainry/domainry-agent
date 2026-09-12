package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type taskStartWebModel struct {
	mu            sync.Mutex
	childAttempts int
	childCatalogs [][]string
	taskID        string
	childEntered  chan struct{}
	enteredOnce   sync.Once
}

func (m *taskStartWebModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{Content: "task fixture", Model: "task-fixture"}, nil
}
func (*taskStartWebModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "task-fixture", Fingerprint: "task-fixture-v1"}
}
func (m *taskStartWebModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	keys := make([]string, 0, len(in.Tools))
	parent := false
	for _, tool := range in.Tools {
		keys = append(keys, tool.Key)
		parent = parent || tool.Key == "task_start"
	}
	last := in.Messages[len(in.Messages)-1]
	if parent {
		if last.Role == "user" && strings.Contains(last.Content, "继续前台") {
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "前台消息已完成。"}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "查询后台任务") {
			m.mu.Lock()
			taskID := m.taskID
			m.mu.Unlock()
			if taskID == "" {
				return agentsdk.ConversationStepResult{}, errors.New("task ID missing before query")
			}
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{
				{ID: "task-list", Name: "task_list", Arguments: `{"status":"completed","scope":"current_conversation","limit":10}`},
				{ID: "task-get", Name: "task_get", Arguments: `{"id":"` + taskID + `"}`},
			}}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "后台生成成果") {
			arguments, _ := json.Marshal(agentsdk.ConversationTaskStart{
				Goal: "生成后台验收纪要", Input: "创建一份后台验收纪要", AllowedTools: []string{"artifact_create"},
				Budget: agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30},
			})
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "start-waiting-background", Name: "task_start", Arguments: string(arguments)}}}}, nil
		}
		if last.Role == "tool" && last.ToolCallID == "task-get" {
			m.mu.Lock()
			taskID := m.taskID
			m.mu.Unlock()
			if !strings.Contains(last.Content, taskID) || !strings.Contains(last.Content, `"status":"completed"`) || !strings.Contains(last.Content, `"completion_event_id":"task_event_`) || !strings.Contains(last.Content, "build 42 已在后台核对完成") {
				return agentsdk.ConversationStepResult{}, errors.New("task_get result incomplete")
			}
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "后台任务查询已确认。"}}, nil
		}
		if last.Role == "tool" {
			var result agentsdk.ConversationToolResult
			if json.Unmarshal([]byte(last.Content), &result) != nil || result.Status != "completed" || result.Completion != "accepted" || !strings.HasPrefix(result.ResourceID, "task_") {
				return agentsdk.ConversationStepResult{}, errors.New("task_start receipt missing")
			}
			m.mu.Lock()
			m.taskID = result.ResourceID
			m.mu.Unlock()
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "后台任务已受理：" + result.ResourceID}}, nil
		}
		arguments, _ := json.Marshal(agentsdk.ConversationTaskStart{
			Goal: "核对 build 42", Input: "确认当前时间并给出完成说明", AllowedTools: []string{"time_now"},
			Budget: agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30},
		})
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "start-background", Name: "task_start", Arguments: string(arguments)}}}}, nil
	}
	m.mu.Lock()
	m.childCatalogs = append(m.childCatalogs, append([]string(nil), keys...))
	creatingArtifact := len(keys) == 1 && keys[0] == "artifact_create"
	if creatingArtifact {
		m.mu.Unlock()
		if last.Role == "tool" {
			if !strings.Contains(last.Content, `"status":"completed"`) || !strings.Contains(last.Content, `"artifact"`) {
				return agentsdk.ConversationStepResult{}, errors.New("artifact result missing")
			}
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "后台验收纪要已创建。"}}, nil
		}
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "background-artifact", Name: "artifact_create", Arguments: `{"title":"后台验收纪要","content":{"kind":"markdown","markdown":"# 后台验收纪要\n\n任务已恢复并完成。"}}`}}}}, nil
	}
	if last.Role != "tool" {
		m.childAttempts++
		attempt := m.childAttempts
		m.mu.Unlock()
		if attempt == 1 {
			m.enteredOnce.Do(func() { close(m.childEntered) })
			<-ctx.Done()
			return agentsdk.ConversationStepResult{}, ctx.Err()
		}
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "background-time", Name: "time_now", Arguments: `{}`}}}}, nil
	}
	m.mu.Unlock()
	if !strings.Contains(last.Content, `"status":"completed"`) {
		return agentsdk.ConversationStepResult{}, errors.New("time_now result missing")
	}
	return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "build 42 已在后台核对完成。"}}, nil
}

func (m *taskStartWebModel) snapshot() (string, int, [][]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.taskID, m.childAttempts, append([][]string(nil), m.childCatalogs...)
}

func TestTaskStartThroughIdentityHTTPWorkerRecoveryAndSQLite(t *testing.T) {
	const initial, changed = "Initial-Task-Start!2", "Changed-Task-Start!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "task-start-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "task-start-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &taskStartWebModel{childEntered: make(chan struct{})}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "tasks.db"), RuntimeID: "task-runtime", WorkspaceID: "task-workspace", ApplicationKey: "task-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}},
	}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "task-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		seen := map[string]bool{}
		for _, permission := range previous {
			seen[permission.PermissionKey] = true
		}
		for _, definition := range agentsdk.ArtifactConversationTools() {
			if !seen[definition.ActionKey] {
				previous = append(previous, identitysdk.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
		}
		return previous
	})
	reopen := func() {
		if err := host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		host = nil
		open()
		b.handler = handler
		b.login("admin@example.com", changed)
	}
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"task-start-http","title":"后台任务验收"}`, 200).Body.Bytes(), &conversation)
	base := "/agent/conversations/" + conversation.ID
	var parent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-start-send","message":"请在后台核对 build 42","write_scope":{"personal_memory":false,"background_tasks":true}}`, 202).Body.Bytes(), &parent)
	waitRun := func(runID string) agentsdk.ConversationRun {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			var run agentsdk.ConversationRun
			_ = json.Unmarshal(b.call("GET", base+"/runs/"+runID, "", 200).Body.Bytes(), &run)
			if run.Terminal() {
				return run
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run timeout")
		return agentsdk.ConversationRun{}
	}
	parent = waitRun(parent.ID)
	taskID, _, _ := model.snapshot()
	if parent.Status != "completed" || taskID == "" || len(parent.Steps) != 2 || len(parent.Steps[0].Calls) != 1 || parent.Steps[0].Calls[0].Name != "task_start" || parent.Steps[0].Calls[0].Completion != "accepted" || parent.Steps[0].Calls[0].ResourceID != taskID {
		t.Fatalf("parent run/task receipt=%+v task=%q", parent, taskID)
	}
	select {
	case <-model.childEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("background worker did not begin")
	}
	var foreground agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"foreground-send","message":"继续前台处理这条消息"}`, 202).Body.Bytes(), &foreground)
	foreground = waitRun(foreground.ID)
	if foreground.Status != "completed" {
		t.Fatalf("background task blocked foreground run: %+v", foreground)
	}
	reopen()
	var messages agentsdk.ConversationMessagePage
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", base+"/messages?limit=20", "", 200).Body.Bytes(), &messages)
		if len(messages.Items) == 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var backgroundInput, backgroundResult agentsdk.ConversationMessage
	for _, message := range messages.Items {
		if message.BackgroundTaskID != taskID {
			continue
		}
		if message.Role == "user" {
			backgroundInput = message
		} else if message.Role == "assistant" {
			backgroundResult = message
		}
	}
	if len(messages.Items) != 6 || backgroundInput.ID == "" || backgroundResult.ID == "" || backgroundInput.RunID != backgroundResult.RunID || backgroundResult.Content != "build 42 已在后台核对完成。" {
		t.Fatalf("background message provenance=%+v", messages.Items)
	}
	child := waitRun(backgroundResult.RunID)
	if child.Status != "completed" || child.Attempt < 2 || child.BackgroundTask == nil || child.BackgroundTask.TaskID != taskID || len(child.BackgroundTask.ToolScope) != 1 || child.BackgroundTask.ToolScope[0].Key != "time_now" || child.BackgroundTask.Budget.MaxToolCalls != 1 {
		t.Fatalf("recovered background run=%+v", child)
	}
	taskIDFromModel, attempts, catalogs := model.snapshot()
	if taskIDFromModel != taskID || attempts < 2 || len(catalogs) < 3 {
		t.Fatalf("model task=%q attempts=%d catalogs=%v", taskIDFromModel, attempts, catalogs)
	}
	for _, catalog := range catalogs {
		if len(catalog) != 1 || catalog[0] != "time_now" {
			t.Fatalf("background model escaped frozen tool scope: %v", catalogs)
		}
	}
	var status string
	var payload []byte
	if err := host.db.QueryRowContext(t.Context(), `SELECT status, payload_json FROM _agent_conversation_tasks WHERE task_id = ?`, taskID).Scan(&status, &payload); err != nil {
		t.Fatal(err)
	}
	if status != agentsdk.ConversationTaskStatusCompleted || strings.Contains(string(payload), "process_id") || strings.Contains(string(payload), "task_definition") || !strings.Contains(string(payload), `"goal":"核对 build 42"`) || !strings.Contains(string(payload), `"result_message_id":"`+backgroundResult.ID+`"`) {
		t.Fatalf("independent task persistence status=%s payload=%s", status, payload)
	}
	var detail agentsdk.ConversationTaskDetail
	detailResponse := b.call("GET", "/agent/conversation-tasks/"+taskID, "", 200)
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != taskID || detail.Status != agentsdk.ConversationTaskStatusCompleted || detail.SourceConversationID != conversation.ID || detail.SourceRunID != parent.ID || detail.ExecutionRunID != child.ID || detail.Progress.RunStatus != "completed" || detail.Progress.Attempt < 2 || len(detail.Steps) != 2 || detail.Result == nil || detail.Result.MessageID != backgroundResult.ID || detail.Result.Preview != backgroundResult.Content || detail.CompletionEventID == "" || detail.CompletionEventSeq != child.LastEventSeq || !detail.ArtifactsComplete {
		t.Fatalf("task detail=%+v", detail)
	}
	if raw := detailResponse.Body.String(); strings.Contains(raw, "definition_hash") || strings.Contains(raw, "authorization_revision") || strings.Contains(raw, "tool_scope") {
		t.Fatalf("internal frozen scope leaked through task_get: %s", raw)
	}
	var taskPage agentsdk.ConversationTaskPage
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks?status=completed&source_conversation_id="+conversation.ID+"&limit=10", "", 200).Body.Bytes(), &taskPage)
	if len(taskPage.Items) != 1 || taskPage.Items[0].ID != taskID || !taskPage.Complete || taskPage.Items[0].Result == nil || taskPage.Items[0].Waiting != nil {
		t.Fatalf("task list=%+v", taskPage)
	}
	var queryRun agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-query-tools","message":"查询后台任务的完整状态"}`, 202).Body.Bytes(), &queryRun)
	queryRun = waitRun(queryRun.ID)
	if queryRun.Status != "completed" || len(queryRun.Steps) != 2 || len(queryRun.Steps[0].Calls) != 2 || queryRun.Steps[0].Calls[0].Name != "task_list" || queryRun.Steps[0].Calls[1].Name != "task_get" || queryRun.Steps[0].Calls[0].Status != "completed" || queryRun.Steps[0].Calls[1].Status != "completed" {
		t.Fatalf("task query tool run=%+v", queryRun)
	}
	var waitingParent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-waiting-tools","message":"请在后台生成成果","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &waitingParent)
	waitingParent = waitRun(waitingParent.ID)
	waitingTaskID, _, _ := model.snapshot()
	if waitingParent.Status != "completed" || waitingTaskID == "" || waitingTaskID == taskID {
		t.Fatalf("waiting task receipt parent=%+v id=%q", waitingParent, waitingTaskID)
	}
	var waitingDetail agentsdk.ConversationTaskDetail
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+waitingTaskID, "", 200).Body.Bytes(), &waitingDetail)
		if waitingDetail.Waiting != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waitingDetail.Status != agentsdk.ConversationTaskStatusRunning || waitingDetail.Progress.RunStatus != "waiting_confirmation" || waitingDetail.Waiting == nil || waitingDetail.Waiting.Kind != "confirmation" || waitingDetail.Waiting.Tool != "artifact_create" || waitingDetail.Waiting.Question == "" || waitingDetail.Interaction == nil || waitingDetail.Interaction.ID != waitingDetail.Waiting.ID || waitingDetail.CompletionEventID != "" {
		t.Fatalf("waiting task detail=%+v", waitingDetail)
	}
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks?status=running&source_conversation_id="+conversation.ID+"&limit=10", "", 200).Body.Bytes(), &taskPage)
	if len(taskPage.Items) != 1 || taskPage.Items[0].ID != waitingTaskID || taskPage.Items[0].Waiting == nil || taskPage.Items[0].Waiting.Tool != "artifact_create" {
		t.Fatalf("waiting task list=%+v", taskPage)
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: waitingDetail.Interaction.ID, ClientID: "approve-background-artifact", ExpectedRevision: waitingDetail.Interaction.Revision, Decision: "approve"}
	rawResponse, _ := json.Marshal(response)
	b.call("POST", base+"/runs/"+waitingDetail.ExecutionRunID+"/respond", string(rawResponse), 200)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		waitingDetail = agentsdk.ConversationTaskDetail{}
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+waitingTaskID, "", 200).Body.Bytes(), &waitingDetail)
		if waitingDetail.Status == agentsdk.ConversationTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waitingDetail.Status != agentsdk.ConversationTaskStatusCompleted || waitingDetail.Waiting != nil || waitingDetail.Result == nil || waitingDetail.Result.Preview != "后台验收纪要已创建。" || len(waitingDetail.Artifacts) != 1 || waitingDetail.Artifacts[0].Title != "后台验收纪要" || waitingDetail.Artifacts[0].SourceConversationID != conversation.ID || waitingDetail.Artifacts[0].SourceRunID != waitingDetail.ExecutionRunID || !waitingDetail.ArtifactsComplete {
		t.Fatalf("completed artifact task=%+v", waitingDetail)
	}
	t.Logf("task_start returned %s; child run %s recovered at attempt %d with exact catalog [time_now]", taskID, child.ID, child.Attempt)
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){"restart_task_host": reopen})
}
