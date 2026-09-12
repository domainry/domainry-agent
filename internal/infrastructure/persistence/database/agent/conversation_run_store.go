package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type conversationRunRow struct {
	Run                      agentsdk.ConversationRun
	Authority                agentsdk.ConversationAuthority
	Owner                    string
	Fence, Expires, EventSeq int64
}

var conversationRunColumns = []string{"payload_json", "authority_json", "request_hash", "lease_owner", "fence", "lease_expires_at", "event_seq"}

func scanConversationRun(row interface{ Scan(...any) error }) (conversationRunRow, error) {
	var v conversationRunRow
	var raw, authority []byte
	err := row.Scan(&raw, &authority, &v.Run.RequestHash, &v.Owner, &v.Fence, &v.Expires, &v.EventSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return v, conversationError("not_found", "run_not_found")
	}
	if err != nil {
		return v, err
	}
	hash := v.Run.RequestHash
	if err = json.Unmarshal(raw, &v.Run); err != nil {
		return v, err
	}
	v.Run.RequestHash = hash
	v.Run.LastEventSeq = v.EventSeq
	err = json.Unmarshal(authority, &v.Authority)
	return v, err
}
func (s *ConversationStore) runRow(ctx context.Context, db conversationDB, id, runID string, a agentsdk.ConversationAuthority) (conversationRunRow, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns(conversationRunColumns...).Where(query.And(conversationScope(a, id), query.Equal("run_id", runID))).Build()
	if err != nil {
		return conversationRunRow{}, err
	}
	return scanConversationRun(db.QueryRowContext(ctx, q, args...))
}
func (s *ConversationStore) Run(ctx context.Context, id, runID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if _, err := s.Get(ctx, id, a); err != nil {
		return agentsdk.ConversationRun{}, err
	}
	v, err := s.runRow(ctx, s.store.Database(), id, runID, a)
	if err != nil {
		return v.Run, err
	}
	err = s.projectConversationRunAudit(ctx, &v.Run, a)
	return v.Run, err
}
func (s *ConversationStore) saveRun(ctx context.Context, tx *sql.Tx, v, old conversationRunRow) error {
	if conversationOwner(v.Authority) != conversationOwner(old.Authority) {
		return conversationError("forbidden", "authority_invalid")
	}
	v.Run.UpdatedAt = time.Now().UTC()
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), "_agent_conversation_runs").Set("payload_json", conversationJSON(v.Run)).Set("authority_json", conversationJSON(v.Authority)).Set("status", v.Run.Status).Set("lease_owner", v.Owner).Set("fence", v.Fence).Set("lease_expires_at", v.Expires).Set("event_seq", v.EventSeq).Where(query.And(conversationScope(old.Authority, v.Run.ConversationID), query.Equal("run_id", v.Run.ID), query.Equal("fence", old.Fence), query.Equal("status", old.Run.Status), query.Equal("event_seq", old.EventSeq))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}
func (s *ConversationStore) event(ctx context.Context, tx *sql.Tx, v *conversationRunRow, kind string, data map[string]any) error {
	if err := projectConversationExecutionEvent(&v.Run, kind, data); err != nil {
		return err
	}
	v.EventSeq++
	v.Run.LastEventSeq = v.EventSeq
	v.Run.UpdatedAt = time.Now().UTC()
	e := agentsdk.ConversationEvent{RunID: v.Run.ID, Seq: v.EventSeq, Type: kind, Data: data, CreatedAt: time.Now().UTC()}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_events").Columns("owner_key", "conversation_id", "run_id", "seq", "payload_json").Values(conversationOwner(v.Authority), v.Run.ConversationID, v.Run.ID, e.Seq, conversationJSON(e)).Build()
	return conversationExec(ctx, tx, q, args, err)
}
func (s *ConversationStore) Enqueue(ctx context.Context, id string, in agentsdk.ConversationSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := s.get(ctx, tx, id, a)
		if err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns(conversationRunColumns...).Where(query.And(conversationScope(a, id), query.Equal("client_message_id", in.ClientMessageID))).Build()
		if err != nil {
			return err
		}
		previous, err := scanConversationRun(tx.QueryRowContext(ctx, q, args...))
		hash := conversationHash(in)
		if err == nil {
			if previous.Run.RequestHash != hash {
				return conversationError("conflict", "idempotency_conflict")
			}
			out = previous.Run
			return nil
		}
		var coded *agentsdk.Error
		if !errors.As(err, &coded) || coded.Code != "agent.conversation.run_not_found" {
			return err
		}
		if c.Archived {
			return conversationError("conflict", "archived")
		}
		if c.ActiveRunID != "" {
			return conversationError("conflict", "busy")
		}
		now := time.Now().UTC()
		v := conversationRunRow{Authority: a, Run: agentsdk.ConversationRun{ID: conversationID("crun_"), ConversationID: id, ClientMessageID: in.ClientMessageID, RequestHash: hash, Status: "queued", UserSeq: c.LastSeq + 1, CreatedAt: now, UpdatedAt: now}}
		if in.WriteScope != nil {
			scope := *in.WriteScope
			v.Run.WriteScope = &scope
		}
		m := agentsdk.ConversationMessage{ID: conversationID("msg_"), ConversationID: id, RunID: v.Run.ID, Seq: v.Run.UserSeq, Role: "user", Content: in.Message, CreatedAt: now}
		c.LastSeq = m.Seq
		c.ActiveRunID = v.Run.ID
		if err = s.save(ctx, tx, c, c.Revision, a); err != nil {
			return err
		}
		if err = s.insertMessage(ctx, tx, m, a); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "run.queued", map[string]any{"message_id": m.ID, "message_seq": m.Seq}); err != nil {
			return err
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("owner_key", "conversation_id", "run_id", "client_message_id", "runtime_id", "authority_json", "request_hash", "status", "lease_owner", "fence", "lease_expires_at", "event_seq", "created_at", "payload_json").Values(conversationOwner(a), id, v.Run.ID, in.ClientMessageID, a.RuntimeID, conversationJSON(a), hash, "queued", "", 0, 0, v.EventSeq, now.UnixMilli(), conversationJSON(v.Run)).Build()
		out = v.Run
		return conversationExec(ctx, tx, q, args, err)
	})
	return out, err
}
func (s *ConversationStore) Claim(ctx context.Context, runtimeID, owner string, ttl time.Duration) (agentpersistence.ConversationClaim, bool, error) {
	var claim agentpersistence.ConversationClaim
	found := false
	if runtimeID == "" || owner == "" || ttl <= 0 {
		return claim, false, conversationError("bad_request", "claim_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		builder := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns(conversationRunColumns...).Where(query.And(query.Equal("runtime_id", runtimeID), query.Or(query.Equal("status", "queued"), query.And(query.Equal("status", "running"), query.LessThanOrEqual("lease_expires_at", now.UnixMilli()))))).OrderBy(query.Ascending("created_at")).Limit(1)
		profile := s.store.Profile()
		if profile != nil && profile.Capabilities().RowLock {
			var err error
			builder, err = profile.ApplyClaimLock(builder, profile.Capabilities().SkipLocked)
			if err != nil {
				return err
			}
		}
		q, args, err := builder.Build()
		if err != nil {
			return err
		}
		old, err := scanConversationRun(tx.QueryRowContext(ctx, q, args...))
		var coded *agentsdk.Error
		if errors.As(err, &coded) && coded.Code == "agent.conversation.run_not_found" {
			found = false
			return nil
		}
		if err != nil {
			return err
		}
		if old.Authority.RuntimeID != runtimeID {
			return conversationError("forbidden", "runtime_denied")
		}
		c, err := s.get(ctx, tx, old.Run.ConversationID, old.Authority)
		if err != nil {
			return err
		}
		if old.Run.BackgroundTask == nil && c.ActiveRunID != old.Run.ID {
			return conversationError("conflict", "run_superseded")
		}
		v := old
		v.Owner = owner
		v.Fence++
		v.Expires = now.Add(ttl).UnixMilli()
		v.Run.Status = "running"
		v.Run.Attempt++
		v.Run.DraftText, v.Run.DraftBytes = "", 0
		if err = s.event(ctx, tx, &v, "run.started", map[string]any{"attempt": v.Run.Attempt, "recovered": old.Run.Status == "running", "draft_reset": true}); err != nil {
			return err
		}
		if err = s.saveRun(ctx, tx, v, old); err != nil {
			return err
		}
		claim = agentpersistence.ConversationClaim{Authority: v.Authority, Run: v.Run, Owner: owner, Fence: v.Fence, ExpiresAt: time.UnixMilli(v.Expires)}
		found = true
		return nil
	})
	return claim, found, err
}
func (s *ConversationStore) claimed(ctx context.Context, tx *sql.Tx, claim agentpersistence.ConversationClaim) (conversationRunRow, error) {
	v, err := s.runRow(ctx, tx, claim.Run.ConversationID, claim.Run.ID, claim.Authority)
	if err != nil {
		return v, err
	}
	if v.Run.Status != "running" || v.Owner != claim.Owner || v.Fence != claim.Fence || v.Expires <= time.Now().UnixMilli() {
		return v, conversationError("conflict", "lease_lost")
	}
	return v, nil
}
func (s *ConversationStore) Heartbeat(ctx context.Context, claim agentpersistence.ConversationClaim, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return false, conversationError("bad_request", "claim_invalid")
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), "_agent_conversation_runs").Set("lease_expires_at", time.Now().Add(ttl).UnixMilli()).Where(query.And(conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID), query.Equal("status", "running"), query.Equal("lease_owner", claim.Owner), query.Equal("fence", claim.Fence), query.GreaterThan("lease_expires_at", time.Now().UnixMilli()))).Build()
	if err != nil {
		return false, err
	}
	result, err := s.store.Database().ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *ConversationStore) AppendEvent(ctx context.Context, claim agentpersistence.ConversationClaim, kind string, data map[string]any) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		v := old
		if err = s.event(ctx, tx, &v, kind, data); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, v, old)
	})
}

