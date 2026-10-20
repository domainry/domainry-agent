package agent

import (
	"context"
	"database/sql"
	"math"
	"slices"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) saveAttachmentIndex(ctx context.Context, tx *sql.Tx, r *persistence.ConversationAttachmentRecord, a agentsdk.ConversationAuthority) error {
	previous := r.Attachment.Revision
	r.Attachment.Revision++
	r.Attachment.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	return s.saveAttachmentRecord(ctx, tx, *r, previous, a)
}

func (s *ConversationStore) queueAttachmentIndexWork(ctx context.Context, tx *sql.Tx, r persistence.ConversationAttachmentRecord, a agentsdk.ConversationAuthority) error {
	if r.Attachment.State != "deleting" && (r.Index == nil || r.Source == nil) {
		return nil
	}
	if r.Index != nil {
		a = r.Index.Actor
	}
	// Cleanup jobs retain only the stable owner namespace. An index job keeps
	// its original actor because the remote private ACL is rechecked with it.
	if r.Index == nil {
		a = agentsdk.ConversationAuthority{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID}
	}
	l := persistence.ConversationAttachmentIndexLease{Authority: a, ConversationID: r.Attachment.ConversationID, AttachmentID: r.Attachment.ID}
	b := query.NewInsertBuilder(s.store.Renderer(), attachmentIndexJobTable).Columns("owner_key", "attachment_id", "runtime_id", "not_before", "lease_until", "fence", "payload_json").Values(conversationOwner(l.Authority), l.AttachmentID, l.Authority.RuntimeID, 0, 0, 0, conversationJSON(l))
	// Deletion wakes existing work without invalidating an in-flight write's fence.
	b, err := s.store.Profile().ApplyUpsert(b, []string{"owner_key", "attachment_id"}, query.AssignExpression("not_before", query.InsertedValue("not_before")))
	if err != nil {
		return err
	}
	q, args, err := b.Build()
	return conversationExec(ctx, tx, q, args, err)
}

func (s *ConversationStore) QueueAttachmentIndex(ctx context.Context, id string, expected int64, source persistence.ConversationAttachmentSource, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	if conversationAuthority(a) != nil || expected < 1 || !artifactSHA(source.Identity) || !artifactSHA(source.AccessPolicySHA256) || !validAttachmentDocumentPermissions(source.DocumentPermissionIDs) || source.DocID != "" || source.RequestID != "" {
		return out, conversationError("bad_request", "attachment_source_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = s.attachmentRecord(ctx, tx, id, a)
		if e != nil {
			return e
		}
		c, e := s.get(ctx, tx, out.Attachment.ConversationID, a)
		if e != nil {
			return e
		}
		if c.Archived {
			return conversationError("conflict", "attachment_conversation_archived")
		}
		owner, e := s.attachmentKnowledgeSourceOwner(ctx, tx, source.Identity)
		if e != nil {
			return e
		}
		if owner != attachmentKnowledgeScope(a) {
			return conversationError("conflict", "attachment_source_not_bound")
		}
		if attachmentDeleted(out.Attachment.State) {
			return conversationError("not_found", "attachment_not_found")
		}
		if out.Index != nil && out.Source != nil {
			if out.Source.Identity != source.Identity || !slices.Equal(out.Source.DocumentPermissionIDs, source.DocumentPermissionIDs) || out.Source.AccessPolicySHA256 != source.AccessPolicySHA256 {
				return conversationError("conflict", "attachment_source_changed")
			}
			return nil // An old response can be recovered; no write or lease is reset.
		}
		if out.Attachment.Revision != expected {
			return conversationError("conflict", "revision_conflict")
		}
		if out.Attachment.State != "stored" || out.Source != nil {
			return conversationError("conflict", "attachment_not_ready")
		}
		frozen := source
		frozen.DocID = "dka_" + conversationHash([]string{"private_attachment.v1", source.Identity, conversationOwner(a), out.Attachment.ConversationID, id, out.Attachment.SHA256})
		frozen.DocumentPermissionIDs = slices.Clone(source.DocumentPermissionIDs)
		frozen.RequestID = "aput_" + conversationHash([]string{frozen.DocID, out.RequestSHA256, strings.Join(source.DocumentPermissionIDs, "\x00"), source.AccessPolicySHA256})
		out.Source = &frozen
		out.Index = &persistence.ConversationAttachmentIndex{Actor: a}
		out.Attachment.State, out.Attachment.IndexStatus, out.Attachment.ErrorCode = "indexing", "QUEUED", ""
		if e = s.saveAttachmentIndex(ctx, tx, &out, a); e != nil {
			return e
		}
		return s.queueAttachmentIndexWork(ctx, tx, out, a)
	})
	return
}

