package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) attachmentAccess(ctx context.Context, action string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRepository, error) {
	return s.knowledgeService().AttachmentAccess(ctx, action, a)
}

func (s *ConversationService) UploadAttachment(ctx context.Context, conversationID string, in agentsdk.ConversationAttachmentUpload, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	return s.knowledgeService().UploadAttachment(ctx, conversationID, in, a)
}

func (s *ConversationService) attachmentView(item agentsdk.ConversationAttachment) agentsdk.ConversationAttachment {
	return s.knowledgeService().AttachmentView(item)
}

func (s *ConversationService) attachmentRecord(ctx context.Context, repo persistence.ConversationAttachmentRepository, conversationID, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	return s.knowledgeService().AttachmentRecord(ctx, repo, conversationID, id, a)
}

func (s *ConversationService) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	return s.knowledgeService().Attachments(ctx, conversationID, after, limit, a)
}

func (s *ConversationService) Attachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	return s.knowledgeService().Attachment(ctx, conversationID, id, a)
}

func (s *ConversationService) DownloadAttachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentDownload, error) {
	return s.knowledgeService().DownloadAttachment(ctx, conversationID, id, a)
}

func (s *ConversationService) DeleteAttachment(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	return s.knowledgeService().DeleteAttachment(ctx, conversationID, id, expected, a)
}

func (s *ConversationService) wakeAttachmentCleanup() { s.knowledgeService().WakeAttachmentCleanup() }

func (s *ConversationService) cleanAttachment(ctx context.Context, repo persistence.ConversationAttachmentRepository, work persistence.ConversationAttachmentCleanup) error {
	return s.knowledgeService().CleanAttachment(ctx, repo, work)
}

func (s *ConversationService) cleanupAttachments(ctx context.Context, repo persistence.ConversationAttachmentRepository) {
	s.knowledgeService().CleanupAttachments(ctx, repo)
}
