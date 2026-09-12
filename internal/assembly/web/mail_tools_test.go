package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func TestMailProductIdentityDraftSourcesAndRestart(t *testing.T) {
	f := newMailProductFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("mail")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grantReader(b, b.readSession()["user_id"].(string))
	for _, d := range toolmodule.MailDefinitions() {
		if settingList(t, b)[d.Key].Available {
			t.Fatal("unconnected mail available")
		}
	}
	account := f.connect(b, false)
	for _, d := range toolmodule.MailDefinitions() {
		if !settingList(t, b)[d.Key].Available {
			t.Fatalf("mail unavailable %s", d.Key)
		}
	}
	conversation, run, answer := mailRun(t, b, "搜索预算邮件，提取待处理事项，并保存、修改和导出回复草稿。", true)
	for _, s := range []string{"已搜索 2 封邮件", mailFixtureBody, "负责人和承诺待确认", "版本 2", "未发送", account.Key, "<budget-mail-1@example.com>"} {
		if !strings.Contains(answer, s) {
			t.Fatalf("answer missing %s: %s", s, answer)
		}
	}
	names := []string{}
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status != "completed" {
				t.Fatalf("failed call %+v", call)
			}
			names = append(names, call.Name)
		}
	}
	if strings.Join(names, ",") != "mail_accounts,mail_list,mail_search,mail_search,mail_read,artifact_create,artifact_read,artifact_edit,artifact_export" {
		t.Fatalf("wrong tool chain %v", names)
	}
	if f.vendorCalls.Load() != 7 {
		t.Fatalf("unexpected vendor requests %d", f.vendorCalls.Load())
	}
	page := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
	if len(page.Items) != 1 {
		t.Fatalf("draft missing %+v", page)
	}
	draft := page.Items[0]
	if draft.SourceConversationID != conversation || draft.SourceRunID != run.ID || draft.Version != 2 {
		t.Fatalf("draft provenance %+v", draft)
	}
	path := "/agent/artifacts/" + draft.ID
	version := accountDecode[sdk.ConversationArtifactVersion](t, b.call("GET", path, "", 200))
	for _, s := range []string{"未发送", "budget@example.com", mailFixtureBody, account.Key, "mail-1", "thread-budget", "<budget-mail-1@example.com>", "核对后再回复"} {
		if !strings.Contains(version.Content.Markdown, s) {
			t.Fatalf("draft lost %s", s)
		}
	}
	exported := accountDecode[sdk.ConversationArtifactExport](t, b.call("POST", path+"/exports", accountJSON(sdk.ConversationArtifactExportRequest{ClientID: "mail-e2e-download", Version: 2, Format: "markdown"}), 200))
	if got := b.call("GET", "/agent/artifact-exports/"+exported.ID+"/download", "", 200).Body.String(); got != version.Content.Markdown {
		t.Fatal("download differs from source draft")
	}
	sse := b.call("GET", "/agent/conversations/"+conversation+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(sse, "mail_read") || !strings.Contains(sse, "artifact_create") || strings.Contains(sse, "fixture-access") || strings.Contains(sse, "fixture-refresh") {
		t.Fatal("SSE evidence boundary")
	}
	assertHidden := func() {
		t.Helper()
		messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
		found := false
		for _, m := range messages.Items {
			if m.Role == "assistant" {
				found = true
				if m.AccessError == "" || strings.Contains(m.Content, "MAIL-BODY") {
					t.Fatalf("retained mail exposed %+v", m)
				}
			}
		}
		if !found {
			t.Fatal("missing retained answer")
		}
		b.call("GET", path, "", 503)
		b.call("GET", "/agent/artifact-exports/"+exported.ID+"/download", "", 503)
		b.call("POST", path+"/exports", accountJSON(sdk.ConversationArtifactExportRequest{ClientID: fmt.Sprintf("denied-export-%d", time.Now().UnixNano()), Version: 2, Format: "markdown"}), 503)
		b.call("PATCH", path, accountJSON(map[string]any{"client_id": fmt.Sprintf("denied-edit-%d", time.Now().UnixNano()), "expected_version": 2, "patch": map[string]string{"title": "forbidden"}}), 503)
		current := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
		if len(current.Items) != 0 || !current.Omitted {
			t.Fatalf("denied source not omitted %+v", current)
		}
	}
	setting := settingList(t, b)["mail_read"]
	b.call("PUT", "/tools/preferences/mail_read", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	assertHidden()
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	if settingList(t, b)["mail_read"].Enabled {
		t.Fatal("restart lost mail choice")
	}
	assertHidden()
	setting = settingList(t, b)["mail_read"]
	b.call("PUT", "/tools/preferences/mail_read", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if current := accountDecode[sdk.ConversationArtifactVersion](t, b.call("GET", path, "", 200)); current.Content.Markdown != version.Content.Markdown {
		t.Fatal("restart lost original draft")
	}
	for _, action := range []string{integration.ActionIntegrationConnectionAccountsList, integration.ActionIntegrationConnectionAccountsRead} {
		mutateTestRolePermissions(t, f.host, b, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range prior {
				if p.PermissionKey != action {
					out = append(out, p)
				}
			}
			return out
		})
		assertHidden()
		b.call("POST", "/app/product/account-setup", `{}`, 200)
		b.call("GET", path, "", 200)
	}
	other := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	other.login("system_administrator@example.com", accountInitial)
	other.changePassword(accountInitial, accountChanged)
	f.grantReader(b, other.readSession()["user_id"].(string))
	for _, d := range toolmodule.MailDefinitions() {
		if settingList(t, other)[d.Key].Available {
			t.Fatal("personal mailbox visible to other user")
		}
	}
	other.call("GET", "/agent/conversations/"+conversation+"/messages", "", 404)
	other.call("GET", path, "", 404)
	f.partial.Store(true)
	_, _, partial := mailRun(t, b, "整理不完整邮件并保存草稿", true)
	if !strings.Contains(partial, "正文不完整") || strings.Contains(partial, "已保存") {
		t.Fatal(partial)
	}
	if current := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200)); len(current.Items) != 1 {
		t.Fatal("partial body generated draft")
	}
	f.partial.Store(false)
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	assertHidden()
	before := f.vendorCalls.Load()
	_, _, revoked := mailRun(t, b, "读取邮件", false)
	if strings.Contains(revoked, "MAIL-BODY") || f.vendorCalls.Load() != before {
		t.Fatal("revoked mail reached vendor/model")
	}
	f.connect(b, true)
	settings := settingList(t, b)
	if !settings["mail_list"].Available || !settings["mail_accounts"].Available || settings["mail_read"].Available || settings["mail_search"].Available {
		t.Fatal("basic scope availability", settings)
	}
	_, _, basic := mailRun(t, b, "总结邮件并创建回复草稿", true)
	if !strings.Contains(basic, "仅有邮件头权限") || strings.Contains(basic, "MAIL-BODY") {
		t.Fatal(basic)
	}
	t.Logf("9 tool calls with draft v2; retained conversation, draft read/edit/export/download denied after tool, list, read and account revocation; restart and two users; partial/basic scope; vendor HTTP=%d model HTTP=%d", f.vendorCalls.Load(), f.modelCalls.Load())
}

func mailRun(t *testing.T, b *browser, message string, write bool) (string, sdk.ConversationRun, string) {
	t.Helper()
	id := fmt.Sprintf("mail-%d", time.Now().UnixNano())
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: id}), 200))
	input := sdk.ConversationSend{ClientMessageID: id, Message: message}
	if write {
		input.WriteScope = &sdk.ConversationWriteScope{PersonalArtifacts: true}
	}
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(input), 202))
	deadline := time.Now().Add(3 * time.Minute)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		run = accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "completed" {
		t.Fatalf("mail run %s error=%s steps=%+v", run.Status, run.ErrorCode, run.Steps)
	}
	messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+c.ID+"/messages", "", 200))
	for _, m := range messages.Items {
		if m.Role == "assistant" {
			return c.ID, run, m.Content
		}
	}
	t.Fatal("mail answer missing")
	return "", run, ""
}