func validAttachmentDocumentPermissions(ids []string) bool {
	if len(ids) != 1 || !slices.IsSorted(ids) {
		return false
	}
	for i, id := range ids {
		if !executionText(id, 128, true) || i > 0 && ids[i-1] == id {
			return false
		}
	}
	return true
}

func (s *ConversationStore) ClaimAttachmentIndexWork(ctx context.Context, runtime, owner string, now time.Time, ttl time.Duration) (out persistence.ConversationAttachmentIndexLease, found bool, err error) {
	if !executionText(runtime, 255, true) || !personalMemoryKey(owner) || ttl < 100*time.Millisecond || ttl > 5*time.Minute {
		return out, false, conversationError("bad_request", "attachment_index_lease_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		out = persistence.ConversationAttachmentIndexLease{}
		found = false
		q, args, e := query.NewSelectBuilder(s.store.Renderer(), attachmentIndexJobTable).Columns("payload_json").Where(query.And(query.Equal("runtime_id", runtime), query.LessThanOrEqual("not_before", now.UnixMilli()), query.LessThanOrEqual("lease_until", now.UnixMilli()))).OrderBy(query.Ascending("not_before"), query.Ascending("owner_key"), query.Ascending("attachment_id")).Limit(1).Build()
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
		if e = unmarshalDurableJSON(raw, &out); e != nil {
			return e
		}
		if out.Authority.RuntimeID != runtime || conversationAuthority(out.Authority) != nil || !personalMemoryKey(out.AttachmentID) || !personalMemoryKey(out.ConversationID) {
			return conversationError("unavailable", "attachment_index_lease_invalid")
		}
		prior := out.Token
		out.Token++
		out.Owner = owner
		out.ExpiresAt = now.UTC().Add(ttl).Truncate(time.Millisecond)
		q, args, e = query.NewUpdateBuilder(s.store.Renderer(), attachmentIndexJobTable).Set("fence", out.Token).Set("lease_until", out.ExpiresAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(attachmentScope(out.Authority, out.AttachmentID), query.Equal("fence", prior), query.LessThanOrEqual("lease_until", now.UnixMilli()))).Build()
		if e = conversationCAS(ctx, tx, q, args, e); e != nil {
			return e
		}
		found = true
		return nil
	})
	return
}

func (s *ConversationStore) attachmentIndexWork(ctx context.Context, db conversationDB, lease persistence.ConversationAttachmentIndexLease) (r persistence.ConversationAttachmentRecord, err error) {
	if conversationAuthority(lease.Authority) != nil || lease.Token < 1 || !personalMemoryKey(lease.Owner) {
		return r, conversationError("conflict", "attachment_index_lease_lost")
	}
	var current persistence.ConversationAttachmentIndexLease
	found, err := s.payloadRead(ctx, db, attachmentIndexJobTable, attachmentScope(lease.Authority, lease.AttachmentID), &current)
	if err != nil {
		return r, err
	}
	if !found || current.Token != lease.Token || current.Owner != lease.Owner || current.Authority != lease.Authority || current.ConversationID != lease.ConversationID || current.AttachmentID != lease.AttachmentID || !current.ExpiresAt.Equal(lease.ExpiresAt) || !current.ExpiresAt.After(time.Now().UTC()) {
		return r, conversationError("conflict", "attachment_index_lease_lost")
	}
	r, err = s.attachmentRecord(ctx, db, lease.AttachmentID, lease.Authority)
	if err != nil {
		return r, err
	}
	if r.Attachment.ConversationID != lease.ConversationID {
		return r, conversationError("unavailable", "attachment_index_lease_invalid")
	}
	if r.Index == nil || r.Source == nil {
		if r.Attachment.State != "deleting" || r.Index != nil || r.Source != nil {
			return r, conversationError("unavailable", "attachment_index_lease_invalid")
		}
		return r, nil
	}
	if r.Source.RequestID == "" || r.Index.Actor != lease.Authority {
		return r, conversationError("unavailable", "attachment_index_lease_invalid")
	}
	return r, nil
}
func (s *ConversationStore) AttachmentIndexWorkRecord(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) (persistence.ConversationAttachmentRecord, error) {
	return s.attachmentIndexWork(ctx, s.store.Database(), lease)
}

func (s *ConversationStore) StartAttachmentIndexPut(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) (out persistence.ConversationAttachmentRecord, started bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		started = false
		var e error
		out, e = s.attachmentIndexWork(ctx, tx, lease)
		if e != nil {
			return e
		}
		if out.Index.PutStarted || attachmentDeleted(out.Attachment.State) {
			return nil
		}
		c, e := s.get(ctx, tx, lease.ConversationID, lease.Authority)
		if e != nil {
			return e
		}
		if c.Archived {
			return conversationError("conflict", "attachment_conversation_archived")
		}
		if out.Attachment.State != "indexing" && out.Attachment.State != "failed" {
			return conversationError("conflict", "attachment_not_ready")
		}
		out.Index.PutStarted = true
		out.Attachment.State, out.Attachment.ErrorCode = "indexing", ""
		if e = s.saveAttachmentIndex(ctx, tx, &out, lease.Authority); e != nil {
			return e
		}
		started = true
		return nil
	})
	return
}

