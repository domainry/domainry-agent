package application

import (
	"context"
	"errors"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func conversationModelFailureDetails(err error) agentsdk.ConversationModelFailureDetails {
	if err == nil || errors.Is(err, context.Canceled) {
		return agentsdk.ConversationModelFailureDetails{}
	}
	var provider agentsdk.ConversationModelFailureProvider
	if errors.As(err, &provider) {
		details := provider.ConversationModelFailureDetails()
		if details.ErrorCode == "" {
			details.ErrorCode = conversationModelFailureCode(err, "provider_failed")
		}
		return details
	}
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		return agentsdk.ConversationModelFailureDetails{Retryable: coded.Retryable, ErrorCode: conversationModelFailureCode(err, "provider_failed")}
	}
	return agentsdk.ConversationModelFailureDetails{ErrorCode: conversationModelFailureCode(err, "provider_failed")}
}

func (s *ConversationService) conversationModelRetryDelay(attempt int, details agentsdk.ConversationModelFailureDetails) time.Duration {
	delay := s.options.ModelRetryBaseDelay
	for value := 1; value < attempt && delay < s.options.ModelRetryMaxDelay; value++ {
		delay = min(s.options.ModelRetryMaxDelay, delay*2)
	}
	if details.RetryAfter > delay {
		delay = details.RetryAfter
	}
	return min(delay, s.options.ModelRetryMaxDelay)
}

func (s *ConversationService) waitConversationModelRetry(ctx context.Context, until time.Time) error {
	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func latestConversationModelAttempt(run agentsdk.ConversationRun, step int) *agentsdk.ConversationModelAttempt {
	for index := len(run.ModelAttempts) - 1; index >= 0; index-- {
		attempt := &run.ModelAttempts[index]
		if attempt.Step == step && attempt.RunAttempt == run.Attempt {
			return attempt
		}
	}
	return nil
}

func (s *ConversationService) waitPendingConversationModelRetry(ctx context.Context, claim persistence.ConversationClaim, step int) error {
	latest := latestConversationModelAttempt(claim.Run, step)
	if latest == nil || latest.Status != "retry_scheduled" || latest.RetryAt == nil {
		return nil
	}
	return s.waitConversationModelRetry(ctx, *latest.RetryAt)
}

func (s *ConversationService) beginConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, step int) (agentsdk.ConversationModelAttempt, error) {
	repo, ok := s.repo.(persistence.ConversationModelAttemptRepository)
	if !ok {
		if step >= 0 {
			if err := s.repo.AppendEvent(ctx, claim, "step.attempt.started", map[string]any{"step": step, "attempt": claim.Run.Attempt, "reset": true}); err != nil {
				return agentsdk.ConversationModelAttempt{}, err
			}
		}
		return agentsdk.ConversationModelAttempt{Step: step, RunAttempt: claim.Run.Attempt, Number: 1, Status: "started", StartedAt: time.Now().UTC()}, nil
	}
	if err := s.waitPendingConversationModelRetry(ctx, claim, step); err != nil {
		return agentsdk.ConversationModelAttempt{}, err
	}
	return repo.BeginConversationModelAttempt(ctx, claim, step)
}

