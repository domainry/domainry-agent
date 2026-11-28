package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

type historyCursor struct {
	Owner, Query string
	Before       int64
}

func historyPrefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func historyMatchExcerpt(text, needle string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	position := strings.Index(strings.ToLower(text), strings.ToLower(needle))
	if position < 0 {
		return historyPrefix(text, limit)
	}
	start := max(0, position-limit/4)
	end := min(len(text), start+limit)
	if end == len(text) {
		start = max(0, end-limit)
	}
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	excerpt := strings.TrimSpace(text[start:end])
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(text) {
		excerpt += "…"
	}
	return excerpt
}

func (s *ConversationStore) SearchHistory(ctx context.Context, in agentsdk.ConversationHistorySearch, a agentsdk.ConversationAuthority) (agentsdk.ConversationHistorySearchResult, error) {
	out := agentsdk.ConversationHistorySearchResult{Items: []agentsdk.ConversationHistoryHit{}}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !executionText(in.Query, 256, true) || len(in.ConversationID) > 96 || in.Limit < 0 || in.Limit > 20 || len(in.Cursor) > 2048 {
		return out, conversationError("bad_request", "history_query_invalid")
	}
	if in.Limit == 0 {
		in.Limit = 10
	}
	var after, before time.Time
	var err error
	if in.After != "" {
		after, err = time.Parse(time.RFC3339, in.After)
		if err != nil {
			return out, conversationError("bad_request", "history_query_invalid")
		}
	}
	if in.Before != "" {
		before, err = time.Parse(time.RFC3339, in.Before)
		if err != nil {
			return out, conversationError("bad_request", "history_query_invalid")
		}
	}
	if !after.IsZero() && !before.IsZero() && !before.After(after) {
		return out, conversationError("bad_request", "history_query_invalid")
	}
	if in.ConversationID != "" {
		if _, err = s.Get(ctx, in.ConversationID, a); err != nil {
			return out, err
		}
	}
	keyInput := in
	keyInput.Cursor = ""
	owner, hash := conversationOwner(a), conversationHash(keyInput)
	predicate := query.Equal("owner_key", owner)
	if in.ConversationID != "" {
		predicate = query.And(predicate, query.Equal("conversation_id", in.ConversationID))
	}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		var cursor historyCursor
		if err != nil || unmarshalDurableJSON(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || cursor.Before < 1 {
			return out, conversationError("bad_request", "history_cursor_invalid")
		}
		predicate = query.And(predicate, query.LessThan("id", cursor.Before))
	}
	predicate = query.And(predicate, query.Equal("item_kind", conversationItemMessage))
	// The typed item journal stores original message content in an opaque JSON
	// payload. Scan bounded keyset pages instead of searching JSON encodings or
	// silently claiming that a capped scan searched the whole user's history.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("id", "payload_json").Where(predicate).OrderBy(query.Descending("id")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned := 0
	last := int64(0)
	more := false
	needle := strings.ToLower(in.Query)
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			more = true
			break
		}
		var physicalID int64
		var raw []byte
		var message agentsdk.ConversationMessage
		if err = rows.Scan(&physicalID, &raw); err != nil {
			return out, err
		}
		if err = unmarshalDurableJSON(raw, &message); err != nil {
			return out, err
		}
		scanned++
		last = physicalID
		if !after.IsZero() && message.CreatedAt.Before(after) || !before.IsZero() && !message.CreatedAt.Before(before) || !strings.Contains(strings.ToLower(message.Content), needle) {
			continue
		}
		out.Items = append(out.Items, agentsdk.ConversationHistoryHit{ConversationID: message.ConversationID, MessageID: message.ID, RunID: message.RunID, Seq: message.Seq, Role: message.Role, Excerpt: historyMatchExcerpt(message.Content, in.Query, 1024), CreatedAt: message.CreatedAt})
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Complete = !more
	if more {
		out.NextCursor = base64.RawURLEncoding.EncodeToString(conversationJSON(historyCursor{Owner: owner, Query: hash, Before: last}))
	}
	return out, nil
}

func (s *ConversationStore) RecentMemoryHistory(ctx context.Context, excludeConversationID string, limit int, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationHistoryHit, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	excludeConversationID = strings.TrimSpace(excludeConversationID)
	if len(excludeConversationID) > 96 || limit < 1 || limit > 128 {
		return nil, conversationError("bad_request", "history_query_invalid")
	}
	predicate := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("item_kind", conversationItemMessage))
	if excludeConversationID != "" {
		predicate = query.And(predicate, query.NotEqual("conversation_id", excludeConversationID))
	}
	scanLimit := min(512, limit*4)
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).
		Columns("payload_json").Where(predicate).OrderBy(query.Descending("id")).Limit(scanLimit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]agentsdk.ConversationHistoryHit, 0, limit)
	for rows.Next() && len(out) < limit {
		var raw []byte
		var message agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = unmarshalDurableJSON(raw, &message); err != nil {
			return nil, err
		}
		if message.Role != "user" && message.Role != "assistant" || strings.TrimSpace(message.Content) == "" {
			continue
		}
		out = append(out, agentsdk.ConversationHistoryHit{ConversationID: message.ConversationID, MessageID: message.ID, RunID: message.RunID, Seq: message.Seq, Role: message.Role, Excerpt: historyPrefix(message.Content, 256), CreatedAt: message.CreatedAt})
	}
	return out, rows.Err()
}

func (s *ConversationStore) HistoryMessage(ctx context.Context, conversationID, messageID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationMessage, error) {
	var out agentsdk.ConversationMessage
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(conversationItemScope(a, conversationID, conversationItemMessage), query.Equal("reference_id", messageID))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, conversationError("not_found", "message_not_found")
		}
		return out, err
	}
	err = unmarshalDurableJSON(raw, &out)
	return out, err
}
