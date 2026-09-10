package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func (s *ConversationService) knowledgeDocumentWorker(ctx context.Context, repo persistence.KnowledgeDocumentRepository) {
	defer s.wg.Done()
	for ctx.Err() == nil {
		lease, ok, err := repo.ClaimKnowledgeDocumentWork(ctx, s.runtimeID, s.owner, time.Now().UTC(), time.Minute)
		if err == nil && ok {
			attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
			s.processKnowledgeDocument(attempt, repo, lease)
			cancel()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.documentWake:
		case <-time.After(s.options.DocumentPoll):
		}
	}
}
func (s *ConversationService) processKnowledgeDocument(ctx context.Context, repo persistence.KnowledgeDocumentRepository, lease persistence.KnowledgeDocumentLease) {
	progress := persistence.KnowledgeDocumentProgress{Event: "retry", ErrorCode: "document_work_unavailable", RetryAt: time.Now().UTC().Add(30 * time.Second)}
	defer func() {
		// A lost response does not release safety state. Persist only through the
		// still-current fenced lease; never let late completion revive deletion.
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = repo.ApplyKnowledgeDocumentProgress(finish, lease, progress)
	}()
	r, err := repo.KnowledgeDocumentWorkRecord(ctx, lease)
	if err != nil {
		return
	}
	deleting := r.Document.State == "deleting"
	if deleting && !r.PutStarted {
		if s.options.DocumentStorage.DeleteKnowledgeDocumentContent(ctx, documentStorageScope(r.Document.LibraryID, r.Actor), r.Document.ID) != nil {
			progress.ErrorCode = "document_cleanup_failed"
			return
		}
		progress.Event = "deleted"
		return
	}
	source, err := s.documentBinding(r.Document.LibraryID, r.Actor)
	if err != nil || source.KnowledgeDocumentSourceIdentity() != r.SourceID {
		progress.ErrorCode = "document_management_unavailable"
		return
	}
	if !r.PutStarted {
		if _, err = s.documentAccess(ctx, r.Document.LibraryID, "documents_upload", r.Actor); err != nil {
			progress.ErrorCode = "document_upload_access_denied"
			return
		}
		raw, err := s.options.DocumentStorage.ReadKnowledgeDocumentContent(ctx, documentStorageScope(r.Document.LibraryID, r.Actor), r.Document.ID, r.BodyRef)
		if err != nil || int64(len(raw)) != r.Document.Bytes || artifact.Hash(raw) != r.Document.SHA256 {
			progress.ErrorCode = "document_content_mismatch"
			return
		}
		// Never overwrite a preexisting remote document, even with our exclusive
		// deterministic ID. The provider currently promises no idempotent writes.
		state, err := source.InspectKnowledgeDocument(ctx, r.RemoteID, r.Actor)
		if err != nil {
			progress.ErrorCode = "document_inspect_failed"
			return
		}
		if state.DocumentID != r.RemoteID || state.Exists {
			progress.ErrorCode = "document_remote_conflict"
			return
		}
		if _, err = s.documentAccess(ctx, r.Document.LibraryID, "documents_upload", r.Actor); err != nil {
			progress.ErrorCode = "document_upload_access_denied"
			return
		}
		startedRecord, started, err := repo.StartKnowledgeDocumentPut(ctx, lease)
		if err != nil || !started {
			return
		}
		if err = source.PutKnowledgeDocument(ctx, agentsdk.KnowledgeDocumentContent{DocumentID: startedRecord.RemoteID, Filename: startedRecord.Document.Filename, Data: raw}, r.Actor); err != nil {
			progress.ErrorCode = "document_put_uncertain"
			return
		}
		progress = persistence.KnowledgeDocumentProgress{Event: "put_acknowledged", RetryAt: time.Now().UTC().Add(s.options.DocumentPoll)}
		return
	}
	state, err := source.InspectKnowledgeDocument(ctx, r.RemoteID, r.Actor)
	if err != nil || state.DocumentID != r.RemoteID {
		progress.ErrorCode = "document_inspect_failed"
		return
	}
	progress.IndexStatus = state.IndexStatus
	progress.RetryAt = time.Now().UTC().Add(s.options.DocumentPoll)
	progress.ErrorCode = ""
	if deleting && r.DeleteStarted {
		if state.Exists {
			progress.ErrorCode = "document_delete_uncertain"
			progress.RetryAt = time.Now().UTC().Add(30 * time.Second)
			return
		}
		if s.options.DocumentStorage.DeleteKnowledgeDocumentContent(ctx, documentStorageScope(r.Document.LibraryID, r.Actor), r.Document.ID) != nil {
			progress.ErrorCode = "document_cleanup_failed"
			return
		}
		progress.Event = "deleted"
		return
	}
	if !state.Exists {
		progress.ErrorCode = "document_put_unconfirmed"
		progress.RetryAt = time.Now().UTC().Add(30 * time.Second)
		return
	}
	if !r.IndexObserved && state.IndexStatus == "INDEXED" {
		progress.Event = "indexed"
		return
	}
	if deleting && r.IndexObserved {
		if err = repo.StartKnowledgeDocumentDelete(ctx, lease); err != nil {
			return
		}
		if err = source.DeleteKnowledgeDocument(ctx, r.RemoteID, r.Actor); err != nil {
			progress.ErrorCode = "document_delete_uncertain"
		}
		return // Next attempt verifies absence; never repeat an uncertain DELETE.
	}
	if state.IndexStatus == "FAILED" || state.IndexStatus == "ERROR" {
		progress.ErrorCode = "document_index_failed"
		progress.RetryAt = time.Now().UTC().Add(30 * time.Second)
	}
}
