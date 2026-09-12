package agent

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func (s *ConversationStore) AttachmentKnowledgeRecords(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) ([]persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().AttachmentKnowledgeRecords(ctx, conversation, a)
}
