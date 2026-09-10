package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) Summary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationSummary, error) {
	var out agentsdk.ConversationSummary
	c, err := s.Get(ctx, id, a)
	if err != nil || c.SummaryID == "" {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_summaries").Columns("payload_json").Where(query.And(conversationScope(a, id), query.Equal("summary_id", c.SummaryID))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
func (s *ConversationStore) SaveSummary(ctx context.Context, claim agentpersistence.ConversationClaim, summary agentsdk.ConversationSummary) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		v := old
		c, err := s.get(ctx, tx, claim.Run.ConversationID, claim.Authority)
		if err != nil {
			return err
		}
		if c.ActiveRunID != claim.Run.ID || c.SummaryID != summary.PreviousID || summary.ConversationID != c.ID || summary.ThroughSeq >= claim.Run.UserSeq || summary.ThroughSeq <= 0 {
			return conversationError("conflict", "summary_stale")
		}
		if summary.PreviousID != "" {
			q, args, e := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_summaries").Columns("through_seq").Where(query.And(conversationScope(claim.Authority, c.ID), query.Equal("summary_id", summary.PreviousID))).Build()
			if e != nil {
				return e
			}
			var through int64
			if e = tx.QueryRowContext(ctx, q, args...).Scan(&through); e != nil {
				return e
			}
			if summary.ThroughSeq <= through && !summary.Rebuild {
				return conversationError("conflict", "summary_stale")
			}
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_messages").Columns("payload_json").Where(query.And(conversationScope(claim.Authority, c.ID), query.Equal("seq", summary.ThroughSeq))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
			return err
		}
		var boundary agentsdk.ConversationMessage
		if err = json.Unmarshal(raw, &boundary); err != nil {
			return err
		}
		if boundary.Role != "assistant" {
			return conversationError("bad_request", "summary_boundary_invalid")
		}
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_summaries").Columns("owner_key", "conversation_id", "summary_id", "through_seq", "payload_json").Values(conversationOwner(claim.Authority), c.ID, summary.ID, summary.ThroughSeq, conversationJSON(summary)).Build()
		if err = conversationExec(ctx, tx, q, args, err); err != nil {
			return err
		}
		c.SummaryID = summary.ID
		if err = s.save(ctx, tx, c, c.Revision, claim.Authority); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "context.compacted", map[string]any{"summary_id": summary.ID, "through_seq": summary.ThroughSeq}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, v, old)
	})
}
func (s *ConversationStore) Memories(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	return s.memories(ctx, s.store.Database(), a)
}
func (s *ConversationStore) memories(ctx context.Context, db conversationDB, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_user_memories").Columns("payload_json").Where(query.Equal("owner_key", conversationOwner(a))).OrderBy(query.Ascending("memory_id")).Limit(33).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentsdk.ConversationMemory{}
	for rows.Next() {
		var raw []byte
		var m agentsdk.ConversationMemory
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
func (s *ConversationStore) WriteMemory(ctx context.Context, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error) {
	var out agentsdk.ConversationMemory
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		return s.writeMemory(ctx, tx, in, a, &out, false)
	})
	return out, err
}

func (s *ConversationStore) writeMemory(ctx context.Context, tx *sql.Tx, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority, out *agentsdk.ConversationMemory, strictRevision bool) error {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_user_memories").Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID))).Build()
	if err != nil {
		return err
	}
	var raw []byte
	var old agentsdk.ConversationMemory
	err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists {
		if err = json.Unmarshal(raw, &old); err != nil {
			return err
		}
		if !strictRevision && old.Title == in.Title && old.Content == in.Content && old.Enabled == in.Enabled {
			*out = old
			return nil
		}
		if old.Revision != in.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
	} else {
		if in.ExpectedRevision != 0 {
			return conversationError("conflict", "revision_conflict")
		}
		items, err := s.memories(ctx, tx, a)
		if err != nil {
			return err
		}
		if len(items) >= 32 {
			return conversationError("conflict", "memory_limit")
		}
	}
	now := time.Now().UTC()
	*out = agentsdk.ConversationMemory{ID: in.ID, Title: in.Title, Content: in.Content, Enabled: in.Enabled, Revision: old.Revision + 1, CreatedAt: old.CreatedAt, UpdatedAt: now}
	if !exists {
		out.CreatedAt = now
		q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_user_memories").Columns("owner_key", "memory_id", "revision", "payload_json").Values(conversationOwner(a), in.ID, out.Revision, conversationJSON(out)).Build()
		return conversationExec(ctx, tx, q, args, err)
	}
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), "_agent_user_memories").Set("payload_json", conversationJSON(out)).Set("revision", out.Revision).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", in.ID), query.Equal("revision", in.ExpectedRevision))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) DeleteMemory(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	if err := conversationAuthority(a); err != nil {
		return err
	}
	q, args, err := query.NewDeleteBuilder(s.store.Renderer(), "_agent_user_memories").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("memory_id", id), query.Equal("revision", revision))).Build()
	return conversationCAS(ctx, s.store.Database(), q, args, err)
}
