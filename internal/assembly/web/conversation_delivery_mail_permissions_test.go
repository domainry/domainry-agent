package web

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

// The fixture can only deliver after receiving the real connector body and
// server-issued result reference. All reads use the normal durable executor.
type peerMailDeliveryModel struct {
	peerWebModel
	accountKey string // assigned before admission starts any model work
}

func (m *peerMailDeliveryModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	id, delivered, probe, probed := "", false, false, false
	var detail sdk.ConversationDelegationDetail
	var receipt *sdk.ConversationResultReference
	for _, message := range in.Messages {
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
			id = match[1]
		}
		probe = probe || message.Role == "user" && message.Content == "invoke-mail-after-revoke"
		if message.Role != "tool" {
			continue
		}
		probed = probed || message.ToolCallID == "mail-probe"
		var wire struct {
			sdk.ConversationToolResult
			Reference *sdk.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" {
			continue
		}
		switch message.ToolCallID {
		case "mail-source":
			if strings.Contains(string(wire.Content), mailFixtureBody) {
				receipt = wire.Reference
			}
		case "mail-agreement":
			_ = unmarshalPeerDetail(wire.Content, &detail)
		case "mail-deliver":
			delivered = true
		}
	}
	call := sdk.ConversationToolCall{}
	if probe && !probed {
		call = sdk.ConversationToolCall{ID: "mail-probe", Name: "mail_read", Arguments: accountJSON(map[string]string{"account_key": m.accountKey, "message_id": "mail-1"})}
	} else if id != "" && !delivered {
		switch {
		case receipt == nil:
			call = sdk.ConversationToolCall{ID: "mail-source", Name: "mail_read", Arguments: accountJSON(map[string]string{"account_key": m.accountKey, "message_id": "mail-1"})}
		case detail.ID == "":
			call = sdk.ConversationToolCall{ID: "mail-agreement", Name: "delegation_get", Arguments: accountJSON(map[string]string{"id": id})}
		default:
			call = sdk.ConversationToolCall{ID: "mail-deliver", Name: "delegation_update", Arguments: accountJSON(map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "已读取原邮件并保留回执", "delivery": map[string]any{"brief_version": detail.Brief.Version, "agreement_revision": detail.AgreementRevision, "summary": mailFixtureBody, "data": map[string]bool{"verified": true}, "conditions": []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "原邮件正文和保存回执", Receipts: []sdk.ConversationResultReference{*receipt}}}, "evidence": []any{}, "unresolved": []string{}}}})}
		}
	}
	if call.ID != "" {
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}, nil
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

