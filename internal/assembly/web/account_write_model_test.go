package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/domainry/domainry-connector-sdk/calendar"
	"github.com/domainry/domainry-connector-sdk/calendarwrite"
	"github.com/domainry/domainry-connector-sdk/mail"
	"github.com/domainry/domainry-connector-sdk/mailwrite"
)

var accountWriteMailText = "完整发送正文\n第二行：<script>这只是正文</script>\n" + strings.Repeat("正文核对。", 4000) + "\n正文末尾确认内容"

func (f *accountWriteProductFixture) model(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tools    []struct{ Function struct{ Name string } }
		Messages []calendarModelMessage
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Messages) == 0 {
		http.Error(w, "bad model input", 400)
		return
	}
	number := f.modelRequests.Add(1)
	started := time.Now()
	f.t.Logf("model request %d: %d messages", number, len(in.Messages))
	defer func() { f.t.Logf("model request %d returned after %s", number, time.Since(started)) }()
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(delta any, finish string) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	defer fmt.Fprint(w, "data: [DONE]\n\n")
	answer := func(s string) { emit(map[string]any{"content": s}, "stop") }
	available := map[string]bool{}
	for _, d := range in.Tools {
		available[d.Function.Name] = true
	}
	type call struct {
		id, key string
		args    any
	}
	calls := func(items ...call) {
		out := []any{}
		for i, v := range items {
			if !available[v.key] {
				answer("当前工具或账号权限不可用：" + v.key)
				return
			}
			b, _ := json.Marshal(v.args)
			out = append(out, map[string]any{"index": i, "id": v.id, "type": "function", "function": map[string]any{"name": v.key, "arguments": string(b)}})
		}
		emit(map[string]any{"tool_calls": out}, "tool_calls")
	}
	data := map[string]json.RawMessage{}
	unknown := false
	for _, m := range in.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "未知发送") {
			unknown = true
		}
		if m.Role == "tool" && strings.HasPrefix(m.CallID, "aw-") {
			var value calendarModelResult
			if json.Unmarshal([]byte(m.Content), &value) != nil || value.Status != "completed" {
				answer("调用未完成，不宣称产生外部结果。")
				return
			}
			data[m.CallID] = value.Content.Data
		}
	}
	if data["aw-create-account"] == nil {
		calls(call{"aw-create-account", "calendar_write_accounts", map[string]string{"operation": calendarwrite.CreateOperationKey}}, call{"aw-update-account", "calendar_write_accounts", map[string]string{"operation": calendarwrite.UpdateOperationKey}}, call{"aw-send-account", "mail_write_accounts", map[string]string{"operation": mailwrite.SendOperationKey}}, call{"aw-reply-account", "mail_write_accounts", map[string]string{"operation": mailwrite.ReplyOperationKey}})
		return
	}
	type account struct {
		Key      string `json:"key"`
		Revision string `json:"account_updated_at"`
	}
	accounts := map[string]account{}
	for _, id := range []string{"aw-create-account", "aw-update-account", "aw-send-account", "aw-reply-account"} {
		var page struct {
			Items []account `json:"items"`
		}
		_ = json.Unmarshal(data[id], &page)
		if len(page.Items) == 0 || page.Items[0].Key == "" || page.Items[0].Revision == "" {
			answer("没有当前获准的写入账号。")
			return
		}
		accounts[id] = page.Items[0]
	}
	if data["aw-calendar-list"] == nil {
		calls(call{"aw-calendar-list", calendar.ListOperationKey, map[string]string{"account_key": accounts["aw-create-account"].Key}}, call{"aw-mail-list", mail.ListOperationKey, map[string]string{"account_key": accounts["aw-reply-account"].Key}})
		return
	}
	var calendars calendar.CalendarsPage
	var messages mail.MessagesPage
	_ = json.Unmarshal(data["aw-calendar-list"], &calendars)
	_ = json.Unmarshal(data["aw-mail-list"], &messages)
	if len(calendars.Items) != 1 || len(messages.Items) != 1 {
		answer("目标目录不完整。")
		return
	}
	calendarID, messageID := calendars.Items[0].ID, messages.Items[0].ID
	if data["aw-events"] == nil {
		calls(call{"aw-events", calendar.EventsOperationKey, map[string]any{"account_key": accounts["aw-update-account"].Key, "calendar_id": calendarID, "time_zone": "America/New_York", "window": calendar.Window{Start: "2026-11-01T00:00:00-04:00", End: "2026-11-02T00:00:00-05:00"}}}, call{"aw-original", mail.ReadOperationKey, map[string]string{"account_key": accounts["aw-reply-account"].Key, "message_id": messageID}})
		return
	}
	var events calendar.EventsPage
	var original mail.Message
	_ = json.Unmarshal(data["aw-events"], &events)
	_ = json.Unmarshal(data["aw-original"], &original)
	if len(events.Items) != 1 || !original.Body.Complete || !original.Summary.MetadataComplete {
		answer("原目标或邮件内容不完整。")
		return
	}
	if data["aw-inspect"] == nil {
		calls(call{"aw-inspect", calendarwrite.InspectOperationKey, map[string]string{"account_key": accounts["aw-update-account"].Key, "calendar_id": calendarID, "event_id": events.Items[0].ID, "time_zone": "America/New_York"}})
		return
	}
	var snapshot calendarwrite.Snapshot
	_ = json.Unmarshal(data["aw-inspect"], &snapshot)
	if snapshot.Version == "" || snapshot.Event.ID != events.Items[0].ID {
		answer("日程版本尚未确认。")
		return
	}
	if data["aw-send"] == nil {
		wrap := func(id string, payload any) any {
			a := accounts[id]
			return map[string]any{"account_key": a.Key, "account_updated_at": a.Revision, "request": payload}
		}
		message := mailwrite.Message{To: []mail.Address{{Address: "to@example.test", Name: "收件人"}}, CC: []mail.Address{{Address: "cc@example.test", Name: "抄送人"}}, BCC: []mail.Address{{Address: "bcc@example.test", Name: "密送人"}}, Subject: "完整发送主题", Text: accountWriteMailText}
		if unknown {
			message.Text += "\n受理后断线"
			calls(call{"aw-send", mailwrite.SendOperationKey, wrap("aw-send-account", mailwrite.SendRequest{Message: message})})
			return
		}
		reply := message
		reply.Subject = "Re: " + original.Summary.Subject
		reply.To = original.Summary.ReplyTo
		if len(reply.To) == 0 {
			reply.To = original.Summary.From
		}
		reply.Text = "完整回复正文"
		empty := ""
		noAttendees := []calendarwrite.Attendee{}
		calls(
			call{"aw-create", calendarwrite.CreateOperationKey, wrap("aw-create-account", calendarwrite.CreateRequest{CalendarID: calendarID, Notifications: calendarwrite.NotifyAttendees, Event: calendarwrite.Draft{Title: "完整日程标题", Description: "完整日程说明", Start: calendar.Moment{Date: "2026-11-01", TimeZone: "America/New_York"}, End: calendar.Moment{Date: "2026-11-02", TimeZone: "America/New_York"}, Attendees: []calendarwrite.Attendee{{Address: "invite@example.test", Name: "受邀者", Kind: "required"}}}})},
			call{"aw-update", calendarwrite.UpdateOperationKey, wrap("aw-update-account", calendarwrite.UpdateRequest{CalendarID: calendarID, EventID: snapshot.Event.ID, ExpectedVersion: snapshot.Version, Scope: calendarwrite.ScopeEvent, Notifications: calendarwrite.NotifyAttendees, Changes: calendarwrite.Patch{Description: &empty, Location: &empty, Attendees: &noAttendees}})},
			call{"aw-send", mailwrite.SendOperationKey, wrap("aw-send-account", mailwrite.SendRequest{Message: message})},
			call{"aw-reply", mailwrite.ReplyOperationKey, wrap("aw-reply-account", mailwrite.ReplyRequest{MessageID: original.Summary.ID, Message: reply})},
		)
		return
	}
	for _, id := range []string{"aw-send", "aw-reply"} {
		var receipt mailwrite.Result
		if json.Unmarshal(data[id], &receipt) != nil || receipt.Status != "accepted" || receipt.Delivery != "unknown" {
			answer("邮件尚无可靠受理回执。")
			return
		}
	}
	for _, id := range []string{"aw-create", "aw-update"} {
		var receipt calendarwrite.Result
		if json.Unmarshal(data[id], &receipt) != nil || receipt.EventID == "" || receipt.Notifications != "requested" {
			answer("日程尚无可靠回执。")
			return
		}
	}
	answer("日程已创建并完成明确修改；两封邮件已由服务受理，送达状态未知。")
}