func (s *ConversationStore) StartAttachmentIndexDelete(ctx context.Context, lease persistence.ConversationAttachmentIndexLease) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		r, err := s.attachmentIndexWork(ctx, tx, lease)
		if err != nil {
			return err
		}
		if r.Attachment.State != "deleting" || !r.Index.IndexObserved || r.Index.DeleteStarted {
			return conversationError("conflict", "attachment_index_state_conflict")
		}
		r.Index.DeleteStarted = true
		return s.saveAttachmentIndex(ctx, tx, &r, lease.Authority)
	})
}

func (s *ConversationStore) ApplyAttachmentIndexProgress(ctx context.Context, lease persistence.ConversationAttachmentIndexLease, in persistence.ConversationAttachmentIndexProgress) error {
	if in.ErrorCode != "" && !attachmentErrorCodePattern.MatchString(in.ErrorCode) || !executionText(in.IndexStatus, 64, false) {
		return conversationError("bad_request", "attachment_index_progress_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		r, err := s.attachmentIndexWork(ctx, tx, lease)
		if err != nil {
			return err
		}
		done := false
		before := conversationHash(r)
		switch in.Event {
		case "put_acknowledged":
			if !r.Index.PutStarted {
				return conversationError("conflict", "attachment_index_state_conflict")
			}
			r.Index.PutAcknowledged = true
		case "indexed":
			if !r.Index.PutStarted || in.IndexStatus != "INDEXED" {
				return conversationError("conflict", "attachment_index_state_conflict")
			}
			r.Index.IndexObserved = true
			r.Attachment.IndexStatus, r.Attachment.ErrorCode = in.IndexStatus, ""
			if r.Attachment.State != "deleting" {
				r.Attachment.State = "ready"
				done = true
			}
		case "retry":
			r.Attachment.ErrorCode, r.Attachment.IndexStatus = in.ErrorCode, in.IndexStatus
			if !attachmentDeleted(r.Attachment.State) {
				r.Attachment.State = "indexing"
				if in.ErrorCode != "" {
					r.Attachment.State = "failed"
				}
				// An attempted upload without acknowledgement or observed completion
				// has an unknown outcome, including after an inspection failure.
				// Keep the durable write markers; this state never authorizes a replay.
				if r.Index.PutStarted && !r.Index.PutAcknowledged && !r.Index.IndexObserved {
					r.Attachment.State = "needs_reconcile"
				}
			}
		case "delete_acknowledged":
			if r.Attachment.State != "deleting" || !r.Index.DeleteStarted {
				return conversationError("conflict", "attachment_index_state_conflict")
			}
			r.Index.DeleteAcknowledged = true
		case "deleted":
			if r.Attachment.State != "deleting" || r.Index != nil && r.Index.PutStarted && (!r.Index.IndexObserved || !r.Index.DeleteStarted || !r.Index.DeleteAcknowledged) {
				return conversationError("conflict", "attachment_cleanup_unconfirmed")
			}
			r.Attachment.State, r.Attachment.ErrorCode = "deleted", ""
			done = true
		default:
			return conversationError("bad_request", "attachment_index_progress_invalid")
		}
		if before != conversationHash(r) {
			if err = s.saveAttachmentIndex(ctx, tx, &r, lease.Authority); err != nil {
				return err
			}
		}
		predicate := query.And(attachmentScope(lease.Authority, lease.AttachmentID), query.Equal("fence", lease.Token))
		if done && r.Attachment.State == "deleted" {
			q, args, e := query.NewDeleteBuilder(s.store.Renderer(), attachmentIndexJobTable).Where(predicate).Build()
			return conversationExec(ctx, tx, q, args, e)
		}
		lease.ExpiresAt = time.Time{}
		notBefore := in.RetryAt.UnixMilli()
		if done {
			// Keep the monotonic fence while ready. Deletion reactivates this row;
			// recreating it at token zero would accept a delayed old lease (ABA).
			notBefore = math.MaxInt64
		}
		q, args, e := query.NewUpdateBuilder(s.store.Renderer(), attachmentIndexJobTable).Set("lease_until", 0).Set("not_before", notBefore).Set("payload_json", conversationJSON(lease)).Where(predicate).Build()
		return conversationCAS(ctx, tx, q, args, e)
	})
}

var _ persistence.ConversationAttachmentIndexRepository = (*ConversationStore)(nil)
