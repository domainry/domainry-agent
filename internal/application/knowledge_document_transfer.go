package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) TransferKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentTransfer, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero agentsdk.KnowledgeDocument
	if !conversationKey(in.ClientID) || !conversationKey(in.SourceLibraryID) || !conversationKey(in.SourceDocumentID) || library == in.SourceLibraryID || in.ExpectedRevision < 1 || in.Mode != "copy" && in.Mode != "move" {
		return zero, conversationFailure("bad_request", "document_transfer_invalid")
	}
	if _, err := s.documentAccess(ctx, library, "documents_transfer", a); err != nil {
		return zero, err
	}
	if _, err := s.documentAccess(ctx, library, "documents_upload", a); err != nil {
		return zero, err
	}
	repo, ok := s.repo.(persistence.KnowledgeDocumentTransferRepository)
	if !ok {
		return zero, conversationFailure("unavailable", "document_transfer_unavailable")
	}
	origin := persistence.KnowledgeDocumentOrigin{LibraryID: in.SourceLibraryID, DocumentID: in.SourceDocumentID, Revision: in.ExpectedRevision, Mode: in.Mode}
	prior, found, err := repo.FindKnowledgeDocumentTransfer(ctx, library, in.ClientID, origin, a)
	if err != nil {
		return zero, err
	}
	if found {
		if prior.Document.State == "deleting" || prior.Document.State == "deleted" {
			return zero, conversationFailure("not_found", "document_not_found")
		}
		// Source revocation/deletion is expected after a committed move. Recovery
		// returns the original target without touching the source a second time.
		if prior.BodyRef != "" {
			return prior.Document, nil
		}
	}
	if _, err = s.documentBinding(library, a); err != nil {
		return zero, err
	}
	recheck := func() error {
		if _, e := s.documentAccess(ctx, in.SourceLibraryID, "documents_download", a); e != nil {
			return e
		}
		if in.Mode == "move" {
			if _, e := s.documentAccess(ctx, in.SourceLibraryID, "documents_delete", a); e != nil {
				return e
			}
		}
		_, e := s.documentAccess(ctx, library, "documents_transfer", a)
		return e
	}
	if err = recheck(); err != nil {
		return zero, err
	}
	source, err := s.DownloadKnowledgeDocument(ctx, in.SourceLibraryID, in.SourceDocumentID, a)
	if err != nil {
		return zero, err
	}
	if source.Document.Revision != in.ExpectedRevision {
		return zero, conversationFailure("conflict", "revision_conflict")
	}
	return s.uploadKnowledgeDocument(ctx, library, agentsdk.KnowledgeDocumentUpload{ClientID: in.ClientID, Filename: source.Document.Filename, Data: source.Data}, nil, &origin, recheck, a)
}

var _ agentsdk.KnowledgeDocumentTransferService = (*ConversationService)(nil)
