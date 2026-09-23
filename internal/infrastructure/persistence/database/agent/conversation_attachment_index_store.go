package agent

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"time"
)

func (s *ConversationStore) QueueAttachmentIndex(ctx context.Context, id string, expected int64, source persistence.ConversationAttachmentSource, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	return s.knowledgeStore().QueueAttachmentIndex(ctx, id, expected, source, a)
}

func (s *ConversationStore) ClaimAttachmentIndexWork(ctx context.Context, runtime, owner string, now time.Time, ttl time.Duration) (out persistence.ConversationAttachmentIndexLease, found bool, err error) {
	return s.knowledgeStore().ClaimAttachmentIndexWork(ctx, runtime, owner, now, ttl)
}

func (s *ConversationStore) attachmentIndexWork(ctx context.Context, db conversationDB, lease persistence.ConversationAttachmentIndexLease) (r persistence.ConversationAttachmentRecord, err error) {
	return s.knowledgeStore().CompatAttachmentIndexWork(ctx, db, lease)
}

func (s *ConversationStore) AttachmentIndexWorkRecord(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeStore().AttachmentIndexWorkRecord(ctx, lease)
}

func (s *ConversationStore) StartAttachmentIndexPut(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) (out persistence.ConversationAttachmentRecord, started bool, err error) {
	return s.knowledgeStore().StartAttachmentIndexPut(ctx, lease)
}

func (s *ConversationStore) StartAttachmentIndexDelete(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) error {
	return s.knowledgeStore().StartAttachmentIndexDelete(ctx, lease)
}

func (s *ConversationStore) ApplyAttachmentIndexProgress(ctx context.Context, lease persistence.ConversationAttachmentIndexLease, in persistence.ConversationAttachmentIndexProgress) error {
	return s.knowledgeStore().ApplyAttachmentIndexProgress(ctx, lease, in)
}
