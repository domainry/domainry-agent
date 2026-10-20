package agent

import (
	"context"
	"database/sql"
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
	return s.conversationSnapshot(ctx, ref, a, false)
}

// Explicit trajectory/fork reads need exact frozen requests and responses.
// They must not silently use the minimal authorization/source projection.
func (s *ConversationStore) ConversationExecutionSnapshot(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	return s.conversationSnapshot(ctx, ref, a, true)
}

func (s *ConversationStore) conversationSnapshot(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority, exact bool) (persistence.ConversationSourceSnapshot, error) {
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
		inputColumn := "source_context_json"
		if exact {
			inputColumn = "payload_json"
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationStepInputTable).Columns(inputColumn).Where(query.And(predicate, query.Equal("step_no", -1))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, q, args...).Scan(&raw)
		if err == nil {
			out.Input = &agentsdk.ConversationModelRequest{}
			if err = unmarshalDurableJSON(raw, out.Input); err != nil {
				return err
			}
			if err = s.restoreModelInputSources(ctx, tx, query.And(predicate, query.Equal("step_no", -1)), out.Input); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		stepPredicate := query.And(predicate, query.GreaterThanOrEqual("step_no", 0))
		if ref.BeforeStep > 0 {
			stepPredicate = query.And(stepPredicate, query.LessThan("step_no", ref.BeforeStep))
		}
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationStepInputTable).Columns("step_no", "source_context_json").Where(stepPredicate).OrderBy(query.Ascending("step_no")).Limit(257).Build()
		if err != nil {
			return err
		}
		stepRows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer stepRows.Close()
		for stepRows.Next() {
			var number int
			var sourceContext executionSourceContext
			if len(out.Steps) >= 256 {
				return conversationError("unavailable", "source_limit_exceeded")
			}
			if err = stepRows.Scan(&number, &raw); err != nil {
				return err
			}
			if err = unmarshalDurableJSON(raw, &sourceContext); err != nil {
				return err
			}
			projected, err := sourceContext.stepContext(number)
			if err != nil {
				return err
			}
			out.Steps = append(out.Steps, persistence.ConversationExecutionStep{Number: number, Input: agentsdk.ConversationStepRequest{Context: projected.Context, Messages: projected.Messages}})
			if projected.Context != nil {
				out.StepContexts = append(out.StepContexts, projected)
			}
		}
		if err = stepRows.Err(); err != nil {
			return err
		}
		stepRows.Close()
		if exact {
			for index, projected := range out.Steps {
				var step persistence.ConversationExecutionStep
				found, err := s.readFrozenExecutionStep(ctx, tx, query.And(predicate, query.Equal("step_no", projected.Number)), &step)
				if err != nil {
					return err
				}
				if !found {
					return conversationError("conflict", "step_input_conflict")
				}
				out.Steps[index] = step
			}
		}
		if row.Run.AssistantMessageID != "" {
			q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(conversationItemScope(a, ref.ConversationID, conversationItemMessage), query.Equal("reference_id", row.Run.AssistantMessageID), query.Equal("run_id", ref.RunID))).Build()
			if err != nil {
				return err
			}
			var message agentsdk.ConversationMessage
			if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); err != nil {
				return err
			}
			if err = unmarshalDurableJSON(raw, &message); err != nil {
				return err
			}
			out.FinalMessage = &message
		}
		q, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationToolCallTable).Columns("payload_json").Where(predicate).OrderBy(query.Ascending("step_no"), query.Ascending("call_key")).Limit(65).Build()
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
			if err = unmarshalDurableJSON(raw, &call); err != nil {
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
			if err = unmarshalDurableJSON(raw, &m); err != nil {
				return err
			}
			out.Peers = append(out.Peers, m)
		}
		if err = peers.Err(); err != nil {
			return err
		}
		peers.Close()
		out.StepSources, err = s.readStepSourceEdges(ctx, tx, query.And(predicate, query.GreaterThanOrEqual("step_no", 0)))
		return err
	})
	if err == nil {
		err = s.projectConversationRunAudit(ctx, &out.Run, a)
	}
	return out, err
}

var _ persistence.ConversationSourceRepository = (*ConversationStore)(nil)
