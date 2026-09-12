package application

import (
	"testing"

	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestReportToolPublicContractFitsConversationEngine(t *testing.T) {
	compiled, err := compileConversationTools(toolsdk.ReportQueryDefinitions())
	if err != nil || len(compiled) != 1 {
		t.Fatal("shared Report tool contract cannot enter the execution catalog", compiled, err)
	}
}
