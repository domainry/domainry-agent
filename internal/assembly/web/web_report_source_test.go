package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	sdk "github.com/domainry/domainry-agent-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// A new host-selected service cannot authorize reports from the old connection,
// even while the original connection still exists and both tools are available.
func TestWebReportDoesNotAdoptReplacementConnection(t *testing.T) {
	f := newWebProductFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("web")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grantReader(b, b.readSession()["user_id"].(string))
	original := f.connect()
	conversation, _, _ := webRun(t, b, "查询公开网页并保存报告", true)
	reports := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
	if len(reports.Items) != 1 {
		t.Fatal("report missing")
	}
	f.close()
	f.options.WebConnectionKey = "replacement-web"
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	replacement := f.connect()
	if replacement.Key == original.Key {
		t.Fatal("replacement identity unchanged")
	}
	for _, d := range toolmodule.WebDefinitions() {
		if !settingList(t, b)[d.Key].Available {
			t.Fatal("replacement unavailable")
		}
	}
	before := f.vendorCalls.Load()
	for _, version := range []string{"1", "2"} {
		r := b.call("GET", "/agent/artifacts/"+reports.Items[0].ID+"?version="+version, "", 403)
		if !strings.Contains(r.Body.String(), "web.source_invalid") {
			t.Fatal("source authorization not checked", r.Body.String())
		}
	}
	messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
	for _, m := range messages.Items {
		if m.Role == "assistant" && (m.AccessError == "" || strings.Contains(m.Content, "WEB-BODY")) {
			t.Fatal("replacement adopted old answer")
		}
	}
	if f.vendorCalls.Load() != before {
		t.Fatal("historical authorization made vendor request")
	}
	t.Log("replacement service has both tools; old report v1/v2 and answer denied without upstream I/O")
}
