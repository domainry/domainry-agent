package web

import (
	"testing"

	agent "github.com/domainry/domainry-agent-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestReportToolCompositionSelectionAndCollision(t *testing.T) {
	options := Options{}
	if err := configureReportDefinitions(&options); err != nil || len(options.ToolDefinitions) != 0 {
		t.Fatal("unselected report was added", err)
	}
	options.ReportTools = true
	if err := configureReportDefinitions(&options); err != nil || len(options.ToolDefinitions) != 1 || len(options.Agent.ConversationOptions.ToolDefinitions) != 1 {
		t.Fatal("report selection missing", err)
	}
	h := &Host{}
	if err := h.bindReportTools(&options); err != nil {
		t.Fatal(err)
	}
	ready, err := h.reportToolAvailability.ConversationToolAvailable(t.Context(), toolsdk.Authority{Known: true, RuntimeID: "r", WorkspaceID: "w", UserID: "u"}, toolsdk.ReportQueryToolKey)
	if err != nil || ready {
		t.Fatal("missing source was available", err)
	}
	for _, execution := range []bool{false, true} {
		duplicate := Options{ReportTools: true}
		if execution {
			duplicate.Agent.ConversationOptions.ToolDefinitions = toolsdk.ReportQueryDefinitions()
		} else {
			duplicate.ToolDefinitions = toolsdk.ReportQueryDefinitions()
		}
		if configureReportDefinitions(&duplicate) == nil {
			t.Fatal("duplicate report definition accepted")
		}
	}
	if toolsdk.ReportQueryDefinitions()[0].ActionKey != agent.ConversationToolActionPrefix+toolsdk.ReportQueryToolKey {
		t.Fatal("tool contract and permission namespace diverged")
	}
}
