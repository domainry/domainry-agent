package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
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
	failAttempts  int
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
		if last.Role == "user" && strings.Contains(last.Content, "通过工具停止后台任务") {
			m.mu.Lock()
			taskID := m.taskID
			m.mu.Unlock()
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "task-cancel", Name: "task_cancel", Arguments: `{"id":"` + taskID + `"}`}}}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "通过工具继续后台任务") {
			m.mu.Lock()
			taskID := m.taskID
			m.mu.Unlock()
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "task-resume", Name: "task_resume", Arguments: `{"id":"` + taskID + `"}`}}}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "后台生成成果") {
			arguments, _ := json.Marshal(agentsdk.ConversationTaskStart{
				Goal: "生成后台验收纪要", Input: "创建一份后台验收纪要", AllowedTools: []string{"artifact_create"},
				Budget: agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30},
			})
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "start-waiting-background", Name: "task_start", Arguments: string(arguments)}}}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "后台等待补充") {
			goal := "等待用户补充发布渠道"
			if strings.Contains(last.Content, "浏览器") {
				goal = "浏览器等待取消任务"
			}
			arguments, _ := json.Marshal(agentsdk.ConversationTaskStart{
				Goal: goal, Input: "向用户询问发布渠道", AllowedTools: []string{"ask_user"},
				Budget: agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30},
			})
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "start-input-background", Name: "task_start", Arguments: string(arguments)}}}}, nil
		}
		if last.Role == "user" && strings.Contains(last.Content, "后台失败后重试") {
			goal := "失败后恢复任务"
			if strings.Contains(last.Content, "浏览器") {
				goal = "浏览器失败恢复任务"
			}
			arguments, _ := json.Marshal(agentsdk.ConversationTaskStart{
				Goal: goal, Input: "首次失败后由用户明确继续", AllowedTools: []string{"time_now"},
				Budget: agentsdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 1024, TimeoutSeconds: 30},
			})
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "start-failing-background", Name: "task_start", Arguments: string(arguments)}}}}, nil
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
		if last.Role == "tool" && (last.ToolCallID == "task-cancel" || last.ToolCallID == "task-resume") {
			if !strings.Contains(last.Content, `"status":"completed"`) || !strings.Contains(last.Content, `"task"`) {
				return agentsdk.ConversationStepResult{}, errors.New("task control result incomplete")
			}
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "后台任务控制已完成。"}}, nil
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
	failing := false
	for index := len(in.Messages) - 1; index >= 0; index-- {
		if in.Messages[index].Role == "user" {
			failing = strings.Contains(in.Messages[index].Content, "首次失败后由用户明确继续")
			break
		}
	}
	if failing {
		if last.Role == "tool" {
			m.mu.Unlock()
			if !strings.Contains(last.Content, `"status":"completed"`) {
				return agentsdk.ConversationStepResult{}, errors.New("resumed task time result missing")
			}
			return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "失败任务已恢复完成。"}}, nil
		}
		m.failAttempts++
		attempt := m.failAttempts
		m.mu.Unlock()
		if attempt == 1 {
			return agentsdk.ConversationStepResult{}, errors.New("injected background failure")
		}
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "resumed-background-time", Name: "time_now", Arguments: `{}`}}}}, nil
	}
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
	asking := len(keys) == 1 && keys[0] == "ask_user"
	if asking {
		m.mu.Unlock()
		if last.Role == "tool" {
			return agentsdk.ConversationStepResult{}, errors.New("input task unexpectedly continued")
		}
		return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "task-fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "background-question", Name: "ask_user", Arguments: `{"question":"请选择发布渠道","choices":["邮件","知识库"]}`}}}}, nil
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

func TestScheduledTaskUsesCurrentIdentityAndWaitsForUserConfirmation(t *testing.T) {
	const initial, changed = "Initial-Scheduled-Task!2", "Changed-Scheduled-Task!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "scheduled-task-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "scheduled-task-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &taskStartWebModel{childEntered: make(chan struct{})}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "scheduled-tasks.db"), RuntimeID: "scheduled-runtime", WorkspaceID: "scheduled-workspace", ApplicationKey: "scheduled-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}},
	}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "task-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	artifactCreate := agentsdk.ArtifactConversationTools()[0]
	for _, definition := range agentsdk.ArtifactConversationTools() {
		if definition.Key == "artifact_create" {
			artifactCreate = definition
		}
	}
	setArtifactCreate := func(enabled bool) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := make([]identitysdk.ProjectRolePermission, 0, len(previous)+1)
			for _, permission := range previous {
				if permission.PermissionKey != artifactCreate.ActionKey {
					out = append(out, permission)
				}
			}
			if enabled {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: artifactCreate.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
			return out
		})
	}
	setArtifactCreate(true)
	var conversation agentsdk.Conversation
	if err = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"scheduled-confirmation","title":"计划确认"}`, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	service := host.Agent.(agentsdk.ConversationBinding).Conversations()
	scheduled := service.(agentsdk.ScheduledConversationTaskService)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	request := agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion,
		PlanID:          "weekly-artifact", SchedulerRunID: "scheduled-run-1", IdempotencyKey: "scheduled-window-1", ScheduledFor: time.Now().UTC(),
		Authority: authority, ConversationID: conversation.ID,
		Input:          agentsdk.ConversationTaskStart{Goal: "生成后台验收纪要", Input: `{"kind":"weekly"}`, AllowedTools: []string{artifactCreate.Key}},
		AllowedActions: []string{artifactCreate.ActionKey},
	}
	ctx := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience)
	receipt, err := scheduled.StartScheduledConversationTask(ctx, request)
	if err != nil || receipt.Task.ID == "" {
		t.Fatalf("scheduled receipt=%+v err=%v", receipt, err)
	}
	tasks := service.(agentsdk.ConversationTaskService)
	var waiting agentsdk.ConversationTaskDetail
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		waiting, err = tasks.ConversationTask(t.Context(), receipt.Task.ID, authority)
		if err != nil {
			t.Fatal(err)
		}
		if waiting.Progress.RunStatus == "waiting_confirmation" && waiting.Interaction != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waiting.Progress.RunStatus != "waiting_confirmation" || waiting.Interaction == nil || waiting.Interaction.Tool != artifactCreate.Key {
		t.Fatalf("scheduled task did not release its worker for confirmation: %+v", waiting)
	}
	setArtifactCreate(false)
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "scheduled-approve-after-revoke", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}
	if _, err = service.(agentsdk.ConversationInteractionService).Respond(t.Context(), conversation.ID, waiting.ExecutionRunID, response, authority); err == nil {
		t.Fatal("revoked scheduled write was approved")
	}
	var artifactVersions int
	if err = host.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_artifact_versions`).Scan(&artifactVersions); err != nil || artifactVersions != 0 {
		t.Fatalf("revoked confirmation created artifact versions: count=%d err=%v", artifactVersions, err)
	}
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
	setArtifactPermissions := func(denied string) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, permission := range previous {
				if !strings.HasPrefix(permission.PermissionKey, agentsdk.ConversationToolActionPrefix+"artifact_") {
					out = append(out, permission)
				}
			}
			for _, definition := range agentsdk.ArtifactConversationTools() {
				if definition.Key != denied {
					out = append(out, identitysdk.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identitysdk.DataScopeOwner})
				}
			}
			return out
		})
	}
	setArtifactPermissions("")
	setTimePermission := func(enabled bool) {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			out := []identitysdk.ProjectRolePermission{}
			for _, permission := range previous {
				if permission.PermissionKey != agentsdk.ConversationToolActionPrefix+"time_now" {
					out = append(out, permission)
				}
			}
			if enabled {
				out = append(out, identitysdk.ProjectRolePermission{PermissionKey: agentsdk.ConversationToolActionPrefix + "time_now", DataScope: identitysdk.DataScopeOwner})
			}
			return out
		})
	}
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
		var run agentsdk.ConversationRun
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			_ = json.Unmarshal(b.call("GET", base+"/runs/"+runID, "", 200).Body.Bytes(), &run)
			if run.Terminal() {
				return run
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("run timeout: %+v", run)
		return run
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
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
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
	listResponse := b.call("GET", "/agent/conversation-tasks?status=completed&source_conversation_id="+conversation.ID+"&limit=10", "", 200)
	_ = json.Unmarshal(listResponse.Body.Bytes(), &taskPage)
	if len(taskPage.Items) != 1 || taskPage.Items[0].ID != taskID || !taskPage.Complete || taskPage.Items[0].Result == nil || taskPage.Items[0].Waiting != nil {
		t.Fatalf("task list=%+v", taskPage)
	}
	if raw := listResponse.Body.String(); strings.Contains(raw, "definition_hash") || strings.Contains(raw, "authorization_revision") || strings.Contains(raw, "tool_scope") {
		t.Fatalf("internal frozen scope leaked through task_list: %s", raw)
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
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
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
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		waitingDetail = agentsdk.ConversationTaskDetail{}
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+waitingTaskID, "", 200).Body.Bytes(), &waitingDetail)
		if waitingDetail.Status == agentsdk.ConversationTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if waitingDetail.Status != agentsdk.ConversationTaskStatusCompleted || waitingDetail.Waiting != nil || waitingDetail.Control.CanCancel || waitingDetail.Control.CanResume || waitingDetail.Result == nil || waitingDetail.Result.Preview != "后台验收纪要已创建。" || len(waitingDetail.Artifacts) != 1 || waitingDetail.Artifacts[0].Title != "后台验收纪要" || waitingDetail.Artifacts[0].SourceConversationID != conversation.ID || waitingDetail.Artifacts[0].SourceRunID != waitingDetail.ExecutionRunID || !waitingDetail.ArtifactsComplete {
		t.Fatalf("completed artifact task=%+v", waitingDetail)
	}
	setArtifactPermissions("artifact_read")
	var restricted agentsdk.ConversationTaskDetail
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+waitingTaskID, "", 200).Body.Bytes(), &restricted)
	if restricted.AccessError == "" || restricted.Control.CanCancel || restricted.Control.CanResume || len(restricted.Artifacts) != 0 || restricted.Result != nil || len(restricted.Steps) != 0 {
		t.Fatalf("revoked artifact source remained in task projection: %+v", restricted)
	}
	setArtifactPermissions("")
	waitingDetail = agentsdk.ConversationTaskDetail{}
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+waitingTaskID, "", 200).Body.Bytes(), &waitingDetail)
	if len(waitingDetail.Artifacts) != 1 || waitingDetail.Result == nil || len(waitingDetail.Steps) != 2 {
		t.Fatalf("restored task projection=%+v", waitingDetail)
	}
	var inputParent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-input-tools","message":"请在后台等待补充发布渠道","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &inputParent)
	inputParent = waitRun(inputParent.ID)
	inputTaskID, _, _ := model.snapshot()
	var inputDetail agentsdk.ConversationTaskDetail
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+inputTaskID, "", 200).Body.Bytes(), &inputDetail)
		if inputDetail.Waiting != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if inputParent.Status != "completed" || inputDetail.Status != agentsdk.ConversationTaskStatusRunning || inputDetail.Progress.RunStatus != "waiting_user" || !inputDetail.Control.CanCancel || inputDetail.Control.CanResume || inputDetail.Control.ResumeBlocker != "interaction_response_required" || inputDetail.Waiting == nil || inputDetail.Waiting.Kind != "input" || inputDetail.Waiting.Question != "请选择发布渠道" || inputDetail.Interaction == nil || len(inputDetail.Interaction.Choices) != 2 {
		t.Fatalf("input waiting task=%+v parent=%+v", inputDetail, inputParent)
	}
	b.call("POST", "/agent/conversation-tasks/"+inputTaskID+"/resume", "", 409)
	var cancelParent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-cancel-tools","message":"请通过工具停止后台任务","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &cancelParent)
	cancelParent = waitRun(cancelParent.ID)
	var cancelledInput agentsdk.ConversationTaskDetail
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+inputTaskID, "", 200).Body.Bytes(), &cancelledInput)
	if cancelParent.Status != "completed" || cancelledInput.Status != agentsdk.ConversationTaskStatusCancelled || cancelledInput.Control.CanCancel || cancelledInput.Control.CanResume || cancelledInput.Control.ResumeBlocker != "interaction_closed" || cancelledInput.Waiting != nil || cancelledInput.CompletionEventID == "" || cancelledInput.CompletionEventSeq < 1 {
		t.Fatalf("cancelled waiting task=%+v parent=%+v", cancelledInput, cancelParent)
	}
	var cancelledAgain agentsdk.ConversationTaskDetail
	_ = json.Unmarshal(b.call("POST", "/agent/conversation-tasks/"+inputTaskID+"/cancel", "", 200).Body.Bytes(), &cancelledAgain)
	if cancelledAgain.Status != agentsdk.ConversationTaskStatusCancelled || cancelledAgain.CompletionEventID != cancelledInput.CompletionEventID {
		t.Fatalf("idempotent cancellation changed task: before=%+v after=%+v", cancelledInput, cancelledAgain)
	}
	b.call("POST", "/agent/conversation-tasks/"+inputTaskID+"/resume", "", 409)

	var failedParent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-failed-tools","message":"请创建后台失败后重试任务","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &failedParent)
	failedParent = waitRun(failedParent.ID)
	failedTaskID, _, _ := model.snapshot()
	var failedDetail agentsdk.ConversationTaskDetail
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+failedTaskID, "", 200).Body.Bytes(), &failedDetail)
		if failedDetail.Status == agentsdk.ConversationTaskStatusFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if failedParent.Status != "completed" || failedDetail.Status != agentsdk.ConversationTaskStatusFailed || failedDetail.Control.CanCancel || !failedDetail.Control.CanResume || failedDetail.Control.ResumeBlocker != "" || failedDetail.ErrorCode == "" || failedDetail.CompletionEventID == "" {
		t.Fatalf("failed background task=%+v parent=%+v", failedDetail, failedParent)
	}
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, permission := range previous {
			if permission.PermissionKey != agentsdk.ConversationToolActionPrefix+"task_resume" {
				out = append(out, permission)
			}
		}
		return out
	})
	b.call("POST", "/agent/conversation-tasks/"+failedTaskID+"/resume", "", 403)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		return append(previous, identitysdk.ProjectRolePermission{PermissionKey: agentsdk.ConversationToolActionPrefix + "task_resume", DataScope: identitysdk.DataScopeOwner})
	})
	setTimePermission(false)
	b.call("POST", "/agent/conversation-tasks/"+failedTaskID+"/resume", "", 200)
	var deniedResume agentsdk.ConversationTaskDetail
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+failedTaskID, "", 200).Body.Bytes(), &deniedResume)
		if deniedResume.Status == agentsdk.ConversationTaskStatusFailed && deniedResume.Progress.Attempt >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if deniedResume.Status != agentsdk.ConversationTaskStatusFailed || deniedResume.Progress.Attempt < 2 || deniedResume.ErrorCode != "tool_access_denied" || !deniedResume.Control.CanResume {
		t.Fatalf("revoked frozen tool was admitted on resume: %+v", deniedResume)
	}
	setTimePermission(true)
	var resumeParent agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"task-resume-tools","message":"请通过工具继续后台任务","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &resumeParent)
	resumeParent = waitRun(resumeParent.ID)
	if resumeParent.Status != "completed" || len(resumeParent.Steps) == 0 || len(resumeParent.Steps[0].Calls) != 1 || resumeParent.Steps[0].Calls[0].Name != "task_resume" || !strings.Contains(resumeParent.Steps[0].Calls[0].ResultPreview, `"task"`) || strings.Contains(resumeParent.Steps[0].Calls[0].ResultPreview, deniedResume.CompletionEventID) {
		t.Fatalf("task_resume tool did not return a fresh running receipt: %+v", resumeParent)
	}
	var resumedDetail agentsdk.ConversationTaskDetail
	_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+failedTaskID, "", 200).Body.Bytes(), &resumedDetail)
	if resumedDetail.Status != agentsdk.ConversationTaskStatusRunning && resumedDetail.Status != agentsdk.ConversationTaskStatusCompleted {
		t.Fatalf("resumed task did not clear terminal receipt: %+v parent=%+v", resumedDetail, resumeParent)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+failedTaskID, "", 200).Body.Bytes(), &resumedDetail)
		if resumedDetail.Status == agentsdk.ConversationTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if resumedDetail.Status != agentsdk.ConversationTaskStatusCompleted || resumedDetail.Progress.Attempt < 3 || resumedDetail.Progress.ToolCalls != 1 || resumedDetail.Result == nil || resumedDetail.Result.Preview != "失败任务已恢复完成。" || resumedDetail.CompletionEventID == "" {
		t.Fatalf("completed resumed task=%+v", resumedDetail)
	}
	if os.Getenv("AGENT_TOOL_UI_ACCEPTANCE") == "1" {
		var browserWaitingParent agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"browser-task-waiting","message":"请创建浏览器后台等待补充任务","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &browserWaitingParent)
		browserWaitingParent = waitRun(browserWaitingParent.ID)
		browserWaitingID, _, _ := model.snapshot()
		var browserWaiting agentsdk.ConversationTaskDetail
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+browserWaitingID, "", 200).Body.Bytes(), &browserWaiting)
			if browserWaiting.Waiting != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if browserWaitingParent.Status != "completed" || browserWaiting.Goal != "浏览器等待取消任务" || browserWaiting.Waiting == nil || !browserWaiting.Control.CanCancel {
			t.Fatalf("browser waiting seed=%+v parent=%+v", browserWaiting, browserWaitingParent)
		}
		model.mu.Lock()
		model.failAttempts = 0
		model.mu.Unlock()
		var browserFailedParent agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"browser-task-failed","message":"请创建浏览器后台失败后重试任务","write_scope":{"background_tasks":true}}`, 202).Body.Bytes(), &browserFailedParent)
		browserFailedParent = waitRun(browserFailedParent.ID)
		browserFailedID, _, _ := model.snapshot()
		var browserFailed agentsdk.ConversationTaskDetail
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			_ = json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+browserFailedID, "", 200).Body.Bytes(), &browserFailed)
			if browserFailed.Status == agentsdk.ConversationTaskStatusFailed {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if browserFailedParent.Status != "completed" || browserFailed.Goal != "浏览器失败恢复任务" || browserFailed.Status != agentsdk.ConversationTaskStatusFailed || !browserFailed.Control.CanResume {
			t.Fatalf("browser failed seed=%+v parent=%+v", browserFailed, browserFailedParent)
		}
		t.Logf("browser task controls seeded waiting=%s failed=%s", browserWaitingID, browserFailedID)
	}
	t.Logf("task_start returned %s; child run %s recovered at attempt %d with exact catalog [time_now]", taskID, child.ID, child.Attempt)
	servePersonalToolAcceptanceWithHost(t, func() *Host { return host }, options, map[string]func(){"restart_task_host": reopen})
}
