package agent

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func (s *ConversationStore) RequestAttachmentIndexCheck(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	return s.knowledgeStore().RequestAttachmentIndexCheck(ctx, conversation, id, expected, a)
}
