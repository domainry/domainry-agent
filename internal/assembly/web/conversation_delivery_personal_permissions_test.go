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

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

type personalDeliveryModel struct{ peerReceiptDeliveryModel }

func (m *personalDeliveryModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	worker := m.peerReceiptDeliveryModel
	worker.sourceCalls = append([]sdk.ConversationToolCall(nil), m.sourceCalls...)
	var batch sdk.ConversationTodoBatch
	var todo sdk.ConversationTodo
	for _, message := range in.Messages {
		if message.Role != "tool" {
			continue
		}
		var result sdk.ConversationToolResult
		if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" {
			continue
		}
		switch message.ToolCallID {
		case "todo-create":
			_ = json.Unmarshal(result.Content, &batch)
		case "todo-get":
			_ = json.Unmarshal(result.Content, &todo)
		}
	}
	for i, call := range worker.sourceCalls {
		if call.ID == "todo-get" && len(batch.Items) == 1 {
			worker.sourceCalls[i].Arguments = accountJSON(map[string]string{"id": batch.Items[0].ID})
		}
		if call.ID == "todo-update" && todo.ID != "" {
			worker.sourceCalls[i].Arguments = accountJSON(map[string]any{"id": todo.ID, "expected_revision": todo.Revision, "patch": map[string]string{"status": "completed"}})
		}
	}
	return worker.StreamConversationStep(ctx, in, emit)
}

func TestPeerPersonalDeliveryReadsOriginalResultsWithOnlyDataReadPermissions(t *testing.T) {
	testPeerPersonalDeliveryReading(t, false)
}
func TestPeerPersonalDeletionDeliveryReadsOnlyOriginalAcknowledgements(t *testing.T) {
	testPeerPersonalDeliveryReading(t, true)
}

