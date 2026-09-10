package agent

import (
	"context"
	"database/sql"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func validKnowledgeDocumentOrigin(o persistence.KnowledgeDocumentOrigin) bool {
	return validLibraryID(o.LibraryID) && validKnowledgeDocumentID(o.DocumentID) && o.Revision > 0 && (o.Mode == "copy" || o.Mode == "move")
}
func (s *ConversationStore) FindKnowledgeDocumentTransfer(ctx context.Context, library, client string, origin persistence.KnowledgeDocumentOrigin, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, found bool, err error) {
	if conversationAuthority(a) != nil || !validLibraryID(library) || !personalMemoryKey(client) || !validKnowledgeDocumentOrigin(origin) || library == origin.LibraryID {
		return out, false, conversationError("bad_request", "document_transfer_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, e := s.library(ctx, tx, library, a); e != nil {
			return e
		}
		var e error
		found, e = s.executionRead(ctx, tx, knowledgeDocumentTable, documentScope(a, knowledgeDocumentReservationID(library, client, "knowledge_document_transfer.v1", a)), &out)
		if e != nil {
			return e
		}
		if found && (out.DocumentOrigin == nil || *out.DocumentOrigin != origin) {
			return conversationError("conflict", "idempotency_conflict")
		}
		return nil
	})
	return
}

func (s *ConversationStore) checkKnowledgeDocumentOrigin(ctx context.Context, tx *sql.Tx, origin *persistence.KnowledgeDocumentOrigin, target agentsdk.KnowledgeDocument, a agentsdk.ConversationAuthority) (source persistence.KnowledgeDocumentRecord, err error) {
	if origin == nil {
		return source, nil
	}
	if !validKnowledgeDocumentOrigin(*origin) || origin.LibraryID == target.LibraryID {
		return source, conversationError("bad_request", "document_transfer_invalid")
	}
	library, err := s.library(ctx, tx, origin.LibraryID, a)
	if err != nil {
		return source, err
	}
	if library.Archived {
		return source, conversationError("conflict", "library_archived")
	}
	if origin.Mode == "move" {
		if err = documentWriting(library); err != nil {
			return source, err
		}
	}
	source, err = s.document(ctx, tx, origin.DocumentID, a)
	if err != nil {
		return source, err
	}
	if source.Document.LibraryID != origin.LibraryID || source.BodyRef == "" || source.Document.State == "deleting" || source.Document.State == "deleted" {
		return source, conversationError("not_found", "document_not_found")
	}
	if source.Document.Revision != origin.Revision {
		return source, conversationError("conflict", "revision_conflict")
	}
	if source.Document.SHA256 != target.SHA256 || source.Document.Bytes != target.Bytes || source.Document.Filename != target.Filename || source.Document.ContentType != target.ContentType {
		return source, conversationError("conflict", "document_transfer_invalid")
	}
	return source, nil
}

func (s *ConversationStore) retireKnowledgeDocument(ctx context.Context, tx *sql.Tx, record *persistence.KnowledgeDocumentRecord, expected int64) error {
	if record.Document.State == "deleting" {
		return s.queueDocumentWork(ctx, tx, *record)
	}
	if record.Document.State == "deleted" {
		return nil
	}
	record.Document.State = "deleting"
	record.Document.ErrorCode = ""
	if err := s.saveDocument(ctx, tx, record, expected); err != nil {
		return err
	}
	return s.queueDocumentWork(ctx, tx, *record)
}

var _ persistence.KnowledgeDocumentTransferRepository = (*ConversationStore)(nil)
