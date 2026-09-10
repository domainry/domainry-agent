package application

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func (s *ConversationService) attachmentAccess(ctx context.Context, action string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRepository, error) {
	if err := s.authorize(a); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationAttachmentRepository)
	if !ok || s.options.AttachmentStorage == nil || s.options.AttachmentAuthorizer == nil {
		return nil, conversationFailure("unavailable", "attachments_unavailable")
	}
	if err := s.options.AttachmentAuthorizer.AuthorizeConversationAttachment(ctx, action, a); err != nil {
		return nil, err
	}
	return repo, nil
}

func attachmentContentType(filename string) string {
	return map[string]string{".pdf": "application/pdf", ".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".txt": "text/plain", ".md": "text/markdown", ".csv": "text/csv", ".tsv": "text/tab-separated-values", ".json": "application/json"}[strings.ToLower(filepath.Ext(filename))]
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
	contentType := attachmentContentType(in.Filename)
	if contentType == "" {
		return zero, conversationFailure("bad_request", "attachment_type_unsupported")
	}
	conversation, err := s.repo.Get(ctx, conversationID, a)
	if err != nil {
		return zero, err
	}
	if conversation.Archived {
		return zero, conversationFailure("conflict", "attachment_conversation_archived")
	}
	hash := artifact.Hash(in.Data)
	record, err := repo.ReserveAttachment(ctx, persistence.ConversationAttachmentReserve{ClientID: in.ClientID, ConversationID: conversationID, Filename: in.Filename, ContentType: contentType, SHA256: hash, Bytes: int64(len(in.Data))}, a)
	if err != nil {
		return zero, err
	}
	if record.Attachment.State == "deleting" || record.Attachment.State == "deleted" {
		return zero, conversationFailure("not_found", "attachment_not_found")
	}
	if record.BodyRef != "" {
		return record.Attachment, nil
	}
	if record.Attachment.State == "failed" {
		record, err = repo.TransitionAttachment(ctx, record.Attachment.ID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "uploading"}, a)
		if err != nil {
			return zero, err
		}
	}
	reference, err := s.options.AttachmentStorage.PutAttachmentContent(ctx, record.Attachment.ID, hash, in.Data, a)
	if err != nil {
		// A cancelled upload remains retryable; a concrete storage failure has a
		// stable public code. Neither can silently mark the attachment indexed.
		if ctx.Err() == nil {
			_, _ = repo.TransitionAttachment(ctx, record.Attachment.ID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "failed", ErrorCode: "upload_failed"}, a)
		}
		return zero, err
	}
	if _, err := s.attachmentAccess(ctx, "attachments_upload", a); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = repo.TransitionAttachment(cleanupCtx, record.Attachment.ID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a)
		s.wakeAttachmentCleanup()
		return zero, err
	}
	stored, err := repo.TransitionAttachment(ctx, record.Attachment.ID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "stored", BodyRef: reference}, a)
	if err != nil {
		// Concurrent identical uploads may have finalized the same immutable
		// object. A deletion or a different reference must never be overwritten.
		current, readErr := repo.AttachmentRecord(ctx, record.Attachment.ID, a)
		if readErr == nil && current.BodyRef == reference && current.Attachment.State != "deleting" && current.Attachment.State != "deleted" {
			return current.Attachment, nil
		}
		s.wakeAttachmentCleanup()
		return zero, err
	}
	return stored.Attachment, nil
}

func (s *ConversationService) attachmentRecord(ctx context.Context, repo persistence.ConversationAttachmentRepository, conversationID, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	if _, err := s.repo.Get(ctx, conversationID, a); err != nil {
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
	return repo.Attachments(ctx, conversationID, after, limit, a)
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
	return record.Attachment, err
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
	if record.BodyRef == "" || record.Attachment.State == "deleting" || record.Attachment.State == "deleted" {
		return out, conversationFailure("not_found", "attachment_content_not_found")
	}
	raw, err := s.options.AttachmentStorage.ReadAttachmentContent(ctx, id, record.BodyRef, a)
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
	if current.Attachment.State == "deleting" || current.Attachment.State == "deleted" || current.BodyRef != record.BodyRef {
		return out, conversationFailure("not_found", "attachment_not_found")
	}
	return agentsdk.ConversationAttachmentDownload{Attachment: current.Attachment, Data: raw}, nil
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
		return record.Attachment, nil
	}
	if record.Attachment.State != "deleting" {
		record, err = repo.TransitionAttachment(ctx, id, expected, persistence.ConversationAttachmentTransition{State: "deleting"}, a)
		if err != nil {
			return zero, err
		}
	}
	s.wakeAttachmentCleanup()
	// The durable worker owns physical cleanup. The immediate result confirms
	// revoked access and honestly reports that cleanup is still pending.
	return record.Attachment, nil
}

func (s *ConversationService) wakeAttachmentCleanup() {
	select {
	case s.attachmentWake <- struct{}{}:
	default:
	}
}

func (s *ConversationService) cleanAttachment(ctx context.Context, repo persistence.ConversationAttachmentRepository, work persistence.ConversationAttachmentCleanup) error {
	record, err := repo.AttachmentRecord(ctx, work.AttachmentID, work.Authority)
	if err != nil {
		return err
	}
	if record.Attachment.State != "deleting" {
		return nil
	}
	// A remote source requires a verified remote deletion adapter. Keep its
	// durable references until that adapter exists; never fake successful cleanup.
	if record.Source != nil {
		return conversationFailure("unavailable", "attachment_remote_cleanup_unavailable")
	}
	if err := s.options.AttachmentStorage.DeleteAttachmentContent(ctx, work.AttachmentID, work.Authority); err != nil {
		return err
	}
	_, err = repo.TransitionAttachment(ctx, work.AttachmentID, record.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, work.Authority)
	return err
}

func (s *ConversationService) cleanupAttachments(ctx context.Context, repo persistence.ConversationAttachmentRepository) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		work, err := repo.AttachmentCleanupCandidates(ctx, s.runtimeID, time.Now().UTC(), 20)
		if err == nil {
			for _, item := range work {
				attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := s.cleanAttachment(attempt, repo, item)
				cancel()
				if err != nil {
					_ = repo.DeferAttachmentCleanup(ctx, item.AttachmentID, time.Now().UTC().Add(30*time.Second), item.Authority)
				}
				if ctx.Err() != nil {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.attachmentWake:
		case <-ticker.C:
		}
	}
}

var _ agentsdk.ConversationAttachmentService = (*ConversationService)(nil)
