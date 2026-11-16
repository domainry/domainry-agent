package provider

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// TaskReadCompletionPolicy is an optional trusted embedding policy. It checks
// only already-observed source results; it performs no tools and grants no
// authority. An incomplete check can request another existing-budget step.
type TaskReadCompletionPolicy interface {
	EvaluateTaskReadCompletion(context.Context, TaskReadCompletionInput) (TaskReadCompletionCheck, error)
}

type TaskReadCompletionInput struct {
	TaskKey, TaskVersion                                    string
	Input                                                   map[string]any
	Messages                                                []agentsdk.ConversationStepMessage
	RemainingCalls, RemainingCostUnits, RemainingModelSteps int
}

type TaskReadCompletionCheck struct {
	Complete      bool
	PendingGroups int
	Instruction   string
}
