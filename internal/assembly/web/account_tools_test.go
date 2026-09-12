package web

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

func TestAccountToolCompositionSelectionAndCollision(t *testing.T) {
	for _, test := range []struct {
		name                string
		calendar, mail, web bool
		count               int
	}{{"none", false, false, false, 0}, {"calendar", true, false, false, 5}, {"mail", false, true, false, 4}, {"web", false, false, true, 2}, {"calendar-mail", true, true, false, 9}, {"calendar-web", true, false, true, 7}, {"mail-web", false, true, true, 6}, {"all", true, true, true, 11}} {
		t.Run(test.name, func(t *testing.T) {
			options := Options{CalendarTools: test.calendar, MailTools: test.mail, WebTools: test.web}
			if err := configureAccountDefinitions(&options); err != nil {
				t.Fatal(err)
			}
			if len(options.ToolDefinitions) != test.count || len(options.Agent.ConversationOptions.ToolDefinitions) != test.count {
				t.Fatal("incorrect selected family")
			}
			h := &Host{}
			if test.count == 0 {
				return
			}
			if err := h.bindAccountTools(&options); err != nil {
				t.Fatal(err)
			}
			if len(h.accountToolAvailability) != test.count {
				t.Fatal("family availability lost")
			}
			for _, d := range options.ToolDefinitions {
				available, selected, err := h.accountToolAvailable(context.Background(), sdk.ConversationAuthority{Known: true, RuntimeID: "r", WorkspaceID: "w", UserID: "u"}, d.Key)
				if err != nil || available || !selected {
					t.Fatalf("unconfigured owner unexpectedly available %s", d.Key)
				}
			}
			if _, selected, _ := h.accountToolAvailable(context.Background(), sdk.ConversationAuthority{}, "unselected"); selected {
				t.Fatal("unselected tool intercepted")
			}
		})
	}
	definitions := append(toolmodule.CalendarDefinitions(), toolmodule.MailDefinitions()...)
	definitions = append(definitions, toolmodule.WebDefinitions()...)
	for _, d := range definitions {
		for _, execution := range []bool{false, true} {
			options := Options{CalendarTools: true, MailTools: true, WebTools: true}
			if execution {
				options.Agent.ConversationOptions.ToolDefinitions = []sdk.ConversationToolDefinition{d}
			} else {
				options.ToolDefinitions = []sdk.ConversationToolDefinition{d}
			}
			if configureAccountDefinitions(&options) == nil {
				t.Fatalf("duplicate accepted %s execution=%v", d.Key, execution)
			}
		}
	}
}
