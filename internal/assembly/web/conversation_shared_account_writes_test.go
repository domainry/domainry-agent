package web

import (
	"encoding/json"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-connector-sdk/calendar"
	"github.com/domainry/domainry-connector-sdk/calendarwrite"
	"github.com/domainry/domainry-connector-sdk/mail"
	"github.com/domainry/domainry-connector-sdk/mailwrite"
	integration "github.com/domainry/domainry-integration-sdk"
)

func TestCrossUserWorkspaceCalendarCreateReceiptsAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "calendar-create-write")
}

func TestCrossUserWorkspaceCalendarUpdateReceiptsAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "calendar-update-write")
}

func TestCrossUserWorkspaceMailReplyReceiptsAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "mail-reply-write")
}

func sharedAccountWriteSourceCalls(family string, account integration.ConnectionAccount) []sdk.ConversationToolCall {
	call := func(id, name string, args any) sdk.ConversationToolCall {
		return sdk.ConversationToolCall{ID: id, Name: name, Arguments: accountJSON(args)}
	}
	write := func(id, name string, request any) sdk.ConversationToolCall {
		return call(id, name, map[string]any{"account_key": account.Key, "account_updated_at": account.UpdatedAt, "request": request})
	}
	switch family {
	case "mail-write", "mail-reply-write":
		op := mailwrite.SendOperationKey
		message := mailwrite.Message{To: []mail.Address{{Address: "to@example.test"}}, CC: []mail.Address{{Address: "cc@example.test"}}, BCC: []mail.Address{{Address: "bcc@example.test"}}, Subject: "共享原回执", Text: "共享发送原正文\n收件人、抄送、密送及末尾内容均须保持。"}
		var request any = mailwrite.SendRequest{Message: message}
		if family == "mail-reply-write" {
			op = mailwrite.ReplyOperationKey
			message.To = []mail.Address{{Address: "reply@example.test"}}
			message.Subject, message.Text = "Re: 评审回复", "完整回复正文"
			request = mailwrite.ReplyRequest{MessageID: "original-mail", Message: message}
		}
		return []sdk.ConversationToolCall{call("accounts", "mail_write_accounts", map[string]string{"operation": op}), write("write", op, request)}
	case "calendar-create-write":
		return []sdk.ConversationToolCall{
			call("accounts", "calendar_write_accounts", map[string]string{"operation": calendarwrite.CreateOperationKey}),
			write("write", calendarwrite.CreateOperationKey, calendarwrite.CreateRequest{CalendarID: "primary", Notifications: calendarwrite.NotifyAttendees, Event: calendarwrite.Draft{Title: "完整日程标题", Description: "完整日程说明", Start: calendar.Moment{Date: "2026-11-01", TimeZone: "America/New_York"}, End: calendar.Moment{Date: "2026-11-02", TimeZone: "America/New_York"}, Attendees: []calendarwrite.Attendee{{Address: "invite@example.test", Name: "受邀者", Kind: "required"}}}}),
		}
	case "calendar-update-write":
		empty, attendees := "", []calendarwrite.Attendee{}
		return []sdk.ConversationToolCall{
			call("accounts", "calendar_write_accounts", map[string]string{"operation": calendarwrite.UpdateOperationKey}),
			call("inspect", calendarwrite.InspectOperationKey, map[string]string{"account_key": account.Key, "calendar_id": "primary", "event_id": "review-event", "time_zone": "America/New_York"}),
			write("write", calendarwrite.UpdateOperationKey, calendarwrite.UpdateRequest{CalendarID: "primary", EventID: "review-event", ExpectedVersion: "model-must-use-observed-version", Scope: calendarwrite.ScopeEvent, Notifications: calendarwrite.NotifyAttendees, Changes: calendarwrite.Patch{Description: &empty, Location: &empty, Attendees: &attendees}}),
		}
	}
	return nil
}

// Derive the mutation ETag from the actual model-visible inspect receipt.
func sharedAccountObservedCalendarUpdate(in sdk.ConversationStepRequest, call sdk.ConversationToolCall) (sdk.ConversationToolCall, error) {
	// The fixture placeholder is not an ETag. Decode without the request's
	// validation method until the actual inspected version has been inserted.
	type observedUpdate calendarwrite.UpdateRequest
	var args struct {
		AccountKey string         `json:"account_key"`
		UpdatedAt  string         `json:"account_updated_at"`
		Request    observedUpdate `json:"request"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return call, err
	}
	for _, message := range in.Messages {
		if message.Role != "tool" || message.ToolCallID != "inspect" {
			continue
		}
		var wire sdk.ConversationToolResult
		if err := json.Unmarshal([]byte(message.Content), &wire); err != nil || wire.Status != "completed" {
			continue
		}
		var envelope struct {
			Data calendarwrite.Snapshot `json:"data"`
		}
		if err := json.Unmarshal(wire.Content, &envelope); err != nil {
			return call, err
		}
		args.Request.ExpectedVersion = envelope.Data.Version
		if err := calendarwrite.UpdateRequest(args.Request).Validate(); err != nil {
			return call, err
		}
		call.Arguments = accountJSON(args)
		return call, nil
	}
	return call, nil
}
