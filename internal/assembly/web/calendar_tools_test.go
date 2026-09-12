package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	calendar "github.com/domainry/domainry-connector-sdk/calendar"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

type calendarProductFixture struct {
	*accountFixture
	partial     atomic.Bool
	vendorCalls atomic.Int32
	modelCalls  atomic.Int32
}

func newCalendarProductFixture(t *testing.T) *calendarProductFixture {
	t.Helper()
	f := &calendarProductFixture{accountFixture: newAccountFixture(t)}
	f.close()
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.vendorCalls.Add(1)
		f.mu.Lock()
		grant := f.tokenGrants[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !strings.Contains(grant, accountScope) {
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		allDay := map[string]any{"kind": "calendar#event", "id": "all-day", "summary": "全天工作坊", "status": "confirmed", "transparency": "transparent", "start": map[string]string{"date": "2026-11-01"}, "end": map[string]string{"date": "2026-11-02"}, "recurringEventId": "workshop-series", "originalStartTime": map[string]string{"date": "2026-10-25"}}
		switch r.URL.Path {
		case "/calendar/v3/users/me/calendarList":
			json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#calendarList", "items": []any{map[string]any{"id": "primary", "summary": "工作日历", "timeZone": "America/New_York", "primary": true}, map[string]any{"id": "secondary", "summary": "共享日历", "timeZone": "America/New_York"}}})
		case "/calendar/v3/calendars/primary/events":
			q := r.URL.Query()
			if q.Get("timeMin") != "2026-11-01T00:00:00-04:00" || q.Get("timeMax") != "2026-11-02T00:00:00-05:00" || q.Get("timeZone") != "America/New_York" || q.Get("singleEvents") != "true" {
				t.Error("provider window/timezone was changed")
				http.Error(w, "bad window", 400)
				return
			}
			if q.Get("pageToken") == "calendar-page-2" {
				json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#events", "timeZone": "America/New_York", "items": []any{map[string]any{"id": "dst", "summary": "跨回拨会议", "start": map[string]string{"dateTime": "2026-11-01T01:30:00-04:00"}, "end": map[string]string{"dateTime": "2026-11-01T01:30:00-05:00"}}}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"kind": "calendar#events", "timeZone": "America/New_York", "nextPageToken": "calendar-page-2", "items": []any{allDay}})
			}
		case "/calendar/v3/calendars/primary/events/all-day":
			allDay["description"] = "CALENDAR-BODY：讨论实际议程与预算。"
			json.NewEncoder(w).Encode(allDay)
		case "/calendar/v3/freeBusy":
			var body struct {
				TimeMin, TimeMax, TimeZone string
				Items                      []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Items) != 2 || body.Items[0].ID != "primary" || body.Items[1].ID != "secondary" {
				http.Error(w, "bad calendars", 400)
				return
			}
			calendars := map[string]any{"primary": map[string]any{"busy": []calendar.Window{{Start: "2026-11-01T01:30:00-04:00", End: "2026-11-01T01:30:00-05:00"}}}, "secondary": map[string]any{"busy": []calendar.Window{}}}
			if f.partial.Load() {
				calendars["secondary"] = map[string]any{"errors": []any{map[string]string{"reason": "notFound"}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"timeMin": body.TimeMin, "timeMax": body.TimeMax, "calendars": calendars})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(vendor.Close)
	f.providerTransport = accountProviderHTTPTransport{vendor, "www.googleapis.com", "/calendar/v3/"}
	model := httptest.NewServer(http.HandlerFunc(f.model))
	t.Cleanup(model.Close)
	f.options.CalendarTools = true
	f.options.Agent = agentmodule.Options{ConversationURL: model.URL, ConversationModel: "calendar-protocol-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, MaxSteps: 12, MaxToolCalls: 12}}
	f.open()
	return f
}

type calendarModelMessage struct {
	Role, Content string
	CallID        string `json:"tool_call_id"`
}
type calendarModelResult struct {
	Status    string `json:"status"`
	ErrorCode string `json:"error_code"`
	Content   struct {
		Data json.RawMessage `json:"data"`
	} `json:"content"`
}

func (f *calendarProductFixture) model(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []calendarModelMessage `json:"messages"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
		http.Error(w, "bad model request", 400)
		return
	}
	f.modelCalls.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(delta any, finish string) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	answer := func(value string) { write(map[string]any{"content": value}, "stop") }
	keys := []string{}
	available := map[string]bool{}
	for _, v := range in.Tools {
		keys = append(keys, v.Function.Name)
		available[v.Function.Name] = true
	}
	call := func(id, key string, args any) {
		if !available[key] {
			answer("当前没有可用的日历读取权限或连接。")
			return
		}
		b, _ := json.Marshal(args)
		write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": key, "arguments": string(b)}}}}, "tool_calls")
	}
	defer fmt.Fprint(w, "data: [DONE]\n\n")
	last := in.Messages[len(in.Messages)-1]
	if last.Role == "user" {
		if strings.Contains(last.Content, "目录") {
			answer("当前工具：" + strings.Join(keys, "、"))
			return
		}
		call("calendar-accounts", "calendar_accounts", map[string]any{"operation": calendar.EventsOperationKey})
		return
	}
	data := map[string]json.RawMessage{}
	for _, m := range in.Messages {
		if m.Role == "tool" && strings.HasPrefix(m.CallID, "calendar-") {
			var result calendarModelResult
			if json.Unmarshal([]byte(m.Content), &result) != nil || result.Status != "completed" {
				answer("日历读取未完成。")
				return
			}
			data[m.CallID] = result.Content.Data
		}
	}
	var accounts struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
	}
	_ = json.Unmarshal(data["calendar-accounts"], &accounts)
	if len(accounts.Items) == 0 {
		answer("没有当前可读取的日历账号。")
		return
	}
	account := accounts.Items[0].Key
	var list calendar.CalendarsPage
	_ = json.Unmarshal(data["calendar-list"], &list)
	if last.CallID == "calendar-accounts" {
		call("calendar-list", calendar.ListOperationKey, map[string]any{"account_key": account})
		return
	}
	if len(list.Items) < 2 {
		answer("没有完整的两个日历来源。")
		return
	}
	calendarID := list.Items[0].ID
	window := calendar.Window{Start: "2026-11-01T00:00:00-04:00", End: "2026-11-02T00:00:00-05:00"}
	args := map[string]any{"account_key": account, "calendar_id": calendarID, "time_zone": "America/New_York", "window": window}
	if last.CallID == "calendar-list" {
		call("calendar-events", calendar.EventsOperationKey, args)
		return
	}
	var first, second calendar.EventsPage
	_ = json.Unmarshal(data["calendar-events"], &first)
	_ = json.Unmarshal(data["calendar-events-next"], &second)
	if last.CallID == "calendar-events" && first.NextCursor != "" {
		args["cursor"] = first.NextCursor
		call("calendar-events-next", calendar.EventsOperationKey, args)
		return
	}
	if last.CallID == "calendar-events" || last.CallID == "calendar-events-next" {
		if len(first.Items) == 0 {
			answer("窗口内没有事件。")
			return
		}
		call("calendar-event", calendar.EventOperationKey, map[string]any{"account_key": account, "calendar_id": calendarID, "event_id": first.Items[0].ID, "time_zone": "America/New_York"})
		return
	}
	if last.CallID == "calendar-event" {
		call("calendar-availability", calendar.AvailabilityOperationKey, map[string]any{"account_key": account, "calendar_ids": []string{calendarID, list.Items[1].ID}, "window": window, "time_zone": "America/New_York"})
		return
	}
	var detail calendar.Event
	_ = json.Unmarshal(data["calendar-event"], &detail)
	var free struct {
		Complete bool              `json:"complete"`
		Free     []calendar.Window `json:"free"`
	}
	_ = json.Unmarshal(data["calendar-availability"], &free)
	availability := "忙闲来源不完整，无法确认共同空闲。"
	if free.Complete {
		b, _ := json.Marshal(free.Free)
		availability = "共同空闲（绝对时刻）：" + string(b)
	}
	answer(fmt.Sprintf("已读取 %d 项安排。全天工作坊：%s，排他结束日期 %s。\n\n事件详情：%s\n\n%s", len(first.Items)+len(second.Items), detail.Start.Date, detail.End.Date, detail.Description, availability))
}

func (f *calendarProductFixture) connect(b *browser) integration.ConnectionAccount {
	f.t.Helper()
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("PUT", "/integration/oauth-applications/calendar-google", accountJSON(integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Google Calendar", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{accountScope}, Enabled: true}), 200)
	s := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: "calendar-google", Scope: integration.ConnectionAccountScopePersonal, Name: "个人日历", Scopes: []string{accountScope}}), 200))
	u, _ := url.Parse(f.callback(s.AuthorizationURL, "connect"))
	done := accountDecode[integration.OAuthAuthorizationSession](f.t, b.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: u.Query().Get("state"), Code: u.Query().Get("code")}), 200))
	if done.Account == nil {
		f.t.Fatal("account missing")
	}
	return *done.Account
}

func TestCalendarProductIdentityConversationAndRestart(t *testing.T) {
	f := newCalendarProductFixture(t)
	files := fstest.MapFS{"index.html": {Data: []byte("calendar")}, "oauth-callback.html": {Data: []byte("callback")}}
	b := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", accountInitial)
	b.changePassword(accountInitial, accountChanged)
	b.call("POST", "/app/product/account-setup", `{}`, 200)
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	settings := settingList(t, b)
	for _, d := range toolmodule.CalendarDefinitions() {
		if v, ok := settings[d.Key]; !ok || v.Available || v.State != "connection_unavailable" {
			t.Fatalf("missing account availability %s: %+v", d.Key, v)
		}
	}
	account := f.connect(b)
	settings = settingList(t, b)
	for _, d := range toolmodule.CalendarDefinitions() {
		if !settings[d.Key].Available {
			t.Fatalf("granted account unavailable %s", d.Key)
		}
	}
	conversation, runID, answer := calendarRunReference(t, b, "读取完整日历安排、事件详情与共同空闲")
	for _, required := range []string{"已读取 2 项安排", "2026-11-01", "2026-11-02", "CALENDAR-BODY", "2026-11-02T05:00:00Z"} {
		if !strings.Contains(answer, required) {
			t.Fatalf("missing %s in %s", required, answer)
		}
	}
	run := accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+conversation+"/runs/"+runID, "", 200))
	count := 0
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.Status != "completed" {
				t.Fatalf("call failed %+v", call)
			}
			count++
		}
	}
	if count != 6 || f.vendorCalls.Load() != 5 {
		t.Fatalf("read chain count: %d calls, %d vendor requests", count, f.vendorCalls.Load())
	}
	sse := b.call("GET", "/agent/conversations/"+conversation+"/runs/"+runID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(sse, "tool.completed") || !strings.Contains(sse, "calendar_event") || strings.Contains(sse, "fixture-access") || strings.Contains(sse, "fixture-refresh") {
		t.Fatal("SSE evidence/secret boundary")
	}
	// The browser mounts account management, not the generic operation executor.
	adapters, err := f.host.IntegrationAdapters()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range adapters {
		for _, route := range a.Routes() {
			if route.Action.Key == integration.ActionIntegrationConnectionAccountsRead {
				t.Fatal("generic reads mounted")
			}
		}
	}
	f.partial.Store(true)
	partial := calendarRun(t, b, "读取不完整日历")
	if !strings.Contains(partial, "无法确认共同空闲") || strings.Contains(partial, "共同空闲（绝对时刻）") {
		t.Fatal(partial)
	}
	f.partial.Store(false)
	setting := settingList(t, b)[calendar.EventOperationKey]
	b.call("PUT", "/tools/preferences/"+calendar.EventOperationKey, accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	assertCalendarHidden(t, b, conversation)
	f.close()
	f.open()
	b.handler = f.boundary("http://127.0.0.1:8091", files)
	b.login("admin@example.com", accountChanged)
	if settingList(t, b)[calendar.EventOperationKey].Enabled {
		t.Fatal("restart lost disabled preference")
	}
	assertCalendarHidden(t, b, conversation)
	b.call("PUT", "/tools/preferences/"+calendar.EventOperationKey, accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: 1, ToolVersion: setting.Version}), 200)
	if got := calendarAnswer(t, b, conversation); got != answer {
		t.Fatalf("restored result changed %s", got)
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
		assertCalendarHidden(t, b, conversation)
		b.call("POST", "/app/product/account-setup", `{}`, 200)
		if calendarAnswer(t, b, conversation) != answer {
			t.Fatal("permission restoration did not restore source")
		}
	}
	other := &browser{t: t, handler: b.handler, cookies: map[string]*http.Cookie{}}
	other.login("system_administrator@example.com", accountInitial)
	other.changePassword(accountInitial, accountChanged)
	f.grantCalendarReader(b, other.readSession()["user_id"].(string))
	if got := settingList(t, other); len(got) != 5 || got[calendar.EventsOperationKey].Available {
		t.Fatal("another user can read personal calendar")
	}
	other.call("GET", "/agent/conversations/"+conversation+"/messages", "", 404)
	current := accountDecode[integration.ConnectionAccount](t, b.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	b.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": current.UpdatedAt}), 200)
	assertCalendarHidden(t, b, conversation)
	before := f.vendorCalls.Load()
	if got := calendarRun(t, b, "读取日历"); strings.Contains(got, "CALENDAR-BODY") {
		t.Fatal("revoked source reached model")
	}
	if f.vendorCalls.Load() != before {
		t.Fatal("revoked account reached vendor")
	}
}

func (f *calendarProductFixture) grantCalendarReader(admin *browser, user string) {
	f.t.Helper()
	mutateTestRolePermissions(f.t, f.host, admin, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := append([]identity.ProjectRolePermission(nil), prior...)
		known := map[string]bool{}
		for _, p := range out {
			known[p.PermissionKey] = true
		}
		keys := []string{integration.ActionIntegrationConnectionAccountsList, integration.ActionIntegrationConnectionAccountsRead}
		for _, d := range toolmodule.CalendarDefinitions() {
			keys = append(keys, d.ActionKey)
		}
		for _, r := range tools.ToolSettingsRoutes() {
			keys = append(keys, r.Action.Permission.Key)
		}
		for _, key := range keys {
			if !known[key] {
				out = append(out, identity.ProjectRolePermission{PermissionKey: key, DataScope: identity.DataScopeOwner})
			}
		}
		return out
	}, user)
}

func calendarAnswer(t *testing.T, b *browser, conversation string) string {
	t.Helper()
	page := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
	for _, m := range page.Items {
		if m.Role == "assistant" {
			return m.Content
		}
	}
	return ""
}
func assertCalendarHidden(t *testing.T, b *browser, conversation string) {
	t.Helper()
	page := accountDecode[sdk.ConversationMessagePage](t, b.call("GET", "/agent/conversations/"+conversation+"/messages", "", 200))
	found := false
	for _, m := range page.Items {
		if m.Role == "assistant" {
			found = true
			if m.AccessError == "" || strings.Contains(m.Content, "CALENDAR-BODY") {
				t.Fatalf("retained calendar result exposed %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("missing retained message")
	}
}

func calendarRun(t *testing.T, b *browser, message string) string {
	t.Helper()
	_, _, answer := calendarRunReference(t, b, message)
	return answer
}
func calendarRunReference(t *testing.T, b *browser, message string) (string, string, string) {
	t.Helper()
	id := fmt.Sprintf("calendar-%d", time.Now().UnixNano())
	c := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: id}), 200))
	run := accountDecode[sdk.ConversationRun](t, b.call("POST", "/agent/conversations/"+c.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: id, Message: message}), 202))
	deadline := time.Now().Add(2 * time.Minute)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		run = accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID, "", 200))
	}
	if run.Status != "completed" {
		t.Fatalf("calendar run status=%s error=%s steps=%d", run.Status, run.ErrorCode, len(run.Steps))
	}
	return c.ID, run.ID, calendarAnswer(t, b, c.ID)
}
