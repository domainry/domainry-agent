package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"slices"
)

func (s *ConversationService) attachmentIndexView(ctx context.Context, r persistence.ConversationAttachmentRecord, a agentsdk.ConversationAuthority) agentsdk.ConversationAttachment {
	out := s.attachmentView(r.Attachment)
	v := &agentsdk.ConversationAttachmentIndexView{Requested: r.Index != nil}
	if r.Index != nil {
		v.LastCheckRevision = r.Index.LastCheckRevision
	}
	out.Indexing = v
	scope, err := s.attachmentKnowledgeBinding(ctx, r.Attachment.ConversationID, a)
	if err != nil {
		v.Reason = "attachment_index_unavailable"
		return out
	}
	v.MaxBytes = DocumentIndexMaximumBytesForFilename(scope.Source, r.Attachment.Filename)
	if r.Source != nil && (r.Source.Identity != scope.Source.KnowledgeDocumentSourceIdentity() || r.Source.AccessPolicySHA256 != scope.Source.KnowledgeDocumentAccessPolicySHA256() || !slices.Equal(r.Source.DocumentPermissionIDs, scope.DocumentPermissionIDs)) {
		v.Reason = "attachment_source_changed"
		return out
	}
	parent, err := s.Get(ctx, r.Attachment.ConversationID, a)
	if err != nil {
		v.Reason = "attachment_index_access_denied"
		return out
	}
	if r.Attachment.State == "deleted" {
		return out
	}
	if parent.Archived && r.Attachment.State != "deleting" {
		v.Reason = "attachment_conversation_archived"
		return out
	}
	if r.Attachment.State == "deleting" {
		_, err = s.attachmentAccess(ctx, "attachments_delete", a)
	} else {
		_, err = s.attachmentAccess(ctx, "attachments_index", a)
		if err == nil {
			_, err = s.attachmentAccess(ctx, "attachments_download", a)
		}
	}
	if err != nil {
		v.Reason = "attachment_index_access_denied"
		return out
	}
	if r.Index == nil {
		if r.Attachment.Bytes > v.MaxBytes {
			v.Reason = "attachment_size_invalid"
			return out
		}
		v.CanStart = r.Attachment.State == "stored" && r.Source == nil
		return out
	}
	if _, ok := s.repo.(persistence.ConversationAttachmentIndexCheckRepository); ok {
		_, err = s.attachmentAccess(ctx, "attachments_check_index", a)
		v.CanCheck = err == nil
	}
	if !v.CanCheck {
		v.Reason = "attachment_index_check_denied"
	}
	return out
}

func (s *ConversationService) CheckAttachmentIndex(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero agentsdk.ConversationAttachment
	records, err := s.attachmentAccess(ctx, "attachments_check_index", a)
	if err != nil {
		return zero, err
	}
	r, err := s.attachmentRecord(ctx, records, conversation, id, a)
	if err != nil {
		return zero, err
	}
	view := s.attachmentIndexView(ctx, r, a)
	if view.Indexing == nil || !view.Indexing.Requested {
		return zero, conversationFailure("conflict", "attachment_index_not_requested")
	}
	if r.Attachment.State == "deleted" {
		return view, nil
	}
	if !view.Indexing.CanCheck {
		class := "forbidden"
		if view.Indexing.Reason == "attachment_index_unavailable" {
			class = "unavailable"
		} else if view.Indexing.Reason == "attachment_source_changed" {
			class = "conflict"
		}
		return zero, conversationFailure(class, view.Indexing.Reason)
	}
	repo, ok := s.repo.(persistence.ConversationAttachmentIndexCheckRepository)
	if !ok {
		return zero, conversationFailure("unavailable", "attachment_index_unavailable")
	}
	r, err = repo.RequestAttachmentIndexCheck(ctx, conversation, id, expected, a)
	if err != nil {
		return zero, err
	}
	s.wakeAttachmentIndex()
	return s.attachmentIndexView(ctx, r, a), nil
}

var _ agentsdk.ConversationAttachmentIndexCheckService = (*ConversationService)(nil)
