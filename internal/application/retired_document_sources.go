package application

import (
	"encoding/json"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Old private source records must never become reusable after removing their authorizer.
func privateAttachmentCall(call agentsdk.ConversationToolCall) bool {
	if call.Name == "knowledge_attachments" {
		return true
	}
	if call.Name != "knowledge_read" && call.Name != "knowledge_extract" {
		return false
	}
	var args struct {
		AttachmentID string `json:"attachment_id"`
	}
	return json.Unmarshal([]byte(call.Arguments), &args) == nil && args.AttachmentID != ""
}

func retiredDocumentResult(result agentsdk.ConversationToolResult) bool {
	var data struct {
		Provider string `json:"provider"`
		Source   struct {
			Provider string `json:"provider"`
		} `json:"source"`
	}
	if json.Unmarshal(result.Content, &data) != nil {
		return false
	}
	retired := func(p string) bool {
		return p == "agent_parsed_document" || p == "agent_attachment_document" || p == "agent_attachments"
	}
	return retired(data.Provider) || retired(data.Source.Provider)
}
