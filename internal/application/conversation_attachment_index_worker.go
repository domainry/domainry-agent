package application

import (
	"context"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) attachmentIndexWorker(ctx context.Context, repo persistence.ConversationAttachmentIndexRepository) {
	s.knowledgeService().AttachmentIndexWorker(ctx, repo)
}

func (s *ConversationService) processAttachmentIndex(ctx context.Context, repo persistence.ConversationAttachmentIndexRepository, lease persistence.ConversationAttachmentIndexLease) {
	s.knowledgeService().ProcessAttachmentIndex(ctx, repo, lease)
}

func (s *ConversationService) attachmentIndexWriteAllowed(ctx context.Context, r persistence.ConversationAttachmentRecord) bool {
	return s.knowledgeService().AttachmentIndexWriteAllowed(ctx, r)
}
