package agent

import (
	"context"
	"database/sql"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func knowledgeDocumentReservationID(library, client string, namespace string, a agentsdk.ConversationAuthority) string {
	parts := []string{conversationOwner(a), library, client}
	if namespace != "" {
		parts = append(parts, namespace)
	}
	return "kdoc_" + conversationHash(parts)[:32]
}
func validAttachmentOrigin(origin persistence.KnowledgeAttachmentOrigin) bool {
	return personalMemoryKey(origin.ConversationID) && personalMemoryKey(origin.AttachmentID) && origin.Revision > 0
}
func (s *ConversationStore) FindKnowledgeAttachmentImport(ctx context.Context, library, client string, origin persistence.KnowledgeAttachmentOrigin, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, found bool, err error) {
	if conversationAuthority(a) != nil || !validLibraryID(library) || !personalMemoryKey(client) || !validAttachmentOrigin(origin) {
		return out, false, conversationError("bad_request", "document_import_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, e := s.library(ctx, tx, library, a); e != nil {
			return e
		}
		var e error
		found, e = s.executionRead(ctx, tx, knowledgeDocumentTable, documentScope(a, knowledgeDocumentReservationID(library, client, "conversation_attachment_import.v1", a)), &out)
		if e != nil {
			return e
		}
		if found && (out.AttachmentOrigin == nil || *out.AttachmentOrigin != origin) {
			return conversationError("conflict", "idempotency_conflict")
		}
		return nil
	})
	return
}

// Check within the same transaction that reserves/commits the target document.
// A deletion that wins before commit cannot publish an independent library copy.
func (s *ConversationStore) checkDocumentAttachmentOrigin(ctx context.Context, tx *sql.Tx, origin *persistence.KnowledgeAttachmentOrigin, doc agentsdk.KnowledgeDocument, a agentsdk.ConversationAuthority) error {
	if origin == nil {
		return nil
	}
	if !validAttachmentOrigin(*origin) {
		return conversationError("bad_request", "document_import_invalid")
	}
	if _, err := s.get(ctx, tx, origin.ConversationID, a); err != nil {
		return err
	}
	source, err := s.attachment(ctx, tx, origin.AttachmentID, a)
	if err != nil {
		return err
	}
	if source.Attachment.ConversationID != origin.ConversationID || attachmentDeleted(source.Attachment.State) || source.BodyRef == "" {
		return conversationError("not_found", "attachment_not_found")
	}
	if source.Attachment.Revision != origin.Revision {
		return conversationError("conflict", "revision_conflict")
	}
	if source.Attachment.SHA256 != doc.SHA256 || source.Attachment.Bytes != doc.Bytes || source.Attachment.Filename != doc.Filename || source.Attachment.ContentType != doc.ContentType {
		return conversationError("conflict", "document_import_invalid")
	}
	return nil
}

var _ persistence.KnowledgeAttachmentImportRepository = (*ConversationStore)(nil)
