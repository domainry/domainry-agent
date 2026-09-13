package application

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"testing"
)

func TestPeerToolSchemasCompile(t *testing.T) {
	for _, d := range sdk.ConversationCollaborationTools() {
		if _, err := compileConversationTools([]sdk.ConversationToolDefinition{d}); err != nil {
			t.Errorf("%s %s: %v", d.Key, d.InputSchema, err)
		}
		if _, err := compileConversationSchema(d.InputSchema); err != nil {
			t.Errorf("%s compile: %v", d.Key, err)
		}
	}
}
