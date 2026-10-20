package application

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/artifact"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) attachmentAccess(ctx context.Context, action string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRepository, error) {
	if err := s.authorize(a); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationAttachmentRepository)
	if !ok || s.options.AttachmentAuthorizer == nil {
		return nil, conversationFailure("unavailable", "attachments_unavailable")
	}
	if err := s.options.AttachmentAuthorizer.AuthorizeConversationAttachment(ctx, action, a); err != nil {
		return nil, err
	}
	return repo, nil
}

func AttachmentContentType(filename string) string {
	return map[string]string{".pdf": "application/pdf", ".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".txt": "text/plain", ".md": "text/markdown", ".csv": "text/csv", ".tsv": "text/tab-separated-values", ".json": "application/json", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[strings.ToLower(filepath.Ext(filename))]
}

func attachmentImageContentType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
}

func (s *ConversationService) UploadAttachment(ctx context.Context, conversationID string, in agentsdk.ConversationAttachmentUpload, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero agentsdk.ConversationAttachment
	repo, err := s.attachmentAccess(ctx, "attachments_upload", a)
	if err != nil {
		return zero, err
	}
	if len(in.Data) == 0 || int64(len(in.Data)) > agentsdk.ConversationAttachmentMaxBytes {
		return zero, conversationFailure("bad_request", "attachment_size_invalid")
	}
	contentType := AttachmentContentType(in.Filename)
	if contentType == "" {
		return zero, conversationFailure("bad_request", "attachment_type_unsupported")
	}
	// Image inputs are sent to model providers as their declared media type.
	// Refuse extension spoofing before reserving storage metadata.
	if strings.HasPrefix(contentType, "image/") && attachmentImageContentType(in.Data) != contentType {
		return zero, conversationFailure("bad_request", "attachment_type_unsupported")
	}
	conversation, err := s.Get(ctx, conversationID, a)
	if err != nil {
		return zero, err
	}
	if conversation.Archived {
		return zero, conversationFailure("conflict", "attachment_conversation_archived")
	}
	hash := artifact.Hash(in.Data)
	record, err := repo.ReserveAttachment(ctx, persistence.ConversationAttachmentReserve{ClientID: in.ClientID, ConversationID: conversationID, Filename: in.Filename, ContentType: contentType, SHA256: hash, Bytes: int64(len(in.Data)), Content: in.Data}, a)
	if err != nil {
		return zero, err
	}
	if record.Attachment.State == "deleting" || record.Attachment.State == "deleted" {
		return zero, conversationFailure("not_found", "attachment_not_found")
	}
	if _, err := s.attachmentAccess(ctx, "attachments_upload", a); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if deleting, transitionErr := repo.TransitionAttachment(cleanupCtx, record.Attachment.ID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a); transitionErr == nil {
			_ = repo.DeleteAttachmentContent(cleanupCtx, record.Attachment.ID, a)
			_, _ = repo.TransitionAttachment(cleanupCtx, record.Attachment.ID, deleting.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, a)
		}
		return zero, err
	}
	return s.attachmentView(record.Attachment), nil
}

func (s *ConversationService) attachmentView(item agentsdk.ConversationAttachment) agentsdk.ConversationAttachment {
	item.Indexing = nil
	return item
}

func (s *ConversationService) attachmentRecord(ctx context.Context, repo persistence.ConversationAttachmentRepository, conversationID, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return persistence.ConversationAttachmentRecord{}, err
	}
	record, err := repo.AttachmentRecord(ctx, id, a)
	if err == nil && record.Attachment.ConversationID != conversationID {
		err = conversationFailure("not_found", "attachment_not_found")
	}
	return record, err
}

func (s *ConversationService) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	repo, err := s.attachmentAccess(ctx, "attachments_list", a)
	if err != nil {
		return agentsdk.ConversationAttachmentPage{}, err
	}
	page, err := repo.Attachments(ctx, conversationID, after, limit, a)
	for i := range page.Items {
		page.Items[i] = s.attachmentView(page.Items[i])
	}
	return page, err
}

func (s *ConversationService) Attachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	repo, err := s.attachmentAccess(ctx, "attachments_get", a)
	if err != nil {
		return agentsdk.ConversationAttachment{}, err
	}
	record, err := s.attachmentRecord(ctx, repo, conversationID, id, a)
	if err == nil && record.Attachment.State == "deleted" {
		err = conversationFailure("not_found", "attachment_not_found")
	}
	if err != nil {
		return agentsdk.ConversationAttachment{}, err
	}
	return s.attachmentView(record.Attachment), nil
}

func (s *ConversationService) DownloadAttachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentDownload, error) {
	var out agentsdk.ConversationAttachmentDownload
	repo, err := s.attachmentAccess(ctx, "attachments_download", a)
	if err != nil {
		return out, err
	}
	record, err := s.attachmentRecord(ctx, repo, conversationID, id, a)
	if err != nil {
		return out, err
	}
	if record.Attachment.State == "deleting" || record.Attachment.State == "deleted" {
		return out, conversationFailure("not_found", "attachment_content_not_found")
	}
	raw, err := repo.AttachmentContent(ctx, id, a)
	if err != nil {
		return out, err
	}
	if int64(len(raw)) != record.Attachment.Bytes || artifact.Hash(raw) != record.Attachment.SHA256 {
		return out, conversationFailure("unavailable", "attachment_content_mismatch")
	}
	if _, err := s.attachmentAccess(ctx, "attachments_download", a); err != nil {
		return out, err
	}
	current, err := s.attachmentRecord(ctx, repo, conversationID, id, a)
	if err != nil {
		return out, err
	}
	if current.Attachment.State == "deleting" || current.Attachment.State == "deleted" || current.Attachment.SHA256 != record.Attachment.SHA256 {
		return out, conversationFailure("not_found", "attachment_not_found")
	}
	return agentsdk.ConversationAttachmentDownload{Attachment: s.attachmentView(current.Attachment), Data: raw}, nil
}

func (s *ConversationService) DeleteAttachment(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero agentsdk.ConversationAttachment
	repo, err := s.attachmentAccess(ctx, "attachments_delete", a)
	if err != nil {
		return zero, err
	}
	if expected < 1 {
		return zero, conversationFailure("bad_request", "revision_required")
	}
	record, err := s.attachmentRecord(ctx, repo, conversationID, id, a)
	if err != nil {
		return zero, err
	}
	if record.Attachment.State == "deleted" {
		return s.attachmentView(record.Attachment), nil
	}
	if record.Attachment.State != "deleting" {
		record, err = repo.TransitionAttachment(ctx, id, expected, persistence.ConversationAttachmentTransition{State: "deleting"}, a)
		if err != nil {
			return zero, err
		}
	}
	if err = repo.DeleteAttachmentContent(ctx, id, a); err != nil {
		return zero, err
	}
	record, err = repo.TransitionAttachment(ctx, id, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, a)
	if err != nil {
		return zero, err
	}
	return s.attachmentView(record.Attachment), nil
}

var _ agentsdk.ConversationAttachmentService = (*ConversationService)(nil)
