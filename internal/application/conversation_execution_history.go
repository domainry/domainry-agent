package application

import (
	"context"
	"sort"

	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Cohesive optional owner port: a native store verifies one current lease and
// reads the bounded run history in one transaction, rather than N transactions
// per step plus N queries per tool. No authorization decisions are cached.
type conversationExecutionHistoryRepository interface {
	ExecutionHistory(context.Context, persistence.ConversationClaim, int) ([]persistence.ConversationExecutionStep, []persistence.ConversationToolExecution, error)
}

func (s *ConversationService) executionHistory(ctx context.Context, claim persistence.ConversationClaim, before int) (map[int]persistence.ConversationExecutionStep, map[int][]persistence.ConversationToolExecution, error) {
	steps := map[int]persistence.ConversationExecutionStep{}
	tools := map[int][]persistence.ConversationToolExecution{}
	if before == 0 {
		return steps, tools, nil
	}
	if repo, ok := s.repo.(conversationExecutionHistoryRepository); ok {
		values, records, err := repo.ExecutionHistory(ctx, claim, before)
		if err != nil {
			return nil, nil, err
		}
		for _, value := range values {
			if _, duplicate := steps[value.Number]; duplicate || value.Number < 0 || value.Number >= before {
				return nil, nil, conversationFailure("conflict", "tool_result_invalid")
			}
			steps[value.Number] = value
		}
		for _, record := range records {
			tools[record.Step] = append(tools[record.Step], record)
		}
		return steps, tools, nil
	}
	repo := s.repo.(persistence.ConversationExecutionRepository)
	for number := 0; number < before; number++ {
		value, found, err := repo.ExecutionStep(ctx, claim, number, nil)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, conversationFailure("conflict", "tool_result_invalid")
		}
		steps[number] = value
		tools[number], err = repo.ExecutionTools(ctx, claim, number)
		if err != nil {
			return nil, nil, err
		}
		if codeRepo, ok := s.repo.(persistence.ConversationCodeExecutionRepository); ok && value.Result != nil {
			for _, call := range value.Result.Message.ToolCalls {
				if call.Name != "run_code" {
					continue
				}
				children, err := codeRepo.ExecutionSubtools(ctx, claim, number, call.ID)
				if err != nil {
					return nil, nil, err
				}
				tools[number] = append(tools[number], children...)
			}
		}
	}
	return steps, tools, nil
}

func executionHistoryChildren(records []persistence.ConversationToolExecution, parent string) []persistence.ConversationToolExecution {
	children := []persistence.ConversationToolExecution{}
	for _, record := range records {
		if record.ParentCallID == parent {
			children = append(children, record)
		}
	}
	sort.Slice(children, func(i, j int) bool { return children[i].DispatchIndex < children[j].DispatchIndex })
	return children
}
