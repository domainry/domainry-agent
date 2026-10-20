package application

import (
	"bytes"
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

func (s *ConversationService) ImportConversationAttachment(ctx context.Context, library string, in agentsdk.KnowledgeAttachmentImport, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero agentsdk.KnowledgeDocument
	if !conversationKey(in.ClientID) || !conversationKey(in.ConversationID) || !conversationKey(in.AttachmentID) || in.ExpectedRevision < 1 {
		return zero, conversationFailure("bad_request", "document_import_invalid")
	}
	if _, err := s.documentAccess(ctx, library, knowledgesdk.ActionDocumentsUpload, a); err != nil {
		return zero, err
	}
	if _, err := s.documentBinding(ctx, library, a); err != nil {
		return zero, err
	}
	source, err := s.DownloadAttachment(ctx, in.ConversationID, in.AttachmentID, a)
	if err != nil {
		return zero, err
	}
	if source.Attachment.Revision != in.ExpectedRevision {
		return zero, conversationFailure("conflict", "revision_conflict")
	}
	if _, err = s.attachmentAccess(ctx, "attachments_download", a); err != nil {
		return zero, err
	}
	if _, err = s.documentAccess(ctx, library, knowledgesdk.ActionDocumentsUpload, a); err != nil {
		return zero, err
	}
	return s.knowledgeService().UploadKnowledgeDocument(ctx, library, knowledgesdk.KnowledgeDocumentUpload{
		ClientID: in.ClientID,
		Filename: source.Attachment.Filename,
		Bytes:    source.Attachment.Bytes,
		SHA256:   source.Attachment.SHA256,
		Content:  bytes.NewReader(source.Data),
	}, a)
}

var _ agentsdk.KnowledgeAttachmentImportService = (*ConversationService)(nil)
