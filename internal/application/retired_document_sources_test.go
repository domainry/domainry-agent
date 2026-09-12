package application

import (
	"encoding/json"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestRemovedOriginalParsingCannotAuthorizeOldResults(t *testing.T) {
	service := &ConversationService{}
	for _, content := range []string{`{"provider":"agent_parsed_document","data":{"blocks":["private"]}}`, `{"source":{"provider":"agent_attachment_document"}}`, `{"provider":"agent_attachments"}`} {
		result := agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(content)}
		if err := service.authorizeStoredToolResult(t.Context(), agentsdk.ConversationToolRequest{}, result); err == nil {
			t.Fatal("retired source survived removal without an authorizer")
		}
	}
	if retiredDocumentResult(agentsdk.ConversationToolResult{Content: json.RawMessage(`{"provider":"agent_library_documents","data":"agent_parsed_document"}`)}) {
		t.Fatal("ordinary connector source incorrectly retired")
	}
	if err := service.authorizeStoredToolResult(t.Context(), agentsdk.ConversationToolRequest{Call: agentsdk.ConversationToolCall{Name: "knowledge_read", Arguments: `{"attachment_id":"att_old"}`}}, agentsdk.ConversationToolResult{Status: "completed"}); err == nil {
		t.Fatal("old private attachment call survived removal")
	}
}