func TestPeerDeliveryReadsMailWithoutToolExecutionPermission(t *testing.T) {
	f := newMailProductFixture(t)
	f.close()
	model := &peerMailDeliveryModel{peerWebModel: peerWebModel{modelKey: "mail-review"}}
	f.options.Agent.ConversationURL = ""
	f.options.Agent.ConversationProvider = model
	f.options.Agent.ConversationOptions.AgentModels = map[string]sdk.ConversationModel{"mail-review": model}
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("mail collaboration")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grantReader(b, b.readSession()["user_id"].(string))
	grantPersonalTools(t, f.host, b, true)
	grantCollaborationPermissions(t, f.host, b)
	account := f.connect(b, false)
	model.accountKey = account.Key
	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"mail-review","name":"邮件核查","description":"核对邮件资料","instructions":"读取邮件并交付核查结果","tools":["mail_read","delegation_get","delegation_update"],"skill_keys":[],"model_key":"mail-review","enabled":true,"max_concurrent":1}`, 200))
	source := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"mail-peer-source","title":"邮件交付读取"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "mail-read-work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "核对邮件", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对预算邮件", Deliverable: "带原回执的邮件摘要", CompletionConditions: []string{"说明原邮件内容和依据"}}}
	var d sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + d.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var out sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		d = read()
		if d.Status == "delivered" && d.Task != nil && d.Task.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail task did not deliver: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.Delivery == nil || d.Delivery.Summary != mailFixtureBody || len(d.Delivery.Conditions) != 1 || len(d.Delivery.Conditions[0].Receipts) != 1 || f.vendorCalls.Load() != 1 {
		t.Fatalf("missing real mail delivery: %+v; vendor=%d", d.Delivery, f.vendorCalls.Load())
	}
	ref := d.Delivery.Conditions[0].Receipts[0]
	runPath := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
	deadline = time.Now().Add(90 * time.Second)
	for {
		c := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+source.ID, "", 200))
		if c.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source notification did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	removePermission := func(key string) {
		mutateTestRolePermissions(t, f.host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if p.PermissionKey != key {
					out = append(out, p)
				}
			}
			return out
		})
	}
	// Configure the preference while this user still has the tool in their
	// editable catalog. Removing its execution Action also removes that entry.
	setting := settingList(t, b)["mail_read"]
	b.call("PUT", "/tools/preferences/mail_read", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("disabled tool's saved source remained readable")
	}
	setting = settingList(t, b)["mail_read"]
	b.call("PUT", "/tools/preferences/mail_read", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	removePermission("mail.read")
	setTestCollaborationPermissions(t, f.host, b, "view", "delivery_read")
	assertReadable := func() {
		t.Helper()
		d = read()
		if d.Delivery == nil || d.Delivery.Summary != mailFixtureBody || d.Task != nil || len(d.Messages) != 0 {
			t.Fatalf("delivery reading coupled to tool execution or exposed working context: %+v", d)
		}
		if body := b.call("GET", path+"/deliveries", "", 200).Body.String(); !strings.Contains(body, mailFixtureBody) {
			t.Fatal("delivery history lost authorized mail evidence")
		}
		b.call("GET", runPath, "", 403)
		b.call("POST", runPath+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
		if f.vendorCalls.Load() != 1 {
			t.Fatal("result reading fetched the mail again")
		}
	}
	assertReadable()
	// Even with collaboration execution_read, the direct raw-result endpoint
	// still needs mail.read. Delivery source verification cannot authorize it.
	setTestCollaborationPermissions(t, f.host, b, "view", "delivery_read", "execution_read")
	b.call("POST", runPath+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	setTestCollaborationPermissions(t, f.host, b, "view", "delivery_read")
	// The same current user can read the delivery but a model-requested mail
	// invocation is still rejected before any connector operation starts.
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"mail-denied-probe","title":"执行权限检查"}`, 200))
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: "denied-mail", Message: "invoke-mail-after-revoke"}), 202))
	deadline = time.Now().Add(90 * time.Second)
	for !run.Terminal() {
		if time.Now().After(deadline) {
			t.Fatal("denied invocation did not finish")
		}
		time.Sleep(20 * time.Millisecond)
		run = accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "failed" || run.ErrorCode == "" || f.vendorCalls.Load() != 1 {
		t.Fatalf("read grant authorized execution: %+v; vendor=%d", run, f.vendorCalls.Load())
	}
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Name == "mail_read" && (call.StartedAt != nil || call.ResultReference != nil || call.Status == "completed") {
				t.Fatalf("denied mail operation started: %+v", call)
			}
		}
	}
	// Reopen the exact temporary databases; neither startup nor a new login
	// restores mail.read, while current account data authorization still works.
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	assertReadable()
	removePermission(integration.ActionIntegrationConnectionAccountsRead)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("revoked source data exposed delivery")
	}
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	assertReadable()
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("revoked mailbox exposed delivery")
	}
	if f.vendorCalls.Load() != 1 {
		t.Fatal("revocation or source authorization made vendor calls")
	}
	t.Log("real mail HTTP once; delivery and receipt history readable after mail.read revocation and restart; raw execution denied; model invocation failed before vendor IO; tool disable, account data revocation and account revoke hide delivery")
}
