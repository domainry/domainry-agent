package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) ImportConversationAttachment(ctx context.Context, library string, in agentsdk.KnowledgeAttachmentImport, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().ImportConversationAttachment(ctx, library, in, a)
}
