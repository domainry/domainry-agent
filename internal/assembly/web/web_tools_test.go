package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	sdk "github.com/domainry/domainry-agent-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func TestWebProductIdentityReportSourcesAndRestart(t *testing.T) {
	f := newWebProductFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("web")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	user := b.readSession()["user_id"].(string)
	f.grantReader(b, user)
	for _, d := range toolmodule.WebDefinitions() {
		if settingList(t, b)[d.Key].Available {
			t.Fatal("unconfigured web available")
		}
	}
	account := f.connect()
	for _, d := range toolmodule.WebDefinitions() {
		if !settingList(t, b)[d.Key].Available {
			t.Fatalf("web unavailable %s", d.Key)
		}
	}
	conversation, run, answer := webRun(t, b, "查询最新导出功能，保留来源，并保存、修改和导出资料报告。", true)
	for _, s := range []string{webFixtureBody, webFixtureURL, "读取时间", "页面完整性未知", "版本 2"} {
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
	if strings.Join(names, ",") != "web_search,web_fetch,artifact_create,artifact_read,artifact_edit,artifact_export" {
		t.Fatalf("wrong tool chain %v", names)
	}
	if f.vendorCalls.Load() != 2 {
		t.Fatalf("duplicate upstream calls %d", f.vendorCalls.Load())
	}
	reports := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
	if len(reports.Items) != 1 {
		t.Fatal("report missing")
	}
	report := reports.Items[0]
	if report.SourceConversationID != conversation || report.SourceRunID != run.ID || report.Version != 2 {
		t.Fatalf("report provenance %+v", report)
	}
	path := "/agent/artifacts/" + report.ID
	version := accountDecode[sdk.ConversationArtifactVersion](t, b.call("GET", path, "", 200))
	for _, s := range []string{webFixtureBody, webFixtureURL, webFixtureQuery, "unknown", "远端页面完整性未确认", "读取时间并非发布时间", "使用前再次核对"} {
		if !strings.Contains(version.Content.Markdown, s) {
			t.Fatalf("report lost %s", s)
		}
	}
	exported := accountDecode[sdk.ConversationArtifactExport](t, b.call("POST", path+"/exports", accountJSON(sdk.ConversationArtifactExportRequest{ClientID: "web-download", Version: 2, Format: "markdown"}), 200))
	download := "/agent/artifact-exports/" + exported.ID + "/download"
	if b.call("GET", download, "", 200).Body.String() != version.Content.Markdown {
		t.Fatal("download changed report")
	}
	sse := b.call("GET", "/agent/conversations/"+conversation+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(sse, "web_fetch") || !strings.Contains(sse, "artifact_create") || strings.Contains(sse, "private-web-service-token") {
		t.Fatal("SSE provenance/credential boundary")
	}
	assertHidden := func() {
		t.Helper()
		messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
		found := false
		for _, m := range messages.Items {
			if m.Role == "assistant" {
				found = true
				if m.AccessError == "" || strings.Contains(m.Content, "WEB-BODY") {
					t.Fatal("retained source exposed")
				}
			}
		}
		if !found {
			t.Fatal("missing retained answer")
		}
		b.call("GET", path, "", 503)
		b.call("GET", download, "", 503)
		b.call("POST", path+"/exports", accountJSON(sdk.ConversationArtifactExportRequest{ClientID: "denied-export", Version: 2, Format: "markdown"}), 503)
		b.call("PATCH", path, accountJSON(map[string]any{"client_id": "denied-edit", "expected_version": 2, "patch": map[string]string{"title": "forbidden"}}), 503)
		page := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
		if len(page.Items) != 0 || !page.Omitted {
			t.Fatal("source dependent report listed")
		}
	}
	setting := settingList(t, b)["web_fetch"]
	b.call("PUT", "/tools/preferences/web_fetch", accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	assertHidden()
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	if settingList(t, b)["web_fetch"].Enabled {
		t.Fatal("restart lost preference")
	}
	assertHidden()
	setting = settingList(t, b)["web_fetch"]
	b.call("PUT", "/tools/preferences/web_fetch", accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if current := accountDecode[sdk.ConversationArtifactVersion](t, b.call("GET", path, "", 200)); current.Content.Markdown != version.Content.Markdown {
		t.Fatal("restart changed report")
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
		f.grantReader(b, user)
		b.call("GET", path, "", 200)
	}
	other := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	other.login("system_administrator@example.com", accountInitial)
	other.changePassword(accountInitial, accountChanged)
	f.grantReader(b, other.readSession()["user_id"].(string))
	for _, d := range toolmodule.WebDefinitions() {
		if !settingList(t, other)[d.Key].Available {
			t.Fatal("shared web service missing for authorized workspace member")
		}
	}
	other.call("GET", "/agent/conversations/"+conversation+"/messages", "", 404)
	other.call("GET", path, "", 404)
	other.call("GET", download, "", 404)
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	assertHidden()
	before := f.vendorCalls.Load()
	_, _, unavailable := webRun(t, b, "读取公开网页", false)
	if f.vendorCalls.Load() != before || !strings.Contains(unavailable, "没有可用") {
		t.Fatal("revoked service dispatched")
	}
	if f.exchanges != 0 {
		t.Fatal("web unexpectedly used OAuth")
	}
	t.Logf("real Identity + Integration + Tools + Agent + Knowledge + SQLite; upstream HTTP=%d; model HTTP=%d; OAuth=%d", f.vendorCalls.Load(), f.modelCalls.Load(), f.exchanges)
}

func TestWebProductEmptyPartialAndFailure(t *testing.T) {
	f := newWebProductFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("web")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grantReader(b, b.readSession()["user_id"].(string))
	f.connect()
	for _, tc := range []struct {
		mode  int32
		want  string
		calls int32
	}{{1, "没有返回匹配来源", 1}, {2, "未取得可读正文", 2}, {3, "正文已裁剪", 2}, {4, "网页读取失败", 1}} {
		t.Run(fmt.Sprint(tc.mode), func(t *testing.T) {
			f.mode.Store(tc.mode)
			before := f.vendorCalls.Load()
			_, run, answer := webRun(t, b, "查询网页并保存报告", true)
			if !strings.Contains(answer, tc.want) || strings.Contains(answer, "已保存") {
				t.Fatal(answer)
			}
			if f.vendorCalls.Load()-before != tc.calls {
				t.Fatal("unexpected replay")
			}
			for _, step := range run.Steps {
				for _, call := range step.Calls {
					if strings.HasPrefix(call.Name, "artifact_") {
						t.Fatal("insufficient evidence created report")
					}
				}
			}
			if page := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200)); len(page.Items) != 0 {
				t.Fatal("unexpected report")
			}
		})
	}
}
