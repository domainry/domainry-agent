package application

import (
	"context"
	"sync/atomic"
)

type conversationSourceEvaluationKey struct{ service *ConversationService }
type conversationSourceEvaluation struct {
	id     uint64
	closed atomic.Bool
}

var conversationSourceEvaluationSequence atomic.Uint64

func (s *ConversationService) sourceEvaluationID(ctx context.Context) uint64 {
	if boundary, _ := ctx.Value(conversationSourceEvaluationKey{s}).(*conversationSourceEvaluation); boundary != nil {
		return boundary.id
	}
	return 0
}

func (s *ConversationService) beginSourceAuthorizationEvaluation(ctx context.Context) (context.Context, func() error) {
	if current, _ := ctx.Value(conversationSourceEvaluationKey{s}).(*conversationSourceEvaluation); current != nil && !current.closed.Load() {
		return ctx, func() error { return nil }
	}
	if s.options.BeginSourceAuthorizationScope == nil {
		return ctx, func() error { return nil }
	}
	ctx, finish := s.options.BeginSourceAuthorizationScope(ctx)
	boundary := &conversationSourceEvaluation{id: conversationSourceEvaluationSequence.Add(1)}
	return context.WithValue(ctx, conversationSourceEvaluationKey{s}, boundary), func() error {
		if !boundary.closed.CompareAndSwap(false, true) {
			return nil
		}
		return finish()
	}
}
