package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) TransferKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentTransfer, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().TransferKnowledgeDocument(ctx, library, in, a)
}
