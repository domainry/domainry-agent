package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type timezoneAuthorizer string

func (a timezoneAuthorizer) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true, UserTimezone: string(a)}, nil
}

func TestPersonalTimeUsesLiveProfileAndCalendarBoundaries(t *testing.T) {
	host := &PersonalConversationHost{authorizer: timezoneAuthorizer("America/New_York"), timezone: time.UTC, now: func() time.Time { return time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC) }}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.PersonalConversationTools() {
		if tool.Key == "time_now" {
			definition = tool
		}
	}
	for _, test := range []struct{ args, wantDate, wantRelative, wantSource string }{
		{`{"relative_date":"tomorrow"}`, "2026-03-07", "2026-03-08", "user_profile"},
		{`{"relative_date":"next_week"}`, "2026-03-07", "2026-03-09", "user_profile"},
		{`{"timezone":"Asia/Shanghai","relative_date":"tomorrow"}`, "2026-03-08", "2026-03-09", "requested"},
	} {
		out, err := host.InvokeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: agentsdk.ConversationAuthority{Known: true}, Definition: definition, Call: agentsdk.ConversationToolCall{Name: "time_now", Arguments: test.args}})
		var value map[string]any
		_ = json.Unmarshal(out.Content, &value)
		if err != nil || value["date"] != test.wantDate || value["resolved_date"] != test.wantRelative || value["timezone_source"] != test.wantSource {
			t.Fatalf("time=%v %v", value, err)
		}
	}
}
