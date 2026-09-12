package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	sdk "github.com/domainry/domainry-agent-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// Availability of another mailbox must never authorize an old source account.
// This exercises the persisted result authorizer, beyond generic availability.
func TestMailDraftDoesNotAdoptReplacementAccount(t *testing.T) {
	f := newMailProductFixture(t)
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", fstest.MapFS{"index.html": {Data: []byte("mail")}, "oauth-callback.html": {Data: []byte("callback")}}), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grantReader(b, b.readSession()["user_id"].(string))
	original := f.connect(b, false)
	conversation, _, _ := mailRun(t, b, "搜索预算邮件并保存回复草稿", true)
	drafts := accountDecode[sdk.ConversationArtifactPage](t, b.call("GET", "/agent/artifacts", "", 200))
	if len(drafts.Items) != 1 {
		t.Fatal("source draft missing")
	}
	draft := drafts.Items[0]
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+original.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+original.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	session := accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "mail-google", Scope: integration.ConnectionAccountScopePersonal, Name: "替代邮箱", Scopes: []string{mailReadScope}}), 200))
	callback, _ := url.Parse(f.callback(session.AuthorizationURL, "connect"))
	done := accountDecode[integration.OAuthAuthorizationSession](t, b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: callback.Query().Get("state"), Code: callback.Query().Get("code")}), 200))
	if done.Account == nil || done.Account.Key == original.Key {
		t.Fatal("replacement must be a distinct account")
	}
	settings := settingList(t, b)
	for _, d := range toolmodule.MailDefinitions() {
		if !settings[d.Key].Available {
			t.Fatal("replacement does not support all tools")
		}
	}
	before := f.vendorCalls.Load()
	for _, version := range []string{"1", "2"} {
		result := b.call("GET", "/agent/artifacts/"+draft.ID+"?version="+version, "", 403)
		if !strings.Contains(result.Body.String(), "mail.source_invalid") {
			t.Fatalf("stored source authorizer was not checked: %s", result.Body.String())
		}
	}
	messages := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
	for _, m := range messages.Items {
		if m.Role == "assistant" && (m.AccessError == "" || strings.Contains(m.Content, "MAIL-BODY")) {
			t.Fatal("replacement adopted old answer")
		}
	}
	if f.vendorCalls.Load() != before {
		t.Fatal("historical source audit performed vendor IO")
	}
	t.Log("all four mail tools available on replacement account; both stored draft versions and original answer remain denied by mail source authorizer; zero vendor IO")
}
