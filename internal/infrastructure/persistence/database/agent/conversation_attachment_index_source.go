package agent

import (
	"context"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func (s *ConversationStore) attachmentKnowledgeSourceOwner(ctx context.Context, db conversationDB, source string) (string, error) {
	return s.knowledgeStore().CompatAttachmentKnowledgeSourceOwner(ctx, db, source)
}

func (s *ConversationStore) ActivateAttachmentKnowledgeSource(ctx context.Context, runtime, workspace, source string) error {
	return s.knowledgeStore().ActivateAttachmentKnowledgeSource(ctx, runtime, workspace, source)
}
