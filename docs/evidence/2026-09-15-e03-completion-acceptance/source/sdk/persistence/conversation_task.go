package persistence

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// ConversationTaskMutationRepository atomically saves task_start with its
// frozen tool call receipt and completion event.
type ConversationTaskMutationRepository interface {
	ApplyConversationTaskTool(context.Context, agentsdk.ConversationToolRequest, agentsdk.ConversationTask) (agentsdk.ConversationToolResult, error)
}

// ScheduledConversationTaskMutationRepository atomically accepts one trusted
// Scheduler window into the existing Agent task queue. Implementations key
// exact replay by owner and IdempotencyKey and must reject changed content.
type ScheduledConversationTaskMutationRepository interface {
	AcceptScheduledConversationTask(context.Context, agentsdk.ScheduledConversationTaskRequest, agentsdk.ConversationTask) (agentsdk.ScheduledConversationTaskReceipt, error)
}

// ConversationTaskWorkerRepository is an Agent-owned queue boundary. Launch
// creates a normal conversation run so the existing worker, lease, execution
// ledger and current authorization checks remain the only execution path.
type ConversationTaskWorkerRepository interface {
	LaunchConversationTask(context.Context, string) (ConversationTaskLaunch, bool, error)
	ConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error)
}

// ConversationFollowUpEventClaim is a leased Agent outbox item. Publishing is
// at-least-once; the event ID is stable so the downstream notification owner
// can deduplicate a retry after a process crash.
type ConversationFollowUpEventClaim struct {
	Event agentsdk.ConversationFollowUpEvent
	Owner string
	Fence int64
}

type ConversationFollowUpEventRepository interface {
	ClaimConversationFollowUpEvent(context.Context, string, string, time.Duration) (ConversationFollowUpEventClaim, bool, error)
	CompleteConversationFollowUpEvent(context.Context, ConversationFollowUpEventClaim) error
	ReleaseConversationFollowUpEvent(context.Context, ConversationFollowUpEventClaim) error
}

// ConversationTaskReadRepository returns owner-scoped executor records. The
// application layer reauthorizes and projects runs, results and artifacts.
type ConversationTaskReadRepository interface {
	ConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error)
	ConversationTasks(context.Context, agentsdk.ConversationTaskQuery, agentsdk.ConversationAuthority) (ConversationTaskRecordPage, error)
}

// ConversationTaskControlRepository owns transitions that have no executing
// run yet. Running task transitions remain atomic with ConversationRun events.
type ConversationTaskControlRepository interface {
	CancelQueuedConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error)
	ResumeQueuedConversationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error)
}

type ConversationTaskAgreementRepository interface {
	UpdateConversationTaskAgreement(context.Context, string, agentsdk.ConversationTaskAgreementUpdate, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, bool, error)
}

// ConversationTaskPlanRepository atomically commits one model-authored plan
// version with its tool receipt. Plan bodies contain references only; artifact
// content remains in Knowledge and business results remain in their ledgers.
type ConversationTaskPlanRepository interface {
	ApplyConversationTaskPlanTool(context.Context, agentsdk.ConversationToolRequest, agentsdk.ConversationPlan) (agentsdk.ConversationToolResult, error)
	ConversationTaskPlan(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationPlan, error)
	ConversationTaskPlans(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationPlanHistory, error)
}

// ConversationTaskCompletionRepository commits executing-Agent assessments,
// user reviews and immutable history against the current task agreement.
type ConversationTaskCompletionRepository interface {
	ApplyConversationTaskCompletionTool(context.Context, agentsdk.ConversationToolRequest, agentsdk.ConversationTaskCompletionRecord) (agentsdk.ConversationToolResult, error)
	ReviewConversationTaskCompletion(context.Context, string, agentsdk.ConversationTaskCompletionReviewRequest, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, bool, error)
	ConversationTaskCompletionHistory(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationTaskCompletionHistory, error)
}

type ConversationTaskRecordPage struct {
	Items      []agentsdk.ConversationTask
	NextCursor string
	Complete   bool
}

type ConversationTaskLaunch struct {
	Task      agentsdk.ConversationTask
	Run       agentsdk.ConversationRun
	Authority agentsdk.ConversationAuthority
}
