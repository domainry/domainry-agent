package agent

import (
	"context"
	"database/sql"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func conversationModelAttemptIndex(run agentsdk.ConversationRun, step, runAttempt int) int {
	for index := len(run.ModelAttempts) - 1; index >= 0; index-- {
		attempt := run.ModelAttempts[index]
		if attempt.Step == step && attempt.RunAttempt == runAttempt {
			return index
		}
	}
	return -1
}

func validConversationModelAttemptStep(step int) bool { return step >= -257 && step < 256 }

func (s *ConversationStore) BeginConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, step int) (agentsdk.ConversationModelAttempt, error) {
	var out agentsdk.ConversationModelAttempt
	if !validConversationModelAttemptStep(step) {
		return out, conversationError("bad_request", "model_attempt_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		number := 1
		if index := conversationModelAttemptIndex(row.Run, step, row.Run.Attempt); index >= 0 {
			previous := row.Run.ModelAttempts[index]
			switch previous.Status {
			case "started":
				out = previous
				return nil
			case "retry_scheduled":
				if previous.RetryAt != nil && time.Now().Before(*previous.RetryAt) {
					return conversationError("conflict", "model_retry_not_ready")
				}
				number = previous.Number + 1
			default:
				return conversationError("conflict", "model_attempt_finished")
			}
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		out = agentsdk.ConversationModelAttempt{Step: step, RunAttempt: row.Run.Attempt, Number: number, Status: "started", StartedAt: now}
		old := row
		if err = s.event(ctx, tx, &row, "model.attempt.started", map[string]any{"step": step, "attempt": row.Run.Attempt, "model_attempt": number, "started_at": now, "reset": true}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, row, old)
	})
	return out, err
}

func (s *ConversationStore) FailConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, step, number int, details agentsdk.ConversationModelFailureDetails, retryAt *time.Time) error {
	if !validConversationModelAttemptStep(step) || number < 1 {
		return conversationError("bad_request", "model_attempt_invalid")
	}
	if raw, err := marshalDurableJSON(details.Usage); err != nil || len(raw) > 256*1024 {
		return conversationError("bad_request", "model_usage_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		index := conversationModelAttemptIndex(row.Run, step, row.Run.Attempt)
		if index < 0 || row.Run.ModelAttempts[index].Number != number || row.Run.ModelAttempts[index].Status != "started" {
			return conversationError("conflict", "model_attempt_conflict")
		}
		completed := time.Now().UTC().Truncate(time.Millisecond)
		code := details.ErrorCode
		if code == "" {
			code = "provider_failed"
		}
		old := row
		if err = s.event(ctx, tx, &row, "model.attempt.failed", map[string]any{"step": step, "attempt": row.Run.Attempt, "model_attempt": number, "error_code": code, "usage": details.Usage, "completed_at": completed}); err != nil {
			return err
		}
		if retryAt != nil {
			retry := retryAt.UTC().Truncate(time.Millisecond)
			delay := max(int64(0), retry.Sub(completed).Milliseconds())
			if err = s.event(ctx, tx, &row, "model.retry.scheduled", map[string]any{"step": step, "attempt": row.Run.Attempt, "model_attempt": number, "retry_at": retry, "retry_delay_ms": delay}); err != nil {
				return err
			}
		}
		return s.saveRun(ctx, tx, row, old)
	})
}

func (s *ConversationStore) CompleteConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, step, number int, usage map[string]any) error {
	if !validConversationModelAttemptStep(step) || number < 1 {
		return conversationError("bad_request", "model_attempt_invalid")
	}
	if raw, err := marshalDurableJSON(usage); err != nil || len(raw) > 256*1024 {
		return conversationError("bad_request", "model_usage_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		index := conversationModelAttemptIndex(row.Run, step, row.Run.Attempt)
		if index < 0 || row.Run.ModelAttempts[index].Number != number {
			return conversationError("conflict", "model_attempt_conflict")
		}
		if row.Run.ModelAttempts[index].Status == "completed" {
			return nil
		}
		if row.Run.ModelAttempts[index].Status != "started" {
			return conversationError("conflict", "model_attempt_conflict")
		}
		completed := time.Now().UTC().Truncate(time.Millisecond)
		old := row
		if err = s.event(ctx, tx, &row, "model.attempt.completed", map[string]any{"step": step, "attempt": row.Run.Attempt, "model_attempt": number, "usage": usage, "completed_at": completed}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, row, old)
	})
}

var _ persistence.ConversationModelAttemptRepository = (*ConversationStore)(nil)
