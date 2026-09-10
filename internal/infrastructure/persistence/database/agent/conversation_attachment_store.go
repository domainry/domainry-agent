package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func attachmentScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("attachment_id", id))
}

func attachmentDeleted(state string) bool { return state == "deleting" || state == "deleted" }

// Public metadata carries stable host error codes, never raw upstream errors.
// Callers must classify failures before persisting them.
var attachmentErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,127}$`)

func (s *ConversationStore) attachment(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	var out persistence.ConversationAttachmentRecord
	if !personalMemoryKey(id) {
		return out, conversationError("bad_request", "attachment_invalid")
	}
	found, err := s.executionRead(ctx, db, attachmentTable, attachmentScope(a, id), &out)
	if err == nil && !found {
		err = conversationError("not_found", "attachment_not_found")
	}
	return out, err
}

func (s *ConversationStore) AttachmentRecord(ctx context.Context, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	if err := conversationAuthority(a); err != nil {
		return persistence.ConversationAttachmentRecord{}, err
	}
	return s.attachment(ctx, s.store.Database(), id, a)
}

func validAttachmentReserve(in persistence.ConversationAttachmentReserve) bool {
	mediaType, params, err := mime.ParseMediaType(in.ContentType)
	return personalMemoryKey(in.ClientID) && personalMemoryKey(in.ConversationID) && artifactSHA(in.SHA256) && in.Bytes > 0 && in.Bytes <= agentsdk.ConversationAttachmentMaxBytes &&
		executionText(in.Filename, 255, true) && in.Filename != "." && in.Filename != ".." && strings.TrimSpace(in.Filename) == in.Filename && !strings.ContainsAny(in.Filename, "/\\") && strings.IndexFunc(in.Filename, unicode.IsControl) < 0 &&
		err == nil && len(params) == 0 && len(in.ContentType) <= 128 && strings.Contains(mediaType, "/") && in.ContentType == mediaType
}

func (s *ConversationStore) ReserveAttachment(ctx context.Context, in persistence.ConversationAttachmentReserve, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	var out persistence.ConversationAttachmentRecord
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !validAttachmentReserve(in) {
		return out, conversationError("bad_request", "attachment_invalid")
	}
	id := "att_" + conversationHash([]string{conversationOwner(a), in.ConversationID, in.ClientID})[:32]
	digest := conversationHash(in)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.get(ctx, tx, in.ConversationID, a); err != nil {
			return err
		}
		var prior persistence.ConversationAttachmentRecord
		found, err := s.executionRead(ctx, tx, attachmentTable, attachmentScope(a, id), &prior)
		if err != nil {
			return err
		}
		if found {
			if prior.RequestSHA256 != digest {
				return conversationError("conflict", "idempotency_conflict")
			}
			out = prior
			return nil
		}
		// Include pending deletion in quotas until physical cleanup succeeds.
		// Serializable transactions cover concurrent upload reservations.
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), attachmentTable).Columns("conversation_id", "bytes", "state").Where(query.Equal("owner_key", conversationOwner(a))).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		count, conversationCount := 0, 0
		var total int64
		for rows.Next() {
			var conversation, state string
			var size int64
			if err := rows.Scan(&conversation, &size, &state); err != nil {
				rows.Close()
				return err
			}
			count++ // Tombstones also have a bounded metadata budget.
			if state != "deleted" {
				total += size
				if conversation == in.ConversationID {
					conversationCount++
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if count >= 1000 || conversationCount >= 50 || total+in.Bytes > 256<<20 {
			return conversationError("conflict", "attachment_limit")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		out = persistence.ConversationAttachmentRecord{RequestSHA256: digest, Attachment: agentsdk.ConversationAttachment{ID: id, ConversationID: in.ConversationID, Filename: in.Filename, ContentType: in.ContentType, SHA256: in.SHA256, Bytes: in.Bytes, Visibility: "conversation_private", State: "uploading", Revision: 1, CreatedAt: now, UpdatedAt: now}}
		statement, args, err = query.NewInsertBuilder(s.store.Renderer(), attachmentTable).Columns("owner_key", "attachment_id", "conversation_id", "state", "revision", "bytes", "updated_at", "payload_json").Values(conversationOwner(a), id, in.ConversationID, "uploading", 1, in.Bytes, now.UnixMilli(), conversationJSON(out)).Build()
		return conversationExec(ctx, tx, statement, args, err)
	})
	return out, err
}

func (s *ConversationStore) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	out := agentsdk.ConversationAttachmentPage{Items: []agentsdk.ConversationAttachment{}, Complete: true}
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return out, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 || after != "" && !personalMemoryKey(after) {
		return out, conversationError("bad_request", "attachment_query_invalid")
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), attachmentTable).Columns("payload_json").Where(query.And(conversationScope(a, conversationID), query.GreaterThan("attachment_id", after), query.NotEqual("state", "deleting"), query.NotEqual("state", "deleted"))).OrderBy(query.Ascending("attachment_id")).Limit(limit + 1).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(out.Items) == limit {
			out.Complete = false
			out.NextAfter = out.Items[len(out.Items)-1].ID
			break
		}
		var raw []byte
		var record persistence.ConversationAttachmentRecord
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			return out, err
		}
		out.Items = append(out.Items, record.Attachment)
	}
	return out, rows.Err()
}

func (s *ConversationStore) saveAttachment(ctx context.Context, tx *sql.Tx, record persistence.ConversationAttachmentRecord, expected int64, a agentsdk.ConversationAuthority) error {
	statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), attachmentTable).Set("state", record.Attachment.State).Set("revision", record.Attachment.Revision).Set("updated_at", record.Attachment.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(record)).Where(query.And(attachmentScope(a, record.Attachment.ID), query.Equal("revision", expected))).Build()
	return conversationCAS(ctx, tx, statement, args, err)
}

func (s *ConversationStore) TransitionAttachment(ctx context.Context, id string, expected int64, in persistence.ConversationAttachmentTransition, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	var out persistence.ConversationAttachmentRecord
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		previous, err := s.attachment(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if expected < 1 || previous.Attachment.Revision != expected {
			return conversationError("conflict", "revision_conflict")
		}
		from := previous.Attachment.State
		allowed := in.State == "deleting" && !attachmentDeleted(from) ||
			from == "uploading" && (in.State == "stored" || in.State == "failed") ||
			from == "stored" && in.State == "indexing" ||
			from == "indexing" && (in.State == "ready" || in.State == "failed") ||
			from == "failed" && (previous.BodyRef == "" && in.State == "uploading" || previous.Source == nil && previous.BodyRef != "" && in.State == "stored" || previous.Source != nil && in.State == "indexing") ||
			from == "deleting" && in.State == "deleted"
		if !allowed || in.ErrorCode != "" && !attachmentErrorCodePattern.MatchString(in.ErrorCode) || (in.State == "failed") != (in.ErrorCode != "") {
			return conversationError("conflict", "attachment_transition_invalid")
		}
		if !attachmentDeleted(in.State) {
			if _, err := s.get(ctx, tx, previous.Attachment.ConversationID, a); err != nil {
				return err
			}
		}
		if in.BodyRef != "" {
			if from != "uploading" || in.State != "stored" || previous.BodyRef != "" || !executionText(in.BodyRef, 1024, true) {
				return conversationError("bad_request", "attachment_reference_invalid")
			}
			previous.BodyRef = in.BodyRef
		}
		if in.Source != nil {
			if from != "stored" || in.State != "indexing" || previous.Source != nil || !executionText(in.Source.Identity, 256, true) || !executionText(in.Source.DocID, 256, true) || !executionText(in.Source.PermissionID, 256, true) {
				return conversationError("bad_request", "attachment_source_invalid")
			}
			source := *in.Source
			previous.Source = &source
		}
		if (in.State == "stored" || in.State == "indexing" || in.State == "ready") && previous.BodyRef == "" || (in.State == "indexing" || in.State == "ready") && previous.Source == nil {
			return conversationError("conflict", "attachment_not_ready")
		}
		previous.Attachment.State, previous.Attachment.ErrorCode = in.State, in.ErrorCode
		previous.Attachment.Revision++
		previous.Attachment.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		out = previous
		if err := s.saveAttachment(ctx, tx, out, expected, a); err != nil {
			return err
		}
		if in.State == "deleting" {
			return s.queueAttachmentCleanup(ctx, tx, id, a)
		}
		if in.State == "deleted" {
			statement, args, err := query.NewDeleteBuilder(s.store.Renderer(), attachmentCleanupTable).Where(attachmentScope(a, id)).Build()
			return conversationExec(ctx, tx, statement, args, err)
		}
		return nil
	})
	return out, err
}

// Called in the parent deletion transaction. Never discard cleanup references
// or permit a late upload/index worker to restore access after conversation deletion.
func (s *ConversationStore) deleteConversationAttachments(ctx context.Context, tx *sql.Tx, conversationID string, a agentsdk.ConversationAuthority) error {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), attachmentTable).Columns("payload_json").Where(query.And(conversationScope(a, conversationID), query.NotEqual("state", "deleted"), query.NotEqual("state", "deleting"))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	var records []persistence.ConversationAttachmentRecord
	for rows.Next() {
		var raw []byte
		var record persistence.ConversationAttachmentRecord
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return err
		}
		records = append(records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, record := range records {
		expected := record.Attachment.Revision
		record.Attachment.State, record.Attachment.ErrorCode = "deleting", ""
		record.Attachment.Revision++
		record.Attachment.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		if err := s.saveAttachment(ctx, tx, record, expected, a); err != nil {
			return err
		}
		if err := s.queueAttachmentCleanup(ctx, tx, record.Attachment.ID, a); err != nil {
			return err
		}
	}
	return nil
}

var _ persistence.ConversationAttachmentRepository = (*ConversationStore)(nil)
