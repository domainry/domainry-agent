package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) attachmentKnowledgeAccess(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, map[string]persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeService().AttachmentKnowledgeAccess(ctx, conversation, a)
}

func (s *ConversationService) attachmentKnowledge(ctx context.Context, conversation, op, q, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	return s.knowledgeService().AttachmentKnowledge(ctx, conversation, op, q, id, a)
}

func (s *ConversationService) authorizeAttachmentKnowledgeResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	auth, err := s.authorizeAttachmentKnowledgeTool(ctx, in)
	if err != nil {
		return err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return conversationFailure("forbidden", "tool_access_denied")
	}
	return s.knowledgeService().AuthorizeAttachmentKnowledgeResult(ctx, in, result)
}
