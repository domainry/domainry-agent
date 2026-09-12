package application

import (
	"context"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) knowledgeDocumentWorker(ctx context.Context, repo persistence.KnowledgeDocumentRepository) {
	s.knowledgeService().KnowledgeDocumentWorker(ctx, repo)
}

func (s *ConversationService) processKnowledgeDocument(ctx context.Context, repo persistence.KnowledgeDocumentRepository, lease persistence.KnowledgeDocumentLease) {
	s.knowledgeService().ProcessKnowledgeDocument(ctx, repo, lease)
}
