package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
)

// History is an owner-scoped snapshot, not a dependency authorization graph.
// Preserve direct execution locators for recovery and tracing without reopening
// historical business records. New operations are authorized by their source API.
// The final identity check still gates the whole read if membership changes.
func (s *ConversationService) modelHistorySources(ctx context.Context, history []sdk.ConversationMessage) (roots [][]sdk.ConversationRunReference, err error) {
	ctx, finish := s.beginSourceAuthorizationEvaluation(ctx)
	defer func() {
		if checkErr := finish(); err == nil && checkErr != nil {
			roots, err = nil, checkErr
		}
	}()
	roots = make([][]sdk.ConversationRunReference, len(history))
	for i, message := range history {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if message.Role != "assistant" || message.RunID == "" {
			continue
		}
		roots[i] = []sdk.ConversationRunReference{{ConversationID: message.ConversationID, RunID: message.RunID}}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return roots, nil
}
