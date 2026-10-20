package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
	"time"
)

func (s *ConversationStore) RequestAttachmentIndexCheck(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (out persistence.ConversationAttachmentRecord, err error) {
	if conversationAuthority(a) != nil || expected < 1 {
		return out, conversationError("bad_request", "attachment_index_request_invalid")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		parent, e := s.get(ctx, tx, conversation, a)
		if e != nil {
			return e
		}
		out, e = s.attachmentRecord(ctx, tx, id, a)
		if e != nil {
			return e
		}
		if out.Attachment.ConversationID != conversation {
			return conversationError("not_found", "attachment_not_found")
		}
		if out.Index == nil || out.Source == nil {
			return conversationError("conflict", "attachment_index_not_requested")
		}
		if out.Attachment.State == "deleted" {
			return nil
		}
		if parent.Archived && out.Attachment.State != "deleting" {
			return conversationError("conflict", "attachment_conversation_archived")
		}
		if out.Index.LastCheckRevision == expected {
			return nil
		}
		if out.Attachment.Revision != expected {
			return conversationError("conflict", "revision_conflict")
		}
		var lease persistence.ConversationAttachmentIndexLease
		found, e := s.payloadRead(ctx, tx, attachmentIndexJobTable, attachmentScope(a, id), &lease)
		if e != nil {
			return e
		}
		if !found || lease.ConversationID != conversation || lease.AttachmentID != id || conversationOwner(lease.Authority) != conversationOwner(a) {
			return conversationError("unavailable", "attachment_index_lease_invalid")
		}
		out.Index.LastCheckRevision = expected
		if !lease.ExpiresAt.After(time.Now().UTC()) {
			// Refresh the authenticated role only after the old lease expires.
			// An active attempt retains its identity and every fencing check.
			out.Index.Actor = a
			lease.Authority = a
			q, args, e := query.NewUpdateBuilder(s.store.Renderer(), attachmentIndexJobTable).Set("not_before", 0).Set("payload_json", conversationJSON(lease)).Where(query.And(attachmentScope(a, id), query.Equal("fence", lease.Token))).Build()
			if e = conversationCAS(ctx, tx, q, args, e); e != nil {
				return e
			}
		}
		return s.saveAttachmentIndex(ctx, tx, &out, a)
	})
	return
}

var _ persistence.ConversationAttachmentIndexCheckRepository = (*ConversationStore)(nil)
