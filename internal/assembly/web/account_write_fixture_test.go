package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentmodule "github.com/domainry/domainry-agent/module"
	connector "github.com/domainry/domainry-connector-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
)

const accountCalendarWriteScope = "https://www.googleapis.com/auth/calendar"
const accountMailWriteScope = "https://www.googleapis.com/auth/gmail.modify"

type accountWriteProductFixture struct {
	*accountFixture
	vendorMu                sync.Mutex
	event                   map[string]any
	effects                 map[string]int
	mailReceipts            []mail.Header
	mailText                []string
	requests, modelRequests atomic.Int32
}

type accountWriteHTTPTransport struct{ target *httptest.Server }

func (tr accountWriteHTTPTransport) ExecuteSQL(context.Context, connector.SQLRequest) (connector.SQLResult, error) {
	return connector.SQLResult{}, fmt.Errorf("unexpected SQL")
}
func (tr accountWriteHTTPTransport) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	u, err := url.Parse(in.URL)
	if err != nil {
		return connector.HTTPResponse{}, err
	}
	prefix := ""
	switch u.Host {
	case "www.googleapis.com":
		prefix = "/calendar/v3/"
	case "gmail.googleapis.com":
		prefix = "/gmail/v1/"
	default:
		return connector.HTTPResponse{}, fmt.Errorf("unexpected vendor")
	}
	return (accountProviderHTTPTransport{tr.target, u.Host, prefix}).RoundTripHTTP(ctx, in)
}

func newAccountWriteProductFixture(t *testing.T) *accountWriteProductFixture {
	f := &accountWriteProductFixture{accountFixture: newAccountFixture(t), effects: map[string]int{}, event: map[string]any{
		"kind": "calendar#event", "id": "review-event", "etag": `"version-1"`, "summary": "原评审日程", "status": "confirmed", "description": "原说明", "location": "原地点",
		"start": map[string]string{"date": "2026-11-01"}, "end": map[string]string{"date": "2026-11-02"}, "attendees": []any{map[string]string{"email": "old@example.test", "displayName": "原参与者"}},
	}}
	f.close()
	vendor := httptest.NewServer(http.HandlerFunc(f.vendor))
	t.Cleanup(vendor.Close)
	f.providerTransport = accountWriteHTTPTransport{vendor}
	model := httptest.NewServer(http.HandlerFunc(f.model))
	t.Cleanup(model.Close)
	f.options.CalendarTools = true
	f.options.MailTools = true
	f.options.CalendarWriteTools = true
	f.options.MailWriteTools = true
	// This exercises a full owner chain, not lease expiry. Keep the production
	// lease and avoid aggressive idle-worker polling against the shared SQLite
	// connection while the race detector checks the confirmation/restart flow.
	f.options.Agent = agentmodule.Options{ConversationURL: model.URL, ConversationModel: "account-write-protocol-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 100 * time.Millisecond, MaxSteps: 20, MaxToolCalls: 32}}
	f.open()
	return f
}
func (f *accountWriteProductFixture) counts() map[string]int {
	f.vendorMu.Lock()
	defer f.vendorMu.Unlock()
	out := map[string]int{}
	for k, v := range f.effects {
		out[k] = v
	}
	return out
}
func (f *accountWriteProductFixture) connect(b *browser) integration.ConnectionAccount {
	f.t.Helper()
	scopes := []string{accountCalendarWriteScope, accountMailWriteScope}
	b.call("PUT", "/integration/oauth-applications/write-google", accountJSON(integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google 写入验收", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: scopes, Enabled: true}), 200)
	s := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "write-google", Scope: integration.ConnectionAccountScopePersonal, Name: "日程与邮件验收账号", Scopes: scopes}), 200))
	u, _ := url.Parse(f.callback(s.AuthorizationURL, "connect"))
	done := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: u.Query().Get("state"), Code: u.Query().Get("code")}), 200))
	if done.Account == nil {
		f.t.Fatal("write account missing")
	}
	return *done.Account
}

