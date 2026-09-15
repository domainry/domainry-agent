package application

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) generateConversationReply(ctx context.Context, claim agentpersistence.ConversationClaim, input agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	for {
		if err := s.authorizeConversationClaim(ctx, claim, "model"); err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		hydrated, err := s.hydrateConversationModelRequest(ctx, claim, input)
		if err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		attempt, err := s.beginConversationModelAttempt(ctx, claim, -1)
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
		result, err := s.generateConversationReplyAttempt(ctx, claim, hydrated)
		if err == nil {
			if completeErr := s.completeConversationModelAttempt(ctx, claim, attempt, result.Usage); completeErr != nil {
				return agentsdk.ConversationModelResult{}, completeErr
			}
			s.dispatchConversationModelCompletedLifecycle(ctx, claim, attempt, result.Model, result.Usage)
			return result, nil
		}
		retryAt, retry, recordErr := s.failConversationModelAttempt(ctx, claim, attempt, err)
		if recordErr != nil {
			return agentsdk.ConversationModelResult{}, recordErr
		}
		if !retry {
			return agentsdk.ConversationModelResult{}, err
		}
		if waitErr := s.waitConversationModelRetry(ctx, retryAt); waitErr != nil {
			return agentsdk.ConversationModelResult{}, waitErr
		}
	}
}

func (s *ConversationService) generateConversationReplyAttempt(ctx context.Context, claim agentpersistence.ConversationClaim, input agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	streamer, ok := s.conversationModel(ctx).(agentsdk.ConversationStreamingModel)
	if !ok {
		modelCtx, cancel := s.externalCallContext(ctx, 0)
		defer cancel()
		return s.conversationModel(ctx).GenerateConversation(modelCtx, input)
	}
	streamCtx, cancel := s.externalCallContext(ctx, 0)
	defer cancel()
	var draft strings.Builder
	var deltaErr error
	result, err := streamer.StreamConversation(streamCtx, input, func(text string) error {
		if deltaErr != nil {
			return deltaErr
		}
		if err := streamCtx.Err(); err != nil {
			return err
		}
		if text == "" {
			return nil
		}
		limit := min(input.MaxOutputBytes, s.options.MaxOutputBytes)
		if !conversationText(text, limit, false) || len(text) > limit-draft.Len() {
			deltaErr = fmt.Errorf("invalid or oversized reply delta")
		} else {
			deltaErr = s.repo.AppendDelta(streamCtx, claim, draft.Len(), text)
		}
		if deltaErr != nil {
			cancel()
			return deltaErr
		}
		// Acknowledgment to the provider happens only after the draft and its
		// event commit atomically. A disconnected browser has no role here.
		draft.WriteString(text)
		return nil
	})
	if deltaErr != nil {
		return agentsdk.ConversationModelResult{}, deltaErr
	}
	if err != nil {
		return agentsdk.ConversationModelResult{}, err
	}
	if draft.Len() == 0 || result.Content != draft.String() {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("stream completed without matching committed deltas")
	}
	return result, nil
}
