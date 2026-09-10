package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

type ConversationStore struct{ store *Store }

func NewConversationStore(s *Store) *ConversationStore { return &ConversationStore{store: s} }

func (s *ConversationStore) Ready(ctx context.Context) error {
	for _, table := range []string{"_agent_conversations", "_agent_conversation_messages", "_agent_conversation_runs", "_agent_conversation_inputs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_user_memories", "_agent_conversation_steps", "_agent_conversation_tool_calls", interactionTable, "_agent_user_todos", "_agent_todo_mutations", attachmentTable, attachmentCleanupTable} {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("owner_key").Limit(1).Build()
		if err != nil {
			return err
		}
		var owner string
		err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	for _, table := range []string{libraryTable, libraryMemberTable} {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("scope_key").Limit(1).Build()
		if err != nil {
			return err
		}
		var scope string
		err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&scope)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

type conversationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func conversationID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}
func conversationHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func conversationJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func conversationError(class, code string) error {
	return &agentsdk.Error{Class: class, Code: "agent.conversation." + code}
}
func conversationAuthority(a agentsdk.ConversationAuthority) error {
	if !a.Known || strings.TrimSpace(a.RuntimeID) == "" || strings.TrimSpace(a.WorkspaceID) == "" || strings.TrimSpace(a.UserID) == "" || len(a.RuntimeID) > 255 || len(a.WorkspaceID) > 255 || len(a.UserID) > 255 {
		return conversationError("forbidden", "principal_required")
	}
	return nil
}
func conversationOwner(a agentsdk.ConversationAuthority) string {
	return conversationHash([]string{a.RuntimeID, a.WorkspaceID, a.UserID})
}
func conversationScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("conversation_id", id))
}
func conversationLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}
func conversationExec(ctx context.Context, db conversationDB, statement string, args []any, err error) error {
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, statement, args...)
	return err
}
func conversationCAS(ctx context.Context, db conversationDB, statement string, args []any, err error) error {
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return conversationError("conflict", "revision_conflict")
	}
	return nil
}
func (s *ConversationStore) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	for attempt := 0; ; attempt++ {
		tx, err := s.store.Database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err == nil {
			err = fn(tx)
			if err == nil {
				err = tx.Commit()
			}
			_ = tx.Rollback()
		}
		if err == nil || attempt >= 4 || !s.store.IsTransientError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(5<<attempt) * time.Millisecond):
		}
	}
}
func (s *ConversationStore) get(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var c agentsdk.Conversation
	if err := conversationAuthority(a); err != nil {
		return c, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("payload_json").Where(conversationScope(a, id)).Build()
	if err != nil {
		return c, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, conversationError("not_found", "not_found")
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}
func (s *ConversationStore) save(ctx context.Context, tx *sql.Tx, c agentsdk.Conversation, expected int64, a agentsdk.ConversationAuthority) error {
	c.Revision = expected + 1
	c.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), "_agent_conversations").Set("payload_json", conversationJSON(c)).Set("updated_at", c.UpdatedAt.UnixMilli()).Set("revision", c.Revision).Set("title", strings.ToLower(c.Title)).Set("archived", boolNumber(c.Archived)).Where(query.And(conversationScope(a, c.ID), query.Equal("revision", expected))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}
func boolNumber(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (s *ConversationStore) Create(ctx context.Context, in agentsdk.ConversationCreate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var c agentsdk.Conversation
	if err := conversationAuthority(a); err != nil {
		return c, err
	}
	if in.ClientID == "" || len(in.ClientID) > 96 {
		return c, conversationError("bad_request", "client_id_required")
	}
	hash := conversationHash(in)
	lookup := func() (agentsdk.Conversation, error) {
		var old agentsdk.Conversation
		var raw []byte
		var oldHash string
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("payload_json", "request_hash").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_id", in.ClientID))).Build()
		if err != nil {
			return old, err
		}
		if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw, &oldHash); err != nil {
			return old, err
		}
		if oldHash != hash {
			return old, conversationError("conflict", "idempotency_conflict")
		}
		err = json.Unmarshal(raw, &old)
		return old, err
	}
	if old, err := lookup(); err == nil {
		return old, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	c = agentsdk.Conversation{ID: conversationID("conv_"), Title: in.Title, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID, MemoryEnabled: in.MemoryEnabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(a), c.ID, in.ClientID, hash, strings.ToLower(c.Title), now.UnixMilli(), 0, 1, conversationJSON(c)).Build()
	if err = conversationExec(ctx, s.store.Database(), q, args, err); err != nil {
		if old, replayErr := lookup(); replayErr == nil {
			return old, nil
		} else {
			var conflict *agentsdk.Error
			if errors.As(replayErr, &conflict) && conflict.Class == "conflict" {
				return c, replayErr
			}
		}
		return c, err
	}
	return c, nil
}
func (s *ConversationStore) Get(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	return s.get(ctx, s.store.Database(), id, a)
}
func (s *ConversationStore) List(ctx context.Context, in agentsdk.ConversationQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationPage, error) {
	out := agentsdk.ConversationPage{Items: []agentsdk.Conversation{}}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	p := []query.Predicate{query.Equal("owner_key", conversationOwner(a))}
	if !in.IncludeArchived {
		p = append(p, query.Equal("archived", 0))
	}
	if in.BeforeID != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.BeforeID)
		stamp, id, ok := strings.Cut(string(raw), ":")
		n, parseErr := strconv.ParseInt(stamp, 10, 64)
		if e != nil || !ok || parseErr != nil || id == "" {
			return out, conversationError("bad_request", "cursor_invalid")
		}
		p = append(p, query.Or(query.LessThan("updated_at", n), query.And(query.Equal("updated_at", n), query.LessThan("conversation_id", id))))
	}
	if in.Search != "" {
		term := strings.NewReplacer("~", "~~", "%", "~%", "_", "~_").Replace(strings.ToLower(in.Search))
		p = append(p, query.LikeEscaped("title", "%"+term+"%"))
	}
	limit := conversationLimit(in.Limit)
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("payload_json").Where(query.And(p...)).OrderBy(query.Descending("updated_at"), query.Descending("conversation_id")).Limit(limit + 1).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	n := 0
	last := ""
	for rows.Next() {
		var raw []byte
		var c agentsdk.Conversation
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &c); err != nil {
			return out, err
		}
		n++
		if n > limit {
			out.NextCursor = last
			break
		}
		last = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.UpdatedAt.UnixMilli(), 10) + ":" + c.ID))
		out.Items = append(out.Items, c)
	}
	return out, rows.Err()
}
func (s *ConversationStore) Update(ctx context.Context, id string, in agentsdk.ConversationUpdate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var out agentsdk.Conversation
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := s.get(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if c.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		if c.ActiveRunID != "" {
			return conversationError("conflict", "busy")
		}
		if in.Title != nil {
			c.Title = *in.Title
		}
		if in.Archived != nil {
			c.Archived = *in.Archived
		}
		if in.MemoryEnabled != nil {
			c.MemoryEnabled = *in.MemoryEnabled
		}
		err = s.save(ctx, tx, c, c.Revision, a)
		if err == nil {
			out, err = s.get(ctx, tx, id, a)
		}
		return err
	})
	return out, err
}
func (s *ConversationStore) Delete(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		c, err := s.get(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if c.Revision != revision {
			return conversationError("conflict", "revision_conflict")
		}
		if err := s.deleteConversationAttachments(ctx, tx, id, a); err != nil {
			return err
		}
		q, args, e := query.NewDeleteBuilder(s.store.Renderer(), "_agent_conversations").Where(query.And(conversationScope(a, id), query.Equal("revision", revision))).Build()
		if err = conversationCAS(ctx, tx, q, args, e); err != nil {
			return err
		}
		for _, table := range []string{"_agent_conversation_messages", "_agent_conversation_runs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_conversation_inputs", "_agent_conversation_steps", "_agent_conversation_tool_calls", interactionTable} {
			q, args, e := query.NewDeleteBuilder(s.store.Renderer(), table).Where(conversationScope(a, id)).Build()
			if err = conversationExec(ctx, tx, q, args, e); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *ConversationStore) Messages(ctx context.Context, id string, in agentsdk.ConversationMessageQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationMessagePage, error) {
	out := agentsdk.ConversationMessagePage{Items: []agentsdk.ConversationMessage{}}
	if _, err := s.Get(ctx, id, a); err != nil {
		return out, err
	}
	p := []query.Predicate{conversationScope(a, id)}
	if in.BeforeSeq > 0 {
		p = append(p, query.LessThan("seq", in.BeforeSeq))
	}
	if in.AfterSeq > 0 {
		p = append(p, query.GreaterThan("seq", in.AfterSeq))
	}
	limit := conversationLimit(in.Limit)
	order := query.Descending("seq")
	if in.AfterSeq > 0 {
		order = query.Ascending("seq")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("payload_json").Where(query.And(p...)).OrderBy(order).Limit(limit + 1).Build()
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
		var m agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &m); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			if in.AfterSeq > 0 {
				out.NextAfterSeq = out.Items[len(out.Items)-1].Seq
			} else {
				out.NextBeforeSeq = out.Items[len(out.Items)-1].Seq
			}
			break
		}
		out.Items = append(out.Items, m)
	}
	for i, j := 0, len(out.Items)-1; in.AfterSeq == 0 && i < j; i, j = i+1, j-1 {
		out.Items[i], out.Items[j] = out.Items[j], out.Items[i]
	}
	return out, rows.Err()
}
func (s *ConversationStore) History(ctx context.Context, id string, after, through int64, limit int, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMessage, error) {
	if _, err := s.Get(ctx, id, a); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("payload_json").Where(query.And(conversationScope(a, id), query.GreaterThan("seq", after), query.LessThanOrEqual("seq", through))).OrderBy(query.Ascending("seq")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentsdk.ConversationMessage{}
	for rows.Next() {
		var raw []byte
		var m agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *ConversationStore) insertMessage(ctx context.Context, tx *sql.Tx, m agentsdk.ConversationMessage, a agentsdk.ConversationAuthority) error {
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("owner_key", "conversation_id", "message_id", "run_id", "seq", "payload_json").Values(conversationOwner(a), m.ConversationID, m.ID, m.RunID, m.Seq, conversationJSON(m)).Build()
	return conversationExec(ctx, tx, q, args, err)
}