// Only the vendor and model are protocol fixtures. Product/SDK/Registry,
// Identity, OAuth, Providers, current authorization and both databases are real.
func (f *accountWriteProductFixture) vendor(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	f.mu.Lock()
	grant := f.tokenGrants[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	f.mu.Unlock()
	want := accountCalendarWriteScope
	if strings.HasPrefix(r.URL.Path, "/gmail/") {
		want = accountMailWriteScope
	}
	if !strings.Contains(grant, want) {
		http.Error(w, "missing vendor grant", 403)
		return
	}
	f.vendorMu.Lock()
	defer f.vendorMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(value any) {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.t.Error(err)
		}
	}
	switch r.URL.Path {
	case "/calendar/v3/users/me/calendarList":
		if r.Method != "GET" {
			http.Error(w, "read only", 405)
			return
		}
		write(map[string]any{"kind": "calendar#calendarList", "items": []any{map[string]any{"id": "primary", "summary": "评审日历", "timeZone": "America/New_York", "primary": true}}})
	case "/calendar/v3/calendars/primary/events":
		if r.Method == "GET" {
			write(map[string]any{"kind": "calendar#events", "timeZone": "America/New_York", "items": []any{f.event}})
			return
		}
		var body map[string]any
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || r.URL.Query().Get("sendUpdates") != "all" {
			http.Error(w, "bad creation", 400)
			return
		}
		if body["summary"] != "完整日程标题" {
			f.t.Error("creation title changed")
		}
		f.effects["calendar_create"]++
		body["kind"] = "calendar#event"
		body["etag"] = `"created-version"`
		write(body)
	case "/calendar/v3/calendars/primary/events/review-event":
		if r.Method == "GET" {
			write(f.event)
			return
		}
		var body map[string]any
		if r.Method != "PATCH" || json.NewDecoder(r.Body).Decode(&body) != nil || r.Header.Get("If-Match") != f.event["etag"] || r.URL.Query().Get("sendUpdates") != "all" {
			http.Error(w, "precondition", 412)
			return
		}
		attendees, ok := body["attendees"].([]any)
		if len(body) != 3 || body["description"] != "" || body["location"] != "" || !ok || len(attendees) != 0 {
			f.t.Error("explicit clears/omitted fields changed")
		}
		f.effects["calendar_update"]++
		for k, v := range body {
			f.event[k] = v
		}
		f.event["etag"] = fmt.Sprintf(`"version-%d"`, f.effects["calendar_update"]+1)
		write(f.event)
	case "/gmail/v1/users/me/profile":
		if r.Method != "GET" {
			http.Error(w, "read only", 405)
			return
		}
		write(map[string]string{"emailAddress": "sender@example.test"})
	case "/gmail/v1/users/me/messages":
		if r.Method != "GET" {
			http.Error(w, "read only", 405)
			return
		}
		write(map[string]any{"messages": []any{map[string]string{"id": "original-mail", "threadId": "original-thread"}}, "resultSizeEstimate": 1})
	case "/gmail/v1/users/me/messages/original-mail":
		if r.Method != "GET" {
			http.Error(w, "read only", 405)
			return
		}
		headers := []any{}
		for _, v := range [][2]string{{"Subject", "评审回复"}, {"From", "from@example.test"}, {"Reply-To", "reply@example.test"}, {"To", "sender@example.test"}, {"Message-ID", "<original@example.test>"}, {"Date", "Fri, 11 Sep 2026 10:00:00 +0800"}} {
			headers = append(headers, map[string]string{"name": v[0], "value": v[1]})
		}
		payload := map[string]any{"mimeType": "text/plain", "headers": headers}
		if r.URL.Query().Get("format") == "full" {
			body := "请回复评审安排。"
			payload["body"] = map[string]any{"size": len(body), "data": base64.RawURLEncoding.EncodeToString([]byte(body))}
		}
		write(map[string]any{"id": "original-mail", "threadId": "original-thread", "labelIds": []string{"INBOX"}, "internalDate": "1789092000000", "payload": payload})
	case "/gmail/v1/users/me/messages/send":
		var input struct {
			Raw    string `json:"raw"`
			Thread string `json:"threadId"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "bad send", 400)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(input.Raw)
		if err != nil {
			http.Error(w, "bad MIME", 400)
			return
		}
		message, err := mail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			http.Error(w, "bad MIME", 400)
			return
		}
		content, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
		if err != nil {
			http.Error(w, "bad content", 400)
			return
		}
		if message.Header.Get("From") != "<sender@example.test>" {
			f.t.Error("sender did not come from current profile")
		}
		if message.Header.Get("Cc") == "" || message.Header.Get("Bcc") == "" {
			f.t.Error("explicit recipient groups lost")
		}
		subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
		if err != nil {
			f.t.Error(err)
		}
		operation := "mail_send"
		if input.Thread != "" {
			operation = "mail_reply"
			if input.Thread != "original-thread" || message.Header.Get("In-Reply-To") != "<original@example.test>" || subject != "Re: 评审回复" {
				f.t.Error("reply target/subject changed")
			}
		}
		to := "to@example.test"
		if operation == "mail_reply" {
			to = "reply@example.test"
		}
		for field, want := range map[string]string{"To": to, "Cc": "cc@example.test", "Bcc": "bcc@example.test"} {
			addresses, err := message.Header.AddressList(field)
			if err != nil || len(addresses) != 1 || addresses[0].Address != want {
				f.t.Errorf("approved %s recipient changed", field)
			}
		}
		wantText := accountWriteMailText
		if operation == "mail_reply" {
			wantText = "完整回复正文"
		} else if strings.HasSuffix(string(content), "\n受理后断线") {
			wantText += "\n受理后断线"
		}
		if string(content) != wantText {
			f.t.Error("full approved mail body changed")
		}
		f.effects[operation]++
		f.mailReceipts = append(f.mailReceipts, message.Header)
		f.mailText = append(f.mailText, string(content))
		if strings.Contains(string(content), "受理后断线") {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		thread := input.Thread
		if thread == "" {
			thread = "new-thread"
		}
		write(map[string]string{"id": fmt.Sprintf("sent-%d", len(f.mailReceipts)), "threadId": thread})
	default:
		http.NotFound(w, r)
	}
}