func testPeerPersonalDeliveryReading(t *testing.T, deletions bool) {
	const initial, changed = "Personal-Delivery-Initial!26", "Personal-Delivery-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "personal-delivery-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "personal-delivery-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	worker := &personalDeliveryModel{peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "personal-review"}, summary: "按确认完成个人工作，附计算、用户答复、记忆和待办的原始回执", sourceCalls: []sdk.ConversationToolCall{
		{ID: "calculate", Name: "calculate", Arguments: `{"operation":"expression","expression":"0.1 + 0.2","precision":2}`},
		{ID: "time", Name: "time_now", Arguments: `{"timezone":"Asia/Shanghai","relative_date":"tomorrow"}`},
		{ID: "answer", Name: "ask_user", Arguments: `{"question":"这份交付采用哪个季度？"}`},
		{ID: "memory-save", Name: "memory_save", Arguments: `{"title":"交付阅读偏好","content":"保留原始日期及出处","enabled":true,"expected_revision":0}`},
		{ID: "memory-search", Name: "memory_search", Arguments: `{"query":"交付阅读偏好"}`},
		{ID: "todo-create", Name: "todo_create", Arguments: `{"items":[{"title":"核对来源日期","description":"保留原始出处","timezone":"Asia/Shanghai"}]}`},
		{ID: "todo-list", Name: "todo_list", Arguments: `{"scope":"current_conversation"}`},
		{ID: "todo-get", Name: "todo_get", Arguments: `{}`},
		{ID: "todo-update", Name: "todo_update", Arguments: `{}`},
	}}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "personal-delivery.db"), RuntimeID: "personal-delivery-runtime", WorkspaceID: "personal-delivery-workspace", ApplicationKey: "personal-delivery-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"personal-review": worker}, Poll: 5 * time.Millisecond, MaxSteps: 16}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		adapters, err := host.ToolSettingsAdapters()
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("personal receipt delivery")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	b := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	var seedTodo sdk.ConversationTodo
	if deletions {
		memory := accountDecode[sdk.ConversationMemory](t, b.call("PUT", "/agent/conversations/memories/forget-receipt", `{"title":"删除测试","content":"该内容应被忘记","enabled":true,"expected_revision":0}`, 200))
		batch := accountDecode[sdk.ConversationTodoBatch](t, b.call("POST", "/agent/todos", accountJSON(sdk.ConversationTodoCreate{ClientID: "delete-receipt-seed", Items: []sdk.ConversationTodoInput{{Title: "删除这项待办", Timezone: "Asia/Shanghai"}}}), 200))
		seedTodo = batch.Items[0]
		worker.summary = "已按确认删除指定记忆与待办，仅交付删除回执"
		worker.sourceCalls = []sdk.ConversationToolCall{
			{ID: "forget", Name: "memory_forget", Arguments: accountJSON(map[string]any{"id": memory.ID, "expected_revision": memory.Revision})},
			{ID: "delete", Name: "todo_delete", Arguments: accountJSON(map[string]any{"id": seedTodo.ID, "expected_revision": seedTodo.Revision})},
		}
	}
	toolNames := []string{"delegation_get", "delegation_update"}
	for _, call := range worker.sourceCalls {
		toolNames = append(toolNames, call.Name)
	}
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "personal-review", Name: "个人工作协作", Description: "按明确确认处理个人资料并交付原始回执", Instructions: "按明确确认执行并提交原始回执", Tools: toolNames, SkillKeys: []string{}, ModelKey: "personal-review", Enabled: true, MaxConcurrent: 1}), 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"personal-delivery","title":"个人成果交付阅读"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "personal-review-work", ConversationID: conversation.ID, AgentID: agent.ID, Purpose: "完成明确确认的个人工作", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "根据用户答复处理个人事项", Deliverable: "每项实际结果的原始回执", CompletionConditions: []string{"提供各项原始回执"}}}
	var detail sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + detail.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	responded := map[string]bool{}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		detail = read()
		if detail.Task != nil && detail.Task.ExecutionRunID != "" {
			runPath := "/agent/conversations/" + detail.ConversationID + "/runs/" + detail.Task.ExecutionRunID
			run := accountDecode[sdk.ConversationRun](t, b.call("GET", runPath, "", 200))
			if run.Interaction != nil && !responded[run.Interaction.ID] && (run.Status == "waiting_confirmation" || run.Status == "waiting_user") {
				i := run.Interaction
				response := sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "respond-" + i.CallID, ExpectedRevision: i.Revision, Decision: "approve"}
				if i.Kind == "input" {
					response.Decision, response.Answer = "answer", "2026 年第二季度"
				}
				b.call("POST", runPath+"/respond", accountJSON(response), 200)
				responded[i.ID] = true
			}
		}
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if detail.Status == "delivered" && detail.Task != nil && detail.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) || detail.Task != nil && detail.Task.Status == "failed" {
			t.Fatalf("personal delivery failed: %+v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	wantResponses := 4
	if deletions {
		wantResponses = 2
	}
	if len(responded) != wantResponses || detail.Delivery == nil || len(detail.Delivery.Conditions) != 1 || len(detail.Delivery.Conditions[0].Receipts) != len(worker.sourceCalls) {
		t.Fatal("missing actual user responses or receipts", responded, detail.Delivery)
	}
	refs := detail.Delivery.Conditions[0].Receipts
	var memory sdk.ConversationMemory
	var todo sdk.ConversationTodo
	original := map[string]string{}
	for i, ref := range refs {
		result := readReleasedResult(t, b, detail.ID, 0, ref)
		original[ref.CallID] = string(result.Content)
		if worker.sourceCalls[i].Name == "memory_save" {
			var out struct {
				Memory sdk.ConversationMemory `json:"memory"`
			}
			_ = json.Unmarshal(result.Content, &out)
			memory = out.Memory
		}
		if worker.sourceCalls[i].Name == "todo_update" {
			_ = json.Unmarshal(result.Content, &todo)
		}
	}
	preference := worker.sourceCalls[0].Name
	setting, exists := settingList(t, b)[preference]
	if !exists {
		t.Fatal("producing tool missing from current settings", preference)
	}
	b.call("PUT", "/tools/preferences/"+preference, accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if d := read(); d.Delivery != nil {
		t.Fatal("disabled source retained delivery")
	}
	setting = settingList(t, b)[preference]
	b.call("PUT", "/tools/preferences/"+preference, accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	grantPersonalTools(t, host, b, false)
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		return append(p, identity.ProjectRolePermission{PermissionKey: sdk.ConversationToolActionPrefix + "todo_get", DataScope: identity.DataScopeOwner})
	})
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	assertRead := func() {
		t.Helper()
		d := read()
		if d.Delivery == nil || d.Delivery.Summary != worker.summary || d.Task != nil {
			t.Fatal("personal delivery hidden or execution exposed", d)
		}
		history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
		if len(history.Items) == 0 {
			t.Fatal("missing original delivery history")
		}
		for _, ref := range refs {
			for _, revision := range []int64{0, history.Items[0].Revision} {
				result := readReleasedResult(t, b, detail.ID, revision, ref)
				if string(result.Content) != original[ref.CallID] {
					t.Fatal("personal receipt changed", ref.CallID)
				}
			}
		}
		memories := accountDecode[[]sdk.ConversationMemory](t, b.call("GET", "/agent/conversations/memories", "", 200))
		if deletions {
			if len(memories) != 0 {
				t.Fatal("receipt read restored forgotten memory")
			}
			b.call("GET", "/agent/todos/"+seedTodo.ID, "", 404)
		} else {
			if len(memories) != 1 || memories[0].Revision != memory.Revision {
				t.Fatal("receipt read repeated memory save", memories)
			}
			current := accountDecode[sdk.ConversationTodo](t, b.call("GET", "/agent/todos/"+todo.ID, "", 200))
			if current.Revision != todo.Revision || current.Status != "completed" {
				t.Fatal("receipt read repeated or undid todo mutation", current)
			}
		}
	}
	assertRead()
	for _, ref := range refs {
		run := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", run, "", 403)
		b.call("POST", run+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	}
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		var out []identity.ProjectRolePermission
		for _, v := range p {
			if v.PermissionKey != sdk.ConversationToolActionPrefix+"todo_get" {
				out = append(out, v)
			}
		}
		return out
	})
	if d := read(); d.Delivery != nil {
		t.Fatal("revoked todo source read retained delivery")
	}
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		return append(p, identity.ProjectRolePermission{PermissionKey: sdk.ConversationToolActionPrefix + "todo_get", DataScope: identity.DataScopeOwner})
	})
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
	if !deletions {
		b.call("DELETE", "/agent/conversations/memories/"+memory.ID+"?expected_revision=1", "", 200)
		if d := read(); d.Delivery != nil {
			t.Fatal("forgotten memory retained delivery content")
		}
		body := b.call("GET", path+"/deliveries", "", 403).Body.String()
		if strings.Contains(body, worker.summary) || strings.Contains(body, memory.Content) {
			t.Fatal("history disclosed forgotten memory")
		}
	}
	t.Log("Actual personal tools, user responses, source data permissions, immutable current/history receipts, revocation and restart verified.")
}
