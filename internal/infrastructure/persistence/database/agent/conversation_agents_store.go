package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
	"time"
)

func (s *ConversationStore) collaborationReplay(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, key string, request any, out any) (bool, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationCollaborationMutationTable).Columns("request_hash", "payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("mutation_id", key))).Build()
	if err != nil {
		return false, err
	}
	var hash string
	var raw []byte
	err = tx.QueryRowContext(ctx, q, args...).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if hash != conversationHash(request) {
		return false, conversationError("conflict", "idempotency_conflict")
	}
	return true, json.Unmarshal(raw, out)
}

func (s *ConversationStore) saveCollaborationMutation(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, key string, request, result any) error {
	q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationCollaborationMutationTable).Columns("owner_key", "mutation_id", "request_hash", "payload_json").Values(conversationOwner(a), key, conversationHash(request), conversationJSON(result)).Build()
	return conversationExec(ctx, tx, q, args, err)
}

func (s *ConversationStore) conversationAgent(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("agent_id", id))).Build()
	if err != nil {
		return out, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, conversationError("not_found", "agent_not_found")
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (s *ConversationStore) ConversationAgent(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	return s.conversationAgent(ctx, s.store.Database(), id, a)
}

func (s *ConversationStore) ConversationAgents(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgent, error) {
	out := []agentsdk.ConversationAgent{}
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentTable).Columns("payload_json").Where(query.Equal("owner_key", conversationOwner(a))).OrderBy(query.Ascending("agent_id")).Limit(64).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var item agentsdk.ConversationAgent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ConversationStore) WriteConversationAgent(ctx context.Context, id string, in agentsdk.ConversationAgentWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if !personalMemoryKey(in.ClientID) || in.ExpectedRevision < 0 {
			return conversationError("bad_request", "agent_invalid")
		}
		key := conversationHash([]string{"agent", id, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if id == "" {
			if in.ExpectedRevision != 0 {
				return conversationError("conflict", "revision_conflict")
			}
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentTable).Projections(query.Project(query.CountAll())).Where(query.Equal("owner_key", conversationOwner(a))).Build()
			if err != nil {
				return err
			}
			var count int
			if err = tx.QueryRowContext(ctx, q, args...).Scan(&count); err != nil {
				return err
			}
			if count >= 64 {
				return conversationError("rate_limited", "agent_limit")
			}
			out = agentsdk.ConversationAgent{ID: "agent_" + conversationHash([]string{conversationOwner(a), in.ClientID})[:32], CreatedAt: now}
		} else {
			var err error
			out, err = s.conversationAgent(ctx, tx, id, a)
			if err != nil {
				return err
			}
			if out.Revision != in.ExpectedRevision {
				return conversationError("conflict", "revision_conflict")
			}
		}
		out.Name, out.Description, out.Instructions = in.Name, in.Description, in.Instructions
		out.DefinitionKey, out.DefinitionVersion, out.DefinitionDigest = in.DefinitionKey, in.DefinitionVersion, in.DefinitionDigest
		out.Tools, out.SkillKeys = append([]string{}, in.Tools...), append([]string{}, in.SkillKeys...)
		out.ModelKey, out.Enabled, out.MaxConcurrent = in.ModelKey, in.Enabled, in.MaxConcurrent
		out.Revision, out.UpdatedAt = in.ExpectedRevision+1, now
		if id == "" {
			q, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationAgentTable).Columns("owner_key", "agent_id", "revision", "payload_json").Values(conversationOwner(a), out.ID, out.Revision, conversationJSON(out)).Build()
			if err = conversationExec(ctx, tx, q, args, err); err != nil {
				return err
			}
		} else {
			q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationAgentTable).Set("revision", out.Revision).Set("payload_json", conversationJSON(out)).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("agent_id", id), query.Equal("revision", in.ExpectedRevision))).Build()
			if err = conversationCAS(ctx, tx, q, args, err); err != nil {
				return err
			}
		}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}
