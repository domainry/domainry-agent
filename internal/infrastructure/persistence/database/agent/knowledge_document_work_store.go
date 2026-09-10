package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func documentLeaseAuthority(l persistence.KnowledgeDocumentLease) agentsdk.ConversationAuthority {
	return agentsdk.ConversationAuthority{Known: true, RuntimeID: l.RuntimeID, WorkspaceID: l.WorkspaceID, UserID: "_worker"}
}
func (s *ConversationStore) queueDocumentWork(ctx context.Context, tx *sql.Tx, r persistence.KnowledgeDocumentRecord) error {
	l := persistence.KnowledgeDocumentLease{RuntimeID: r.Actor.RuntimeID, WorkspaceID: r.Actor.WorkspaceID, LibraryID: r.Document.LibraryID, DocumentID: r.Document.ID}
	b := query.NewInsertBuilder(s.store.Renderer(), knowledgeDocumentJobTable).Columns("scope_key", "document_id", "runtime_id", "not_before", "lease_until", "fence", "payload_json").Values(libraryScope(r.Actor), r.Document.ID, r.Actor.RuntimeID, 0, 0, 0, conversationJSON(l))
	// Revocation wakes work but never resets an in-flight lease or its fence.
	b, e := s.store.Profile().ApplyUpsert(b, []string{"scope_key", "document_id"}, query.AssignExpression("not_before", query.InsertedValue("not_before")))
	if e != nil {
		return e
	}
	q, args, e := b.Build()
	return conversationExec(ctx, tx, q, args, e)
}
func (s *ConversationStore) ClaimKnowledgeDocumentWork(ctx context.Context, runtime, owner string, now time.Time, ttl time.Duration) (out persistence.KnowledgeDocumentLease, found bool, err error) {
	if !executionText(runtime, 255, true) || !personalMemoryKey(owner) || ttl < 100*time.Millisecond || ttl > 5*time.Minute {
		return out, false, conversationError("bad_request", "document_lease_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out = persistence.KnowledgeDocumentLease{}
		found = false
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), knowledgeDocumentJobTable).Columns("payload_json").Where(query.And(query.Equal("runtime_id", runtime), query.LessThanOrEqual("not_before", now.UnixMilli()), query.LessThanOrEqual("lease_until", now.UnixMilli()))).OrderBy(query.Ascending("not_before"), query.Ascending("scope_key"), query.Ascending("document_id")).Limit(1).Build()
		if e != nil {
			return e
		}
		var raw []byte
		e = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &out); e != nil {
			return e
		}
		if out.RuntimeID != runtime || !validKnowledgeDocumentID(out.DocumentID) || !validLibraryID(out.LibraryID) || conversationAuthority(documentLeaseAuthority(out)) != nil {
			return conversationError("unavailable", "document_lease_invalid")
		}
		prior := out.Token
		out.Token++
		out.Owner = owner
		out.ExpiresAt = now.UTC().Add(ttl)
		q, args, e = query.NewUpdateBuilder(s.store.Renderer(), knowledgeDocumentJobTable).Set("fence", out.Token).Set("lease_until", out.ExpiresAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(documentScope(documentLeaseAuthority(out), out.DocumentID), query.Equal("fence", prior), query.LessThanOrEqual("lease_until", now.UnixMilli()))).Build()
		if e = conversationCAS(ctx, tx, q, args, e); e != nil {
			return e
		}
		found = true
		return nil
	})
	return
}
func (s *ConversationStore) documentWork(ctx context.Context, db conversationDB, l persistence.KnowledgeDocumentLease) (out persistence.KnowledgeDocumentRecord, err error) {
	if l.Token < 1 || !personalMemoryKey(l.Owner) || !validKnowledgeDocumentID(l.DocumentID) || conversationAuthority(documentLeaseAuthority(l)) != nil {
		return out, conversationError("conflict", "document_lease_lost")
	}
	var current persistence.KnowledgeDocumentLease
	found, e := s.executionRead(ctx, db, knowledgeDocumentJobTable, documentScope(documentLeaseAuthority(l), l.DocumentID), &current)
	if e != nil {
		return out, e
	}
	if !found || current.Token != l.Token || current.Owner != l.Owner || current.RuntimeID != l.RuntimeID || current.LibraryID != l.LibraryID || !current.ExpiresAt.After(time.Now().UTC()) {
		return out, conversationError("conflict", "document_lease_lost")
	}
	out, e = s.document(ctx, db, l.DocumentID, documentLeaseAuthority(l))
	if e != nil {
		return out, e
	}
	if out.Actor.RuntimeID != l.RuntimeID || out.Actor.WorkspaceID != l.WorkspaceID || out.Document.LibraryID != l.LibraryID {
		return out, conversationError("unavailable", "document_lease_invalid")
	}
	return out, nil
}
func (s *ConversationStore) KnowledgeDocumentWorkRecord(ctx context.Context, l persistence.KnowledgeDocumentLease) (persistence.KnowledgeDocumentRecord, error) {
	return s.documentWork(ctx, s.store.Database(), l)
}
func (s *ConversationStore) StartKnowledgeDocumentPut(ctx context.Context, l persistence.KnowledgeDocumentLease) (out persistence.KnowledgeDocumentRecord, started bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		started = false
		var e error
		out, e = s.documentWork(ctx, tx, l)
		if e != nil {
			return e
		}
		if out.PutStarted || out.Document.State == "deleting" || out.Document.State == "deleted" {
			return nil
		}
		library, e := s.library(ctx, tx, out.Document.LibraryID, out.Actor)
		if e != nil {
			return e
		}
		if e = documentWriting(library); e != nil {
			return e
		}
		if out.BodyRef == "" || out.Document.State != "queued" && out.Document.State != "failed" {
			return conversationError("conflict", "document_state_conflict")
		}
		out.PutStarted = true
		out.Document.State = "indexing"
		out.Document.ErrorCode = ""
		if e = s.saveDocument(ctx, tx, &out, out.Document.Revision); e != nil {
			return e
		}
		started = true
		return nil
	})
	return
}
func (s *ConversationStore) StartKnowledgeDocumentDelete(ctx context.Context, l persistence.KnowledgeDocumentLease) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		r, e := s.documentWork(ctx, tx, l)
		if e != nil {
			return e
		}
		if r.Document.State != "deleting" || !r.IndexObserved {
			return conversationError("conflict", "document_cleanup_unconfirmed")
		}
		if r.DeleteStarted {
			return nil
		}
		r.DeleteStarted = true
		return s.saveDocument(ctx, tx, &r, r.Document.Revision)
	})
}
func (s *ConversationStore) ApplyKnowledgeDocumentProgress(ctx context.Context, l persistence.KnowledgeDocumentLease, in persistence.KnowledgeDocumentProgress) error {
	if in.ErrorCode != "" && !attachmentErrorCodePattern.MatchString(in.ErrorCode) || !executionText(in.IndexStatus, 64, false) {
		return conversationError("bad_request", "document_progress_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		r, e := s.documentWork(ctx, tx, l)
		if e != nil {
			return e
		}
		before := conversationHash(r)
		done := false
		switch in.Event {
		case "put_acknowledged":
			if !r.PutStarted {
				return conversationError("conflict", "document_state_conflict")
			}
			r.PutAcknowledged = true
		case "indexed":
			if !r.PutStarted || in.IndexStatus != "INDEXED" {
				return conversationError("conflict", "document_state_conflict")
			}
			r.IndexObserved = true
			r.Document.IndexStatus = in.IndexStatus
			r.Document.ErrorCode = ""
			if r.Document.State != "deleting" {
				r.Document.State = "ready"
				done = true
			}
		case "retry":
			r.Document.IndexStatus = in.IndexStatus
			r.Document.ErrorCode = in.ErrorCode
			if r.Document.State != "deleting" && r.Document.State != "deleted" {
				if !r.PutStarted {
					r.Document.State = "failed"
				} else if !r.PutAcknowledged {
					r.Document.State = "needs_reconcile"
				} else {
					r.Document.State = "indexing"
				}
			}
		case "deleted":
			if r.Document.State != "deleting" || r.PutStarted && (!r.IndexObserved || !r.DeleteStarted) {
				return conversationError("conflict", "document_cleanup_unconfirmed")
			}
			r.Document.State = "deleted"
			r.Document.ErrorCode = ""
			r.BodyRef = ""
			done = true
		default:
			return conversationError("bad_request", "document_progress_invalid")
		}
		if before != conversationHash(r) {
			if e = s.saveDocument(ctx, tx, &r, r.Document.Revision); e != nil {
				return e
			}
		}
		predicate := query.And(documentScope(documentLeaseAuthority(l), l.DocumentID), query.Equal("fence", l.Token))
		if done {
			q, args, e := query.NewDeleteBuilder(s.store.Renderer(), knowledgeDocumentJobTable).Where(predicate).Build()
			return conversationExec(ctx, tx, q, args, e)
		}
		l.Owner = ""
		l.ExpiresAt = time.Time{}
		q, args, e := query.NewUpdateBuilder(s.store.Renderer(), knowledgeDocumentJobTable).Set("lease_until", 0).Set("not_before", in.RetryAt.UnixMilli()).Set("payload_json", conversationJSON(l)).Where(predicate).Build()
		return conversationCAS(ctx, tx, q, args, e)
	})
}
