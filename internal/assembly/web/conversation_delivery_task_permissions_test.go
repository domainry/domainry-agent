package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type taskReceiptHTTPModel struct {
	peerWebModel
	entered  chan struct{}
	once     sync.Once
	released atomic.Bool
}

func (m *taskReceiptHTTPModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	last := in.Messages[len(in.Messages)-1]
	const prefix = "Task receipt call: "
	if last.Role == "user" && strings.HasPrefix(last.Content, prefix) {
		var call sdk.ConversationToolCall
		if err := json.Unmarshal([]byte(strings.TrimPrefix(last.Content, prefix)), &call); err != nil {
			return sdk.ConversationStepResult{}, err
		}
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, nil
	}
	if last.Role == "user" && strings.Contains(last.Content, "Hold task receipt") && !m.released.Load() {
		m.once.Do(func() { close(m.entered) })
		<-ctx.Done()
		return sdk.ConversationStepResult{}, ctx.Err()
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

func TestPeerTaskDeliveryReadsOriginalReceiptsAfterControlRevocation(t *testing.T) {
	const initial, changed = "Task-Receipt-Initial!26", "Task-Receipt-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "task-receipt-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "task-receipt-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &taskReceiptHTTPModel{entered: make(chan struct{})}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "task-receipts.db"), RuntimeID: "task-receipt-runtime", WorkspaceID: "task-receipt-workspace", ApplicationKey: "task-receipt-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"task-review": &peerWebModel{modelKey: "task-review"}}, Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("task receipt reading")}}})
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
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"task-receipts","title":"任务原回执"}`, 200))
	base := "/agent/conversations/" + conversation.ID
	refs := []sdk.ConversationResultReference{}
	original := map[string]string{}
	invoke := func(id, key string, args any) sdk.ConversationToolView {
		t.Helper()
		call := sdk.ConversationToolCall{ID: id, Name: key, Arguments: accountJSON(args)}
		run := accountDecode[sdk.ConversationRun](t, b.call("POST", base+"/messages", accountJSON(map[string]any{"client_message_id": id, "message": "Task receipt call: " + accountJSON(call), "write_scope": map[string]bool{"background_tasks": true}}), 202))
		deadline := time.Now().Add(60 * time.Second)
		for !run.Terminal() {
			if time.Now().After(deadline) {
				t.Fatalf("task receipt run timeout: %+v", run)
			}
			time.Sleep(10 * time.Millisecond)
			run = accountDecode[sdk.ConversationRun](t, b.call("GET", base+"/runs/"+run.ID, "", 200))
		}
		if run.Status != "completed" || run.AccessError != "" {
			t.Fatalf("task receipt run failed: %+v", run)
		}
		for _, step := range run.Steps {
			for _, view := range step.Calls {
				if view.ID == id && view.Status == "completed" && view.ResultReference != nil {
					refs = append(refs, *view.ResultReference)
					return view
				}
			}
		}
		t.Fatalf("missing actual task receipt: %+v", run)
		return sdk.ConversationToolView{}
	}
	started := invoke("start", "task_start", sdk.ConversationTaskStart{Goal: "Hold task receipt", Input: "Wait until the current task is explicitly cancelled, then resume once", AllowedTools: []string{}, Budget: sdk.ConversationTaskBudget{MaxSteps: 3, MaxToolCalls: 1, MaxOutputBytes: 4096, TimeoutSeconds: 300}})
	taskID := started.ResourceID
	select {
	case <-model.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("background task did not run")
	}
	invoke("list", "task_list", map[string]string{"scope": "current_conversation"})
	invoke("get", "task_get", map[string]string{"id": taskID})
	invoke("cancel", "task_cancel", map[string]string{"id": taskID})
	model.released.Store(true)
	invoke("resume", "task_resume", map[string]string{"id": taskID})
	waitTask := func(id string) sdk.ConversationTaskDetail {
		t.Helper()
		for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
			task := accountDecode[sdk.ConversationTaskDetail](t, b.call("GET", "/agent/conversation-tasks/"+id, "", 200))
			if task.Status == "completed" {
				return task
			}
			if task.Status == "failed" {
				t.Fatalf("task failed: %+v", task)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("task did not complete")
		return sdk.ConversationTaskDetail{}
	}
	done := waitTask(taskID)
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "task-review", Name: "任务回执复核", Description: "核对实际任务回执", Instructions: "完成一次独立复核", Tools: []string{}, SkillKeys: []string{}, ModelKey: "task-review", Enabled: true, MaxConcurrent: 1}), 200))
	var d sdk.ConversationDelegationDetail
	input := sdk.ConversationDelegationCreate{ClientID: "task-delivery", ConversationID: conversation.ID, AgentID: agent.ID, Purpose: "交付已核对的原始任务回执", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对任务原回执", Deliverable: "原始受理、查询、取消和恢复回执", CompletionConditions: []string{"保留原始任务回执"}}}
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	waitTask(d.TaskID)
	// The delegation completion wakes its issuing conversation. Read the
	// private delegated task from a separate ordinary conversation, as a user
	// can do in the UI while the issuer handles that actual notification.
	sourceBase := base
	readerConversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"task-receipt-reader","title":"读取任务回执"}`, 200))
	base = "/agent/conversations/" + readerConversation.ID
	invoke("delegated-get", "task_get", map[string]string{"id": d.TaskID})
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var value sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	d = read()
	update := sdk.ConversationDelegationUpdate{ClientID: "deliver-task-receipts", ExpectedRevision: d.Revision, Action: "deliver", Reason: "核对原任务受理和控制回执", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Summary: "原任务回执已核对", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "保留六份原始回执", Receipts: refs}}}}
	b.call("POST", path+"/decisions", accountJSON(update), 200)
	for _, ref := range refs {
		original[ref.CallID] = string(readReleasedResult(t, b, d.ID, 0, ref).Content)
	}
	// Let the real completion notice finish before revoking execution rights.
	for deadline := time.Now().Add(30 * time.Second); ; {
		root := accountDecode[sdk.Conversation](t, b.call("GET", sourceBase, "", 200))
		if root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion notice did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	grantPersonalTools(t, host, b, false)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read")
	assertRead := func() {
		t.Helper()
		history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
		if len(history.Items) == 0 {
			t.Fatal("missing task delivery history")
		}
		for _, ref := range refs {
			for _, revision := range []int64{0, history.Items[0].Revision} {
				if result := readReleasedResult(t, b, d.ID, revision, ref); string(result.Content) != original[ref.CallID] {
					t.Fatal("task receipt changed", ref.CallID)
				}
			}
		}
		current := accountDecode[sdk.ConversationTaskDetail](t, b.call("GET", "/agent/conversation-tasks/"+taskID, "", 200))
		if current.ExecutionRunID != done.ExecutionRunID || current.CompletionEventID != done.CompletionEventID || current.Status != "completed" {
			t.Fatal("receipt read resumed/cancelled original task", current)
		}
	}
	assertRead()
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	if value := read(); value.Delivery != nil {
		t.Fatal("delivery-only reader saw private delegated task details")
	}
	b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[len(refs)-1], MaxBytes: 4096}}), 403)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read")
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
	t.Log("Actual task start/list/get/cancel/resume, delegated task data permission, original current/history receipts and restart verified")
}
