package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	tools "github.com/domainry/domainry-tools-sdk"
)

type historySourceHTTPModel struct {
	peerWebModel
	issued atomic.Int64
}

func (m *historySourceHTTPModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == "source-time" {
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "不可伪造的历史来源标记。" + strings.Repeat("保留原始日期和出处。", 100)}}, nil
		}
	}
	m.issued.Add(1)
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "source-time", Name: "time_now", Arguments: `{"timezone":"Asia/Shanghai"}`}}}}, nil
}

func TestPeerHistoryDeliveryReadsOriginalMessagesAndExecutionAfterToolRevocation(t *testing.T) {
	const initial, changed = "History-Receipt-Initial!26", "History-Receipt-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "history-receipt-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "history-receipt-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	source := &historySourceHTTPModel{peerWebModel: peerWebModel{modelKey: "history-source"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "history-receipts.db"), RuntimeID: "history-receipt-runtime", WorkspaceID: "history-receipt-workspace", ApplicationKey: "history-receipt-app", Agent: agentmodule.Options{ConversationProvider: &taskReceiptHTTPModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"history-source": source}, Poll: 5 * time.Millisecond}}}
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
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("history receipt reading")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
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
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "history-source", Name: "历史来源协作", Description: "保存可核对的原始工作记录", Instructions: "读取原始日期并完成说明", Tools: []string{"time_now"}, SkillKeys: []string{}, ModelKey: "history-source", Enabled: true, MaxConcurrent: 1}), 200))
	issuer := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"history-issuer","title":"原始协作"}`, 200))
	var d sdk.ConversationDelegationDetail
	input := sdk.ConversationDelegationCreate{ClientID: "history-delegation", ConversationID: issuer.ID, AgentID: agent.ID, Purpose: "记录实际历史与执行来源", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对日期并说明", Deliverable: "原始说明及执行记录", CompletionConditions: []string{"提供准确的历史与执行回执"}}}
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var v sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for deadline := time.Now().Add(60 * time.Second); ; {
		d = read()
		if d.Task != nil && d.Task.Status == "completed" && d.Task.Result != nil {
			break
		}
		if time.Now().After(deadline) || d.Task != nil && d.Task.Status == "failed" {
			t.Fatalf("source task failed: %+v", d)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var timeRef sdk.ConversationResultReference
	for _, step := range d.Task.Steps {
		for _, call := range step.Calls {
			if call.ID == "source-time" && call.ResultReference != nil {
				timeRef = *call.ResultReference
			}
		}
	}
	if timeRef.CallID == "" || source.issued.Load() != 1 {
		t.Fatal("missing original execution reference", d.Task)
	}
	reader := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"history-reader","title":"读取原始回执"}`, 200))
	base := "/agent/conversations/" + reader.ID
	refs := []sdk.ConversationResultReference{}
	calls := []sdk.ConversationToolCall{
		{ID: "history-search", Name: "history_search", Arguments: accountJSON(sdk.ConversationHistorySearch{Query: "不可伪造的历史来源标记", ConversationID: d.ConversationID})},
		{ID: "history-read", Name: "history_read", Arguments: accountJSON(map[string]any{"conversation_id": d.ConversationID, "message_id": d.Task.Result.MessageID, "max_bytes": 256})},
		{ID: "execution-read", Name: "execution_read", Arguments: accountJSON(sdk.ConversationExecutionRead{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID})},
		{ID: "result-read", Name: "tool_result_read", Arguments: accountJSON(sdk.ConversationResultRead{Reference: timeRef, MaxBytes: 256})},
	}
	invoke := func(call sdk.ConversationToolCall) {
		t.Helper()
		run := accountDecode[sdk.ConversationRun](t, b.call("POST", base+"/messages", accountJSON(map[string]any{"client_message_id": call.ID, "message": "Task receipt call: " + accountJSON(call)}), 202))
		for deadline := time.Now().Add(60 * time.Second); !run.Terminal(); {
			if time.Now().After(deadline) {
				t.Fatalf("history call timeout: %+v", run)
			}
			time.Sleep(10 * time.Millisecond)
			run = accountDecode[sdk.ConversationRun](t, b.call("GET", base+"/runs/"+run.ID, "", 200))
		}
		if run.Status != "completed" || run.AccessError != "" {
			t.Fatalf("history call failed: %+v", run)
		}
		for _, step := range run.Steps {
			for _, actual := range step.Calls {
				if actual.ID == call.ID && actual.Status == "completed" && actual.ResultReference != nil {
					refs = append(refs, *actual.ResultReference)
				}
			}
		}
	}
	for _, call := range calls {
		invoke(call)
	}
	if len(refs) == len(calls) {
		nested := sdk.ConversationToolCall{ID: "nested-result-read", Name: "tool_result_read", Arguments: accountJSON(sdk.ConversationResultRead{Reference: refs[len(refs)-1], MaxBytes: 256})}
		calls = append(calls, nested)
		invoke(nested)
	}
	if len(refs) != len(calls) {
		t.Fatal("missing history/execution wrapper receipts", refs)
	}
	d = read()
	update := sdk.ConversationDelegationUpdate{ClientID: "deliver-history", ExpectedRevision: d.Revision, Action: "deliver", Reason: "核对保存的原始消息和工具回执", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Summary: "四类原始记录及嵌套读取已核对", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "逐项核对五份实际读取结果", Receipts: refs}}}}
	b.call("POST", path+"/decisions", accountJSON(update), 200)
	original := map[string]string{}
	for _, ref := range refs {
		original[ref.CallID] = string(readReleasedResult(t, b, d.ID, 0, ref).Content)
	}
	var fragment struct {
		Complete bool   `json:"complete"`
		Content  string `json:"content"`
	}
	if json.Unmarshal([]byte(original["history-read"]), &fragment) != nil || fragment.Complete || len(fragment.Content) > 256 {
		t.Fatal("expected original partial UTF-8 history page")
	}
	setting := settingList(t, b)["time_now"]
	b.call("PUT", "/tools/preferences/time_now", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if value := read(); value.Delivery != nil {
		t.Fatal("disabled original source remained readable through wrappers")
	}
	setting = settingList(t, b)["time_now"]
	b.call("PUT", "/tools/preferences/time_now", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	for deadline := time.Now().Add(30 * time.Second); ; {
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+issuer.ID, "", 200))
		if root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source completion notice did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	grantPersonalTools(t, host, b, false)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read", "communicate")
	assertRead := func() {
		t.Helper()
		if value := read(); value.Delivery == nil || value.Delivery.Summary != update.Delivery.Summary {
			t.Fatal("independent history delivery hidden", value)
		}
		history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
		if len(history.Items) == 0 {
			t.Fatal("missing immutable history delivery")
		}
		for _, ref := range refs {
			for _, revision := range []int64{0, history.Items[0].Revision} {
				if result := readReleasedResultPages(t, b, d.ID, revision, ref, 256); string(result.Content) != original[ref.CallID] {
					t.Fatal("historical page was replaced", ref.CallID)
				}
			}
		}
		if source.issued.Load() != 1 {
			t.Fatal("source execution repeated during reading")
		}
	}
	assertRead()
	b.call("POST", "/agent/conversations/"+timeRef.ConversationID+"/runs/"+timeRef.RunID+"/result", accountJSON(sdk.ConversationResultRead{Reference: timeRef, MaxBytes: 4096}), 403)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "communicate")
	if value := read(); value.Delivery != nil {
		t.Fatal("same-delegation raw history ignored execution read revocation")
	}
	b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[0], MaxBytes: 256}}), 403)
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read", "execution_read", "communicate")
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
	t.Log("Actual history search/read, execution index/result slice, current data permissions, immutable pages, revocation and restart verified")
}
