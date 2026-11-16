package execution

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// TaskBudget is an Owner snapshot. Durable reservations remain authoritative;
// the native runner uses this snapshot plus its completed calls for planning.
type TaskBudget struct {
	Limit Budget
	Used  Usage
}

type taskBudgetContextKey struct{}

func WithTaskBudget(ctx context.Context, budget TaskBudget) context.Context {
	return context.WithValue(ctx, taskBudgetContextKey{}, budget)
}

func TaskBudgetFromContext(ctx context.Context) (TaskBudget, bool) {
	budget, ok := ctx.Value(taskBudgetContextKey{}).(TaskBudget)
	return budget, ok
}

func TaskCostBudgetUnits(budget string) int {
	switch strings.ToLower(strings.TrimSpace(budget)) {
	case "low":
		return 5
	case "high":
		return 50
	default:
		return 20
	}
}

func TaskToolCostUnits(tool string) int {
	switch strings.TrimSpace(tool) {
	case agentsdk.AgentToolInvokeAction:
		return 5
	case agentsdk.AgentToolQueryRecords:
		return 2
	default:
		return 1
	}
}