func (s *ConversationService) failConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, attempt agentsdk.ConversationModelAttempt, cause error) (time.Time, bool, error) {
	// Graceful shutdown and explicit cancellation leave the claimed request
	// unfinished. The claim fence or cancellation transaction decides recovery;
	// do not turn a committed stream fragment into a provider failure here.
	if errors.Is(cause, context.Canceled) {
		return time.Time{}, false, nil
	}
	details := conversationModelFailureDetails(cause)
	event := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleModelFailed)
	event.Step = attempt.Step
	event.ModelAttempt = attempt.Number
	event.Purpose = conversationLifecycleModelPurpose(attempt.Step)
	event.ModelKey = s.conversationLifecycleModelKey(ctx)
	event.Failure = &agentsdk.ConversationLifecycleFailure{ErrorCode: details.ErrorCode, Retryable: details.Retryable, RetryAfterMilliseconds: details.RetryAfter.Milliseconds()}
	decision, lifecycleErr := s.dispatchConversationLifecycle(ctx, event)
	repo, ok := s.repo.(persistence.ConversationModelAttemptRepository)
	if !ok {
		if lifecycleErr != nil {
			return time.Time{}, false, lifecycleErr
		}
		return time.Time{}, false, nil
	}
	maxAttempts := s.options.MaxModelAttempts
	retry := details.Retryable && attempt.Number < maxAttempts
	if decision.Retry != nil {
		if !decision.Retry.Retry {
			retry = false
		}
		if decision.Retry.MaxAttempts > 0 {
			maxAttempts = min(maxAttempts, decision.Retry.MaxAttempts)
			retry = retry && attempt.Number < maxAttempts
		}
	}
	if lifecycleErr != nil {
		retry = false
	}
	var retryAt *time.Time
	if retry {
		delay := s.conversationModelRetryDelay(attempt.Number, details)
		if decision.Retry != nil && time.Duration(decision.Retry.DelayMilliseconds)*time.Millisecond > delay {
			delay = min(s.options.ModelRetryMaxDelay, time.Duration(decision.Retry.DelayMilliseconds)*time.Millisecond)
		}
		value := time.Now().UTC().Add(delay)
		if deadline, ok := ctx.Deadline(); ok && !value.Before(deadline) {
			retry = false
		} else {
			retryAt = &value
		}
	}
	if err := repo.FailConversationModelAttempt(context.WithoutCancel(ctx), claim, attempt.Step, attempt.Number, details, retryAt); err != nil {
		return time.Time{}, false, err
	}
	if lifecycleErr != nil {
		return time.Time{}, false, lifecycleErr
	}
	if retryAt == nil {
		return time.Time{}, false, nil
	}
	retryEvent := conversationLifecycleEventForClaim(claim, agentsdk.ConversationLifecycleModelRetry)
	retryEvent.Step = attempt.Step
	retryEvent.ModelAttempt = attempt.Number
	retryEvent.Purpose = conversationLifecycleModelPurpose(attempt.Step)
	retryEvent.ModelKey = s.conversationLifecycleModelKey(ctx)
	retryEvent.Failure = &agentsdk.ConversationLifecycleFailure{ErrorCode: details.ErrorCode, Retryable: true, RetryAfterMilliseconds: max(int64(0), time.Until(*retryAt).Milliseconds())}
	_, _ = s.dispatchConversationLifecycle(ctx, retryEvent)
	return *retryAt, true, nil
}

func (s *ConversationService) completeConversationModelAttempt(ctx context.Context, claim persistence.ConversationClaim, attempt agentsdk.ConversationModelAttempt, usage map[string]any) error {
	repo, ok := s.repo.(persistence.ConversationModelAttemptRepository)
	if !ok {
		return nil
	}
	return repo.CompleteConversationModelAttempt(ctx, claim, attempt.Step, attempt.Number, usage)
}

func (s *ConversationService) generateConversationSummary(ctx context.Context, claim persistence.ConversationClaim, step int, input agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	for {
		if err := s.authorizeConversationClaim(ctx, claim, "summary"); err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		hydrated, err := s.hydrateConversationModelRequest(ctx, claim, input)
		if err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		attempt, err := s.beginConversationModelAttempt(ctx, claim, step)
		if err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		if err = s.dispatchConversationModelRequestLifecycle(ctx, claim, attempt, &input, nil); err != nil {
			_, _, recordErr := s.failConversationModelAttempt(ctx, claim, attempt, err)
			if recordErr != nil {
				return agentsdk.ConversationModelResult{}, recordErr
			}
			return agentsdk.ConversationModelResult{}, err
		}
		modelCtx, cancel := s.externalCallContext(ctx, 0)
		result, callErr := s.conversationModel(ctx).GenerateConversation(modelCtx, hydrated)
		cancel()
		if callErr == nil {
			if err = s.completeConversationModelAttempt(ctx, claim, attempt, result.Usage); err != nil {
				return agentsdk.ConversationModelResult{}, err
			}
			s.dispatchConversationModelCompletedLifecycle(ctx, claim, attempt, result.Model, result.Usage)
			return result, nil
		}
		retryAt, retry, recordErr := s.failConversationModelAttempt(ctx, claim, attempt, callErr)
		if recordErr != nil {
			return agentsdk.ConversationModelResult{}, recordErr
		}
		if !retry {
			return agentsdk.ConversationModelResult{}, callErr
		}
		if err = s.waitConversationModelRetry(ctx, retryAt); err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
	}
}
