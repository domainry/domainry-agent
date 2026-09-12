package application

import (
	toolsdk "github.com/domainry/domainry-tools-sdk"
	"testing"
)

func TestAnalysisToolPublicContractFitsConversationEngine(t *testing.T) {
	compiled, err := compileConversationTools(toolsdk.AnalysisDefinitions())
	if err != nil || len(compiled) != 1 {
		t.Fatal("public analysis contract cannot enter execution catalog", compiled, err)
	}
}
