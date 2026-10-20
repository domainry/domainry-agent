package agent

import (
	"context"
	"sort"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationStore) AttachmentKnowledgeRecords(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) ([]persistence.ConversationAttachmentRecord, error) {
	if _, err := s.Get(ctx, conversation, a); err != nil {
		return nil, err
	}
	items, err := s.attachmentArtifactsForConversation(ctx, s.store.Database(), conversation, a)
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	out := []persistence.ConversationAttachmentRecord{}
	for _, item := range items {
		if len(out) >= 50 {
			return nil, conversationError("unavailable", "attachment_metadata_invalid")
		}
		_, record, loadErr := s.attachmentArtifact(ctx, s.store.Database(), item.ID, a)
		if loadErr != nil {
			return nil, loadErr
		}
		if record.Attachment.ConversationID != conversation {
			return nil, conversationError("unavailable", "attachment_metadata_invalid")
		}
		if record.Attachment.State == "ready" {
			out = append(out, record)
		}
	}
	return out, nil
}

var _ persistence.ConversationAttachmentKnowledgeRepository = (*ConversationStore)(nil)