func (s *ConversationStore) AppendDelta(ctx context.Context, claim agentpersistence.ConversationClaim, offset int, delta string) error {
	if offset < 0 || delta == "" || !utf8.ValidString(delta) || strings.ContainsRune(delta, 0) {
		return conversationError("bad_request", "delta_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		size := len(old.Run.DraftText)
		if offset < size {
			if len(delta) <= size-offset && old.Run.DraftText[offset:offset+len(delta)] == delta {
				return nil
			}
			return conversationError("conflict", "delta_conflict")
		}
		if offset != size {
			return conversationError("conflict", "delta_offset_invalid")
		}
		v := old
		v.Run.DraftText += delta
		v.Run.DraftBytes = len(v.Run.DraftText)
		if err = s.event(ctx, tx, &v, "message.delta", map[string]any{"attempt": v.Run.Attempt, "offset": offset, "text": delta}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, v, old)
	})
}
func (s *ConversationStore) Finish(ctx context.Context, claim agentpersistence.ConversationClaim, result agentsdk.ConversationModelResult, code string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		v := old
		c, err := s.get(ctx, tx, v.Run.ConversationID, v.Authority)
		if err != nil {
			return err
		}
		background := v.Run.BackgroundTask != nil
		if !background && c.ActiveRunID != v.Run.ID {
			return conversationError("conflict", "run_superseded")
		}
		v.Run.Status = "failed"
		v.Run.ErrorCode = code
		v.Run.Model = result.Model
		v.Run.Usage = result.Usage
		v.Owner = ""
		v.Expires = 0
		if code == "" {
			if old.Run.DraftBytes > 0 && old.Run.DraftText != result.Content {
				return conversationError("conflict", "draft_mismatch")
			}
			v.Run.Status = "completed"
			m := agentsdk.ConversationMessage{ID: conversationID("msg_"), ConversationID: c.ID, RunID: v.Run.ID, Seq: c.LastSeq + 1, Role: "assistant", Content: result.Content, CreatedAt: time.Now().UTC()}
			if v.Run.BackgroundTask != nil {
				m.BackgroundTaskID = v.Run.BackgroundTask.TaskID
			}
			if err = s.insertMessage(ctx, tx, m, v.Authority); err != nil {
				return err
			}
			v.Run.AssistantMessageID = m.ID
			c.LastSeq = m.Seq
			v.Run.DraftText, v.Run.DraftBytes = "", 0
		}
		if !background {
			c.ActiveRunID = ""
		}
		if err = s.save(ctx, tx, c, c.Revision, v.Authority); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "run."+v.Run.Status, map[string]any{"attempt": v.Run.Attempt, "assistant_message_id": v.Run.AssistantMessageID, "error_code": code}); err != nil {
			return err
		}
		if err = s.finishConversationTask(ctx, tx, v.Run, v.Authority, result.Content); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, v, old)
	})
}
func (s *ConversationStore) Cancel(ctx context.Context, id, runID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	return s.transition(ctx, id, runID, a, false)
}
func (s *ConversationStore) Resume(ctx context.Context, id, runID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	return s.transition(ctx, id, runID, a, true)
}
func (s *ConversationStore) transition(ctx context.Context, id, runID string, a agentsdk.ConversationAuthority, resume bool) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := s.get(ctx, tx, id, a)
		if err != nil {
			return err
		}
		old, err := s.runRow(ctx, tx, id, runID, a)
		if err != nil {
			return err
		}
		v := old
		background := v.Run.BackgroundTask != nil
		out = v.Run
		if resume {
			if v.Run.Status == "waiting_user" || v.Run.Status == "waiting_confirmation" {
				return conversationError("conflict", "interaction_response_required")
			}
			if !v.Run.Terminal() && v.Run.Status != "needs_reconciliation" {
				return nil
			}
			if v.Run.Status == "completed" {
				return conversationError("conflict", "run_completed")
			}
			if c.Archived {
				return conversationError("conflict", "archived")
			}
			if i := v.Run.Interaction; i != nil && i.Kind != "reconciliation" && (i.Status == "cancelled" || i.Status == "expired" || i.Status == "rejected") {
				return conversationError("conflict", "interaction_closed")
			}
			if !background && (c.ActiveRunID != "" && !(c.ActiveRunID == runID && v.Run.Status == "needs_reconciliation") || c.LastSeq != max(v.Run.UserSeq, v.Run.LastInputSeq)) {
				return conversationError("conflict", "run_superseded")
			}
			v.Run.Status = "queued"
			// Explicit resumption refreshes the trusted role selection only;
			// owner scope and frozen operations remain unchanged.
			v.Authority = a
			v.Run.ErrorCode = ""
			v.Run.DraftText, v.Run.DraftBytes = "", 0
			if !background {
				c.ActiveRunID = runID
			}
		} else {
			if v.Run.Terminal() {
				return nil
			}
			if err = s.interruptConversationWrites(ctx, tx, &v); err != nil {
				return err
			}
			v.Run.Status = "cancelled"
			if c.ActiveRunID == runID {
				c.ActiveRunID = ""
			}
			if err = s.closeRunInteraction(ctx, tx, &v, "cancelled"); err != nil {
				return err
			}
		}
		v.Fence++
		v.Owner = ""
		v.Expires = 0
		if !background {
			if err = s.save(ctx, tx, c, c.Revision, a); err != nil {
				return err
			}
		}
		if err = s.event(ctx, tx, &v, "run."+v.Run.Status, map[string]any{"attempt": v.Run.Attempt, "draft_reset": resume}); err != nil {
			return err
		}
		if v.Run.BackgroundTask != nil {
			if resume {
				err = s.resumeConversationTask(ctx, tx, v.Run, v.Authority)
			} else {
				err = s.finishConversationTask(ctx, tx, v.Run, v.Authority, "")
			}
			if err != nil {
				return err
			}
		}
		out = v.Run
		return s.saveRun(ctx, tx, v, old)
	})
	return out, err
}
func (s *ConversationStore) Events(ctx context.Context, id, runID string, after int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationEventPage, error) {
	out := agentsdk.ConversationEventPage{Items: []agentsdk.ConversationEvent{}, NextSeq: after}
	if _, err := s.Get(ctx, id, a); err != nil {
		return out, err
	}
	row, err := s.runRow(ctx, s.store.Database(), id, runID, a)
	if err != nil {
		return out, err
	}
	if after > row.EventSeq {
		return out, conversationError("bad_request", "cursor_invalid")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_events").Columns("payload_json").Where(query.And(conversationScope(a, id), query.Equal("run_id", runID), query.GreaterThan("seq", after), query.LessThanOrEqual("seq", row.EventSeq))).OrderBy(query.Ascending("seq")).Limit(conversationLimit(limit)).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var e agentsdk.ConversationEvent
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &e); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
		out.NextSeq = e.Seq
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Terminal = row.Run.Terminal() && out.NextSeq >= row.EventSeq
	return out, nil
}

var _ agentpersistence.ConversationRepository = (*ConversationStore)(nil)
