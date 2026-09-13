package application

import (
	"context"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

// Sharing is a versioned reference, never an implicit library membership or an
// attachment scope change. Knowledge remains the sole data authority.
func (s *ConversationService) checkSharedDocuments(ctx context.Context, refs []sdk.ConversationDocumentReference, a sdk.ConversationAuthority) error {
	if !sdk.ValidConversationDocumentReferences(refs) {
		return conversationFailure("bad_request", "shared_document_invalid")
	}
	if len(refs) == 0 {
		return nil
	}
	ctx, cancel := s.externalCallContext(ctx, 5*time.Second)
	defer cancel()
	owner := s.knowledgeService()
	for _, ref := range refs {
		if _, err := owner.DocumentAccess(ctx, ref.LibraryID, "documents_download", a); err != nil {
			return err
		}
		doc, err := owner.KnowledgeDocument(ctx, ref.LibraryID, ref.DocumentID, a)
		if err != nil {
			return err
		}
		if doc.ID != ref.DocumentID || doc.LibraryID != ref.LibraryID || doc.Revision != ref.Revision || doc.SHA256 != ref.SHA256 || doc.State != "ready" {
			return conversationFailure("conflict", "shared_document_changed")
		}
	}
	return ctx.Err()
}
