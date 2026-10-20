package application

import (
	"context"

	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) attachmentIndexWriteAllowed(ctx context.Context, r persistence.ConversationAttachmentRecord) bool {
	return s.knowledgeService().AttachmentIndexWriteAllowed(ctx, r)
}
