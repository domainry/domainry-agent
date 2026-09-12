package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"time"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func (s *ConversationStore) queueAttachmentCleanup(ctx context.Context, tx *sql.Tx, id string, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().CompatQueueAttachmentCleanup(ctx, tx, id, a)
}

func (s *ConversationStore) AttachmentCleanupCandidates(ctx context.Context, runtimeID string, now time.Time, limit int) ([]persistence.ConversationAttachmentCleanup, error) {
	return s.knowledgeStore().AttachmentCleanupCandidates(ctx, runtimeID, now, limit)
}

func (s *ConversationStore) DeferAttachmentCleanup(ctx context.Context, id string, until time.Time, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().DeferAttachmentCleanup(ctx, id, until, a)
}
