package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) ConversationSourceAuthority(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority) (agentsdk.ConversationAuthority, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationAuthority{}, err
	}
	if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
		return agentsdk.ConversationAuthority{}, conversationError("bad_request", "source_reference_invalid")
	}
	row, err := s.runRow(ctx, s.store.Database(), ref.ConversationID, ref.RunID, a)
	if err != nil {
		return agentsdk.ConversationAuthority{}, err
	}
	if conversationAuthority(row.Authority) != nil || conversationOwner(row.Authority) != conversationOwner(a) {
		return agentsdk.ConversationAuthority{}, conversationError("forbidden", "execution_subject_mismatch")
	}
	return row.Authority, nil
}

var _ persistence.ConversationSourceAuthorityRepository = (*ConversationStore)(nil)

func (s *ConversationStore) ConversationSourceSnapshot(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	var out persistence.ConversationSourceSnapshot
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
		return out, conversationError("bad_request", "source_reference_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, a)
		if err != nil {
			return err
		}
		out.Run = row.Run
		out.Authority = row.Authority
		predicate := query.And(conversationScope(a, ref.ConversationID), query.Equal("run_id", ref.RunID))
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_inputs").Columns("payload_json").Where(predicate).Build()
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
		if err == nil {
			out.Input = &agentsdk.ConversationModelRequest{}
			if err = json.Unmarshal(raw, out.Input); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_tool_calls").Columns("payload_json").Where(predicate).OrderBy(query.Ascending("step_no"), query.Ascending("call_key")).Limit(65).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if len(out.Calls) >= 64 {
				return conversationError("unavailable", "source_limit_exceeded")
			}
			var call persistence.ConversationToolExecution
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &call); err != nil {
				return err
			}
			out.Calls = append(out.Calls, call)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		rows.Close()
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(query.And(conversationScope(a, ref.ConversationID), query.Equal("consumed_run_id", ref.RunID))).Limit(129).Build()
		if err != nil {
			return err
		}
		peers, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer peers.Close()
		for peers.Next() {
			if len(out.Peers) >= 128 {
				return conversationError("unavailable", "source_limit_exceeded")
			}
			var m agentsdk.ConversationAgentMessage
			if err = peers.Scan(&raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &m); err != nil {
				return err
			}
			out.Peers = append(out.Peers, m)
		}
		if err = peers.Err(); err != nil {
			return err
		}
		peers.Close()
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationStepSourceTable).Columns("step_no", "payload_json").Where(predicate).OrderBy(query.Ascending("step_no")).Limit(257).Build()
		if err != nil {
			return err
		}
		sources, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer sources.Close()
		for sources.Next() {
			var value persistence.ConversationStepSources
			if err = sources.Scan(&value.Step, &raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &value.Sources); err != nil {
				return err
			}
			if len(out.StepSources) >= 256 || len(value.Sources) > 64 {
				return conversationError("unavailable", "source_limit_exceeded")
			}
			out.StepSources = append(out.StepSources, value)
		}
		return sources.Err()
	})
	return out, err
}

var _ persistence.ConversationSourceRepository = (*ConversationStore)(nil)
