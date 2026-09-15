package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func validConversationForkSeed(seed persistence.ConversationForkSeed) bool {
	if seed.Version != agentsdk.ConversationTrajectoryVersion || !personalMemoryKey(seed.Source.ConversationID) || !personalMemoryKey(seed.Source.RunID) || seed.Source.BeforeStep != 0 || seed.BoundaryEventSeq < 1 || len(seed.SourceSHA256) != 64 || seed.CreatedAt.IsZero() || len(seed.Messages) == 0 || len(seed.Messages) > 4096 {
		return false
	}
	raw, err := json.Marshal(seed)
	if err != nil || len(raw) > 4*1024*1024 {
		return false
	}
	for _, message := range seed.Messages {
		if (message.Role != "system" && message.Role != "user" && message.Role != "assistant") || !validStoredConversationContent(message.Role, message.Content, message.ContentBlocks, "", true) {
			return false
		}
		for _, block := range message.ContentBlocks {
			if block.Image != nil && (block.Image.Source == nil || *block.Image.Source != seed.Source) {
				return false
			}
		}
	}
	return true
}

func (s *ConversationStore) ForkConversation(ctx context.Context, in agentsdk.ConversationForkRequest, seed persistence.ConversationForkSeed, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var out agentsdk.Conversation
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !personalMemoryKey(in.ClientID) || !executionText(in.Title, 512, true) || in.AgentID != "" && !personalMemoryKey(in.AgentID) || !validConversationForkSeed(seed) {
		return out, conversationError("bad_request", "fork_invalid")
	}
	requestHash := conversationHash([]any{"fork", in, seed.Source, seed.BoundaryEventSeq, seed.SourceSHA256})
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		lookup, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversations").Columns("payload_json", "request_hash").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_id", in.ClientID))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		var savedHash string
		if err = tx.QueryRowContext(ctx, lookup, args...).Scan(&raw, &savedHash); err == nil {
			if savedHash != requestHash || json.Unmarshal(raw, &out) != nil || out.Fork == nil || out.Fork.ConversationID != seed.Source.ConversationID || out.Fork.RunID != seed.Source.RunID || out.Fork.BoundaryEventSeq != seed.BoundaryEventSeq {
				return conversationError("conflict", "idempotency_conflict")
			}
			var stored []byte
			q, values, buildErr := query.NewSelectBuilder(s.store.Renderer(), conversationForkTable).Columns("payload_json").Where(query.And(conversationScope(a, out.ID))).Build()
			if buildErr != nil {
				return buildErr
			}
			var storedSeed persistence.ConversationForkSeed
			if scanErr := tx.QueryRowContext(ctx, q, values...).Scan(&stored); scanErr != nil || json.Unmarshal(stored, &storedSeed) != nil || conversationHash(storedSeed) != conversationHash(seed) {
				return conversationError("conflict", "fork_snapshot_changed")
			}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row, err := s.runRow(ctx, tx, seed.Source.ConversationID, seed.Source.RunID, a)
		if err != nil {
			return err
		}
		if row.Run.Status != "completed" || row.EventSeq != seed.BoundaryEventSeq || row.Run.AssistantMessageID == "" || row.Run.BackgroundTask != nil {
			return conversationError("conflict", "fork_boundary_unstable")
		}
		source, err := s.get(ctx, tx, seed.Source.ConversationID, a)
		if err != nil {
			return err
		}
		if source.DelegationID != "" {
			return conversationError("conflict", "fork_source_invalid")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		origin := &agentsdk.ConversationForkOrigin{ConversationID: seed.Source.ConversationID, RunID: seed.Source.RunID, BoundaryEventSeq: seed.BoundaryEventSeq, CreatedAt: now}
		out = agentsdk.Conversation{ID: conversationID("conv_"), AgentID: in.AgentID, Fork: origin, Title: strings.TrimSpace(in.Title), RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID, MemoryEnabled: source.MemoryEnabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
		q, values, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_conversations").Columns("owner_key", "conversation_id", "client_id", "request_hash", "title", "updated_at", "archived", "revision", "payload_json").Values(conversationOwner(a), out.ID, in.ClientID, requestHash, strings.ToLower(out.Title), now.UnixMilli(), 0, 1, conversationJSON(out)).Build()
		if err = conversationExec(ctx, tx, q, values, err); err != nil {
			return err
		}
		q, values, err = query.NewInsertBuilder(s.store.Renderer(), conversationForkTable).Columns("owner_key", "conversation_id", "source_conversation_id", "source_run_id", "source_event_seq", "payload_json").Values(conversationOwner(a), out.ID, seed.Source.ConversationID, seed.Source.RunID, seed.BoundaryEventSeq, conversationJSON(seed)).Build()
		return conversationExec(ctx, tx, q, values, err)
	})
	return out, err
}

func (s *ConversationStore) ConversationForkSeed(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) (persistence.ConversationForkSeed, bool, error) {
	var out persistence.ConversationForkSeed
	if err := conversationAuthority(a); err != nil {
		return out, false, err
	}
	if !personalMemoryKey(conversationID) {
		return out, false, conversationError("bad_request", "fork_invalid")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationForkTable).Columns("payload_json").Where(conversationScope(a, conversationID)).Build()
	if err != nil {
		return out, false, err
	}
	var raw []byte
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if err = json.Unmarshal(raw, &out); err != nil || !validConversationForkSeed(out) {
		return out, false, conversationError("conflict", "fork_snapshot_changed")
	}
	return out, true, nil
}

var _ persistence.ConversationForkRepository = (*ConversationStore)(nil)
