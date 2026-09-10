package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func documentStorageScope(library string, a agentsdk.ConversationAuthority) agentsdk.KnowledgeDocumentStorageScope {
	return agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: library}
}
func (s *ConversationService) documentAccess(ctx context.Context, library, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRepository, error) {
	libs, err := s.libraryAccess(ctx, op, a)
	if err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.KnowledgeDocumentRepository)
	if !ok || s.options.DocumentStorage == nil {
		return nil, conversationFailure("unavailable", "documents_unavailable")
	}
	item, err := libs.KnowledgeLibrary(ctx, library, a)
	if err != nil {
		return nil, err
	}
	if item.Archived && op != "documents_delete" && op != "documents_list" && op != "documents_get" {
		return nil, conversationFailure("conflict", "library_archived")
	}
	if (op == "documents_upload" || op == "documents_delete" || op == "documents_import_attachment" || op == "documents_transfer") && item.Role != "editor" && item.Role != "manager" {
		return nil, conversationFailure("forbidden", "document_write_denied")
	}
	if err = s.libraryAuthorize(ctx, op, item, a); err != nil {
		return nil, err
	}
	return repo, nil
}
func (s *ConversationService) documentBinding(library string, a agentsdk.ConversationAuthority) (agentsdk.ManagedKnowledgeDocumentSource, error) {
	for _, b := range s.options.LibraryKnowledge {
		if b.LibraryID != library || b.WorkspaceID != a.WorkspaceID || a.RuntimeID != s.runtimeID || !b.ManageDocuments {
			continue
		}
		source, ok := b.Source.(agentsdk.ManagedKnowledgeDocumentSource)
		if !ok || source.KnowledgeDocumentManagementReady() != nil {
			break
		}
		return source, nil
	}
	return nil, conversationFailure("unavailable", "document_management_unavailable")
}
func (s *ConversationService) UploadKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.uploadKnowledgeDocument(ctx, library, in, nil, nil, nil, a)
}
func (s *ConversationService) uploadKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, origin *persistence.KnowledgeAttachmentOrigin, documentOrigin *persistence.KnowledgeDocumentOrigin, recheck func() error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero agentsdk.KnowledgeDocument
	repo, err := s.documentAccess(ctx, library, "documents_upload", a)
	if err != nil {
		return zero, err
	}
	source, err := s.documentBinding(library, a)
	if err != nil {
		return zero, err
	}
	if len(in.Data) == 0 || len(in.Data) > 16<<20 {
		return zero, conversationFailure("bad_request", "document_size_invalid")
	}
	contentType := attachmentContentType(in.Filename)
	if contentType == "" {
		return zero, conversationFailure("bad_request", "document_type_unsupported")
	}
	hash := artifact.Hash(in.Data)
	r, err := repo.ReserveKnowledgeDocument(ctx, persistence.KnowledgeDocumentReserve{AttachmentOrigin: origin, DocumentOrigin: documentOrigin, LibraryID: library, ClientID: in.ClientID, Filename: in.Filename, ContentType: contentType, Bytes: int64(len(in.Data)), SHA256: hash, SourceID: source.KnowledgeDocumentSourceIdentity()}, a)
	if err != nil {
		return zero, err
	}
	if r.Document.State == "deleting" || r.Document.State == "deleted" {
		return zero, conversationFailure("not_found", "document_not_found")
	}
	if r.BodyRef != "" {
		return r.Document, nil
	}
	ref, err := s.options.DocumentStorage.PutKnowledgeDocumentContent(ctx, documentStorageScope(library, a), r.Document.ID, hash, in.Data)
	if err != nil {
		return zero, err
	}
	// Losing membership during upload leaves an inaccessible uploading record;
	// a current library editor can delete it. Never publish on stale authority.
	if _, err = s.documentAccess(ctx, library, "documents_upload", a); err != nil {
		return zero, err
	}
	if recheck != nil {
		if err = recheck(); err != nil {
			return zero, err
		}
	}
	r, err = repo.CommitKnowledgeDocumentContent(ctx, r.Document.ID, r.Document.Revision, ref, a)
	if err != nil {
		return zero, err
	}
	s.wakeKnowledgeDocuments()
	return r.Document, nil
}
func (s *ConversationService) knowledgeDocumentRecord(ctx context.Context, repo persistence.KnowledgeDocumentRepository, library, id string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRecord, error) {
	r, err := repo.KnowledgeDocumentRecord(ctx, id, a)
	if err == nil && (r.Document.LibraryID != library || r.Document.State == "deleted") {
		err = conversationFailure("not_found", "document_not_found")
	}
	return r, err
}
func (s *ConversationService) KnowledgeDocuments(ctx context.Context, library, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentPage, error) {
	repo, err := s.documentAccess(ctx, library, "documents_list", a)
	if err != nil {
		return agentsdk.KnowledgeDocumentPage{}, err
	}
	out, err := repo.KnowledgeDocuments(ctx, library, after, limit, a)
	if err == nil {
		_, err = s.documentAccess(ctx, library, "documents_list", a)
	}
	if err != nil {
		return agentsdk.KnowledgeDocumentPage{}, err
	}
	return out, nil
}
func (s *ConversationService) KnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	repo, err := s.documentAccess(ctx, library, "documents_get", a)
	if err != nil {
		return agentsdk.KnowledgeDocument{}, err
	}
	r, err := s.knowledgeDocumentRecord(ctx, repo, library, id, a)
	if err != nil {
		return agentsdk.KnowledgeDocument{}, err
	}
	return r.Document, nil
}
func (s *ConversationService) DownloadKnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	var zero agentsdk.KnowledgeDocumentDownload
	repo, err := s.documentAccess(ctx, library, "documents_download", a)
	if err != nil {
		return zero, err
	}
	r, err := s.knowledgeDocumentRecord(ctx, repo, library, id, a)
	if err != nil {
		return zero, err
	}
	if r.BodyRef == "" || r.Document.State == "deleting" {
		return zero, conversationFailure("not_found", "document_content_not_found")
	}
	raw, err := s.options.DocumentStorage.ReadKnowledgeDocumentContent(ctx, documentStorageScope(library, a), id, r.BodyRef)
	if err != nil {
		return zero, err
	}
	if int64(len(raw)) != r.Document.Bytes || artifact.Hash(raw) != r.Document.SHA256 {
		return zero, conversationFailure("unavailable", "document_content_mismatch")
	}
	if _, err = s.documentAccess(ctx, library, "documents_download", a); err != nil {
		return zero, err
	}
	current, err := s.knowledgeDocumentRecord(ctx, repo, library, id, a)
	if err != nil {
		return zero, err
	}
	if current.BodyRef != r.BodyRef || current.Document.State == "deleting" {
		return zero, conversationFailure("not_found", "document_content_not_found")
	}
	return agentsdk.KnowledgeDocumentDownload{Document: current.Document, Data: raw}, nil
}
func (s *ConversationService) DeleteKnowledgeDocument(ctx context.Context, library, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero agentsdk.KnowledgeDocument
	repo, err := s.documentAccess(ctx, library, "documents_delete", a)
	if err != nil {
		return zero, err
	}
	if expected < 1 {
		return zero, conversationFailure("bad_request", "revision_required")
	}
	r, err := repo.KnowledgeDocumentRecord(ctx, id, a)
	if err != nil {
		return zero, err
	}
	if r.Document.LibraryID != library {
		return zero, conversationFailure("not_found", "document_not_found")
	}
	r, err = repo.RequestKnowledgeDocumentDeletion(ctx, id, expected, a)
	if err != nil {
		return zero, err
	}
	s.wakeKnowledgeDocuments()
	return r.Document, nil
}
func (s *ConversationService) wakeKnowledgeDocuments() {
	select {
	case s.documentWake <- struct{}{}:
	default:
	}
}

func activateDocumentSources(repo persistence.ConversationRepository, runtime string, options ConversationOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, b := range options.LibraryKnowledge {
		if !b.ManageDocuments {
			continue
		}
		documents, ok := repo.(persistence.KnowledgeDocumentRepository)
		if !ok || options.DocumentStorage == nil {
			return conversationFailure("unavailable", "documents_unavailable")
		}
		if err := documents.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: runtime, WorkspaceID: b.WorkspaceID, LibraryID: b.LibraryID}, b.Source.(agentsdk.ManagedKnowledgeDocumentSource).KnowledgeDocumentSourceIdentity()); err != nil {
			return err
		}
	}
	return nil
}

var _ agentsdk.KnowledgeDocumentService = (*ConversationService)(nil)
