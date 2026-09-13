package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

func TestPeerWriteDeliveryReadsOriginalMailReceiptWithoutSendPermission(t *testing.T) {
	f := newAccountWriteProductFixture(t)
	f.close()
	worker := &peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "mail-receipt"}, summary: "邮件服务已受理，实际送达状态未知，附原操作回执"}
	f.options.Agent.ConversationURL = ""
	f.options.Agent.ConversationProvider = &peerWebModel{}
	f.options.Agent.ConversationOptions.AgentModels = map[string]sdk.ConversationModel{"mail-receipt": worker}
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("write receipt collaboration")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	grantPersonalTools(t, f.host, b, true)
	grantCollaborationPermissions(t, f.host, b)
	account := f.connect(b)
	worker.sourceCalls = []sdk.ConversationToolCall{
		{ID: "send-catalog", Name: "mail_write_accounts", Arguments: `{"operation":"mail_send"}`},
		{ID: "send-original", Name: "mail_send", Arguments: accountJSON(map[string]any{"account_key": account.Key, "account_updated_at": account.UpdatedAt, "request": map[string]any{"message": map[string]any{"to": []any{map[string]string{"address": "to@example.test"}}, "cc": []any{map[string]string{"address": "cc@example.test"}}, "bcc": []any{map[string]string{"address": "bcc@example.test"}}, "subject": "回执读取验收", "text": accountWriteMailText}}})},
	}
	recipient := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"mail-receipt","name":"邮件协作","description":"按确认操作发送并交付回执","instructions":"请求确认后执行并交付回执","tools":["mail_write_accounts","mail_send","delegation_get","delegation_update"],"skill_keys":[],"model_key":"mail-receipt","enabled":true,"max_concurrent":1}`, 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"mail-receipt-source","title":"邮件回执交付"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "mail-receipt-work", ConversationID: conversation.ID, AgentID: recipient.ID, Purpose: "发送经确认的邮件", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "按确认内容发送邮件", Deliverable: "交付服务受理回执", CompletionConditions: []string{"提供原邮件服务回执并说明送达状态"}}}
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
	deadline := time.Now().Add(90 * time.Second)
	for {
		detail = read()
		if detail.Task != nil && detail.Task.ExecutionRunID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("write worker did not start: %+v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	runPath := "/agent/conversations/" + detail.ConversationID + "/runs/" + detail.Task.ExecutionRunID
	waiting := waitAccountWriteRun(t, b, runPath, "waiting_confirmation")
	if waiting.Interaction == nil || waiting.Interaction.CallID != "send-original" || len(f.counts()) != 0 {
		t.Fatal("missing exact confirmation or premature effect", waiting)
	}
	b.call("POST", runPath+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve-fixture-mail", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}), 200)
	waitAccountWriteRun(t, b, runPath, "completed")
	for {
		detail = read()
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if detail.Status == "delivered" && detail.Task != nil && detail.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipt was not delivered: %+v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if detail.Delivery == nil || len(detail.Delivery.Conditions) != 1 || len(detail.Delivery.Conditions[0].Receipts) != 2 {
		t.Fatalf("missing catalog/original receipt: %+v", detail)
	}
	refs := detail.Delivery.Conditions[0].Receipts
	effects := f.counts()
	requests := f.requests.Load()
	if !reflect.DeepEqual(effects, map[string]int{"mail_send": 1}) {
		t.Fatalf("unexpected effects: %+v", effects)
	}
	setting := settingList(t, b)["mail_send"]
	b.call("PUT", "/tools/preferences/mail_send", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("disabled send tool retained released receipt")
	}
	setting = settingList(t, b)["mail_send"]
	b.call("PUT", "/tools/preferences/mail_send", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	revoke := func(keys ...string) {
		mutateTestRolePermissions(t, f.host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				keep := true
				for _, key := range keys {
					keep = keep && p.PermissionKey != key
				}
				if keep {
					out = append(out, p)
				}
			}
			return out
		})
	}
	revoke("mail.send", "mail.write_accounts", integration.ActionIntegrationConnectionAccountsWrite)
	setTestCollaborationPermissions(t, f.host, b, "view", "delivery_read")
	assertRead := func() {
		t.Helper()
		d := read()
		if d.Delivery == nil || d.Delivery.Summary != worker.summary || d.Task != nil || len(d.Messages) != 0 {
			t.Fatalf("receipt reading coupled to execution or context leaked: %+v", d)
		}
		if body := b.call("GET", path+"/deliveries", "", 200).Body.String(); !strings.Contains(body, worker.summary) {
			t.Fatal("receipt history hidden")
		}
		if f.requests.Load() != requests || !reflect.DeepEqual(f.counts(), effects) {
			t.Fatal("receipt view performed vendor IO")
		}
	}
	assertRead()
	for _, ref := range refs {
		if result := readReleasedResult(t, b, detail.ID, 0, ref); result.Status != "completed" {
			t.Fatal("released original receipt unavailable without execution rights")
		}
	}
	for _, ref := range refs {
		rawPath := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", rawPath, "", 403)
		b.call("POST", rawPath+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	}
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	assertRead()
	revoke(integration.ActionIntegrationConnectionAccountsRead)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("revoked account data retained sent receipt")
	}
	if raw := b.call("GET", path+"/deliveries", "", 503).Body.String(); strings.Contains(raw, worker.summary) {
		t.Fatal("receipt leaked through history")
	}
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	revoke("mail.send", "mail.write_accounts", integration.ActionIntegrationConnectionAccountsWrite)
	assertRead()
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("revoked account retained sent receipt")
	}
	if f.requests.Load() != requests || !reflect.DeepEqual(f.counts(), effects) {
		t.Fatal("read/revocation resent mail")
	}
	// Do not claim provider acceptance is delivery; the exact accepted receipt
	// stays available only while the original account source remains authorized.
	raw, _ := json.Marshal(effects)
	t.Logf("real Agent/Identity/Tools/Integration/Provider HTTP: exact confirmation, effects=%s, saved catalog/receipt readable without send or account-write action and after restart; data/account/tool revocation hides result; no additional vendor IO", raw)
}
