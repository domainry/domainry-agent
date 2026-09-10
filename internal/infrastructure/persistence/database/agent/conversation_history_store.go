package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

type historyCursor struct {
	Owner, Query, Before string
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
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || len(cursor.Before) > 96 || cursor.Before == "" {
			return out, conversationError("bad_request", "history_cursor_invalid")
		}
		predicate = query.And(predicate, query.LessThan("message_id", cursor.Before))
	}
	// The existing message table stores its original content in an opaque JSON
	// payload. Scan bounded keyset pages instead of searching JSON encodings or
	// silently claiming that a capped scan searched the whole user's history.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("payload_json").Where(predicate).OrderBy(query.Descending("message_id")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned := 0
	last := ""
	more := false
	needle := strings.ToLower(in.Query)
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			more = true
			break
		}
		var raw []byte
		var message agentsdk.ConversationMessage
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &message); err != nil {
			return out, err
		}
		scanned++
		last = message.ID
		if !after.IsZero() && message.CreatedAt.Before(after) || !before.IsZero() && !message.CreatedAt.Before(before) || !strings.Contains(strings.ToLower(message.Content), needle) {
			continue
		}
		out.Items = append(out.Items, agentsdk.ConversationHistoryHit{ConversationID: message.ConversationID, MessageID: message.ID, RunID: message.RunID, Seq: message.Seq, Role: message.Role, Excerpt: historyPrefix(message.Content, 256), CreatedAt: message.CreatedAt})
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

func (s *ConversationStore) HistoryMessage(ctx context.Context, conversationID, messageID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationMessage, error) {
	var out agentsdk.ConversationMessage
	if _, err := s.Get(ctx, conversationID, a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("payload_json").Where(query.And(conversationScope(a, conversationID), query.Equal("message_id", messageID))).Build()
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
	err = json.Unmarshal(raw, &out)
	return out, err
}
