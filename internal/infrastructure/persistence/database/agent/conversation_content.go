package agent

import (
	"encoding/hex"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

var storedConversationImageID = regexp.MustCompile(`^att_[a-f0-9]{32}$`)

func storedConversationImageType(value string) bool {
	switch value {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// Content bytes are deliberately absent from every durable record. A source
// reference identifies provenance only; the application must still authorize
// and load the attachment before each provider request.
func validStoredConversationContent(role, content string, blocks []agentsdk.ConversationContentBlock, directConversation string, allowSource bool) bool {
	if !executionText(content, 4*1024*1024, false) || len(blocks) > 16 {
		return false
	}
	if len(blocks) == 0 {
		return true
	}
	var text strings.Builder
	images := 0
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Image != nil || !executionText(block.Text, 4*1024*1024-text.Len(), false) {
				return false
			}
			text.WriteString(block.Text)
		case "image":
			images++
			image := block.Image
			if role != "user" || images > 4 || block.Text != "" || image == nil || len(image.Data) != 0 || !storedConversationImageID.MatchString(image.AttachmentID) ||
				!personalMemoryKey(image.ConversationID) || !executionText(image.Filename, 255, true) || !storedConversationImageType(image.ContentType) ||
				image.Bytes < 1 || image.Bytes > agentsdk.ConversationAttachmentMaxBytes || image.Revision < 1 || len(image.SHA256) != 64 ||
				(image.Detail != "auto" && image.Detail != "low" && image.Detail != "high") {
				return false
			}
			if digest, err := hex.DecodeString(image.SHA256); err != nil || len(digest) != 32 {
				return false
			}
			if image.Source == nil {
				if directConversation != "" && image.ConversationID != directConversation {
					return false
				}
				continue
			}
			if !allowSource || image.Source.ConversationID != image.ConversationID || !personalMemoryKey(image.Source.ConversationID) ||
				!personalMemoryKey(image.Source.RunID) || image.Source.BeforeStep < 0 || image.Source.BeforeStep > 257 {
				return false
			}
		default:
			return false
		}
	}
	return (images > 0 || text.Len() > 0) && text.String() == content
}

func validStoredConversationModelMessages(messages []agentsdk.ConversationModelMessage, directConversation string) bool {
	for _, message := range messages {
		if message.Role != "system" && message.Role != "user" && message.Role != "assistant" ||
			!validStoredConversationContent(message.Role, message.Content, message.ContentBlocks, directConversation, true) {
			return false
		}
	}
	return true
}

func validStoredConversationStepMessages(messages []agentsdk.ConversationStepMessage, directConversation string) bool {
	for _, message := range messages {
		if !validStoredConversationContent(message.Role, message.Content, message.ContentBlocks, directConversation, true) {
			return false
		}
	}
	return true
}

func storedConversationModelMessagesHaveImages(messages []agentsdk.ConversationModelMessage) bool {
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block.Type == "image" && block.Image != nil {
				return true
			}
		}
	}
	return false
}

func storedConversationStepMessagesHaveImages(messages []agentsdk.ConversationStepMessage) bool {
	for _, message := range messages {
		for _, block := range message.ContentBlocks {
			if block.Type == "image" && block.Image != nil {
				return true
			}
		}
	}
	return false
}

func validStoredConversationTaskContent(task agentsdk.ConversationTask) bool {
	if len(task.InputContent) == 0 {
		return true
	}
	if !validStoredConversationContent("user", "", task.InputContent, "", true) || task.SourceConversationID == "" || task.SourceRunID == "" || task.Model == nil || !task.Model.Capabilities.ImageInput {
		return false
	}
	for _, block := range task.InputContent {
		if block.Type != "image" || block.Image == nil || block.Image.Source == nil ||
			block.Image.Source.ConversationID != task.SourceConversationID || block.Image.Source.RunID != task.SourceRunID {
			return false
		}
	}
	return true
}

func validStoredConversationBusinessEvent(task agentsdk.ConversationTask) bool {
	if task.BusinessEvent == nil {
		return true
	}
	event := task.BusinessEvent
	if event.TargetAgentID == "" || task.Agent == nil || event.TargetAgentID != task.Agent.ID || event.Source.ReceivedAt.IsZero() ||
		event.Execution.WorkspaceID == "" || event.Execution.UserID == "" ||
		!scheduledConversationStoreKey(event.Source.EventID) || !scheduledConversationStoreKey(event.Source.Provider) ||
		!scheduledConversationStoreKey(event.Source.EventType) || !executionText(event.Source.ExternalID, 1024, true) ||
		!scheduledConversationStoreKey(event.Rule.Key) || !scheduledConversationStoreKey(event.IdempotencyKey) || len(event.Rule.Revision) != 64 || strings.ToLower(event.Rule.Revision) != event.Rule.Revision {
		return false
	}
	if digest, err := hex.DecodeString(event.Rule.Revision); err != nil || len(digest) != 32 {
		return false
	}
	switch event.Mode {
	case "start":
		return event.RelatedTaskID == ""
	case "wake":
		return personalMemoryKey(event.RelatedTaskID)
	default:
		return false
	}
}
