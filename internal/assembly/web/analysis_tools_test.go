package web

import (
	toolsdk "github.com/domainry/domainry-tools-sdk"
	"testing"
)

func TestAnalysisSelectionCoexistsWithReportAndRejectsDuplicates(t *testing.T) {
	options := Options{}
	if err := configureAnalysisDefinitions(&options); err != nil || len(options.ToolDefinitions) != 0 {
		t.Fatal("unselected tool added", err)
	}
	options.AnalysisTools, options.ReportTools = true, true
	if err := configureReportDefinitions(&options); err != nil {
		t.Fatal(err)
	}
	if err := configureAnalysisDefinitions(&options); err != nil || len(options.ToolDefinitions) != 2 || len(options.Agent.ConversationOptions.ToolDefinitions) != 2 {
		t.Fatal("independent tool selection lost", err)
	}
	h := &Host{}
	if err := h.bindReportTools(&options); err != nil {
		t.Fatal(err)
	}
	if err := h.bindAnalysisTools(&options); err != nil {
		t.Fatal(err)
	}
	ready, err := h.analysisToolAvailability.ConversationToolAvailable(t.Context(), toolsdk.Authority{Known: true, RuntimeID: "r", WorkspaceID: "w", UserID: "u"}, toolsdk.AnalysisRunToolKey)
	if err != nil || ready {
		t.Fatal("unconfigured analysis became available", err)
	}
	for _, execution := range []bool{false, true} {
		duplicate := Options{AnalysisTools: true}
		if execution {
			duplicate.Agent.ConversationOptions.ToolDefinitions = toolsdk.AnalysisDefinitions()
		} else {
			duplicate.ToolDefinitions = toolsdk.AnalysisDefinitions()
		}
		if configureAnalysisDefinitions(&duplicate) == nil {
			t.Fatal("duplicate accepted")
		}
	}
}
