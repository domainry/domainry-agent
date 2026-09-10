package application

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) generateConversationReply(ctx context.Context, claim agentpersistence.ConversationClaim, input agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	streamer, ok := s.model.(agentsdk.ConversationStreamingModel)
	if !ok {
		return s.model.GenerateConversation(ctx, input)
	}
	streamCtx, cancel := context.WithCancel(ctx)
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
