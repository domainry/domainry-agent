package persistence

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Execution records are internal persistence contracts. Product APIs expose
// explicit views so model input and opaque provider state stay server-side.
type ConversationExecutionStep struct {
	Number    int                              `json:"number"`
	Input     agentsdk.ConversationStepRequest `json:"input"`
	Result    *agentsdk.ConversationStepResult `json:"result,omitempty"`
	CreatedAt time.Time                        `json:"created_at"`
	UpdatedAt time.Time                        `json:"updated_at"`
}

type ConversationToolInspection struct {
	Token       string     `json:"token"`
	ClientID    string     `json:"client_id"`
	ExpiresAt   time.Time  `json:"expires_at"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ActorID     string     `json:"actor_id"`
}

type ConversationToolExecution struct {
	ReusedFrom     *agentsdk.ConversationResultReference  `json:"reused_from,omitempty"`
	Inspection     *ConversationToolInspection            `json:"inspection,omitempty"`
	Step           int                                    `json:"step"`
	Call           agentsdk.ConversationToolCall          `json:"call"`
	Definition     agentsdk.ConversationToolDefinition    `json:"definition"`
	IdempotencyKey string                                 `json:"idempotency_key"`
	Authorization  agentsdk.ConversationToolAuthorization `json:"authorization"`
	State          string                                 `json:"state"` // started, uncertain or completed
	Result         *agentsdk.ConversationToolResult       `json:"result,omitempty"`
	CreatedAt      time.Time                              `json:"created_at"`
	UpdatedAt      time.Time                              `json:"updated_at"`
	// Internal receipt guard for the most recent invocation/reconciliation.
	// Never expose these fields to a browser or model.
	LeaseOwner string `json:"lease_owner,omitempty"`
	Fence      int64  `json:"fence,omitempty"`
}

// Optional repository extension; every mutation validates the current run
// lease/fence and commits its event with the record in one transaction.
// FinishExecutionTool may also settle the original in-flight receipt after
// cancellation, while the cancelled run still directly follows that lease.
// This exception must not permit new effects, model output, lease takeover,
// overwriting a completed receipt, or writes after resume/deletion.
type ConversationExecutionRepository interface {
	ExecutionStep(context.Context, ConversationClaim, int, *agentsdk.ConversationStepRequest) (ConversationExecutionStep, bool, error)
	CompleteExecutionStep(context.Context, ConversationClaim, int, agentsdk.ConversationStepResult) error
	ExecutionTools(context.Context, ConversationClaim, int) ([]ConversationToolExecution, error)
	BeginExecutionTool(context.Context, ConversationClaim, int, string, agentsdk.ConversationToolAuthorization) (ConversationToolExecution, bool, error)
	FinishExecutionTool(context.Context, ConversationClaim, int, string, agentsdk.ConversationToolResult) error
}

// ConversationWorkStepReservation serializes a whole-work allowance across
// concurrently delegated Agents. Completion replaces conservative reserved
// input with the provider's actual usage. InputTokenUpperBound is the exact
// serialized provider-request byte count, which is a conservative token bound
// even when the provider does not expose a tokenizer before the call.
type ConversationWorkStepReservation struct {
	InputTokenUpperBound    int64                            `json:"input_token_upper_bound"`
	MaxOutputTokens         int64                            `json:"max_output_tokens"`
	MaxDurationMilliseconds int64                            `json:"max_duration_ms"`
	Price                   *agentsdk.ConversationModelPrice `json:"price,omitempty"`
}

type ConversationWorkBudgetRepository interface {
	ConversationWorkBudget(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationWorkBudget, agentsdk.ConversationWorkUsage, error)
	ReserveConversationWorkStep(context.Context, ConversationClaim, int, ConversationWorkStepReservation) (ConversationWorkStepReservation, error)
	ReleaseConversationWorkStep(context.Context, ConversationClaim, int) error
}

// Inspection retains the old run's terminal state. The token fences one exact
// receipt query; it is not a worker lease and grants no invocation rights.
type ConversationOutcomeInspection struct {
	Request agentsdk.ConversationToolRequest
	Record  ConversationToolExecution
	Run     agentsdk.ConversationRun
}
type ConversationOutcomeInspectionRepository interface {
	BeginConversationOutcomeInspection(context.Context, string, int64, agentsdk.ConversationOutcomeInspectionRequest, string, agentsdk.ConversationAuthority) (ConversationOutcomeInspection, error)
	FinishConversationOutcomeInspection(context.Context, ConversationOutcomeInspection, agentsdk.ConversationToolResult, agentsdk.ConversationAuthority) error
	VerifyConversationOutcomeInspection(context.Context, agentsdk.ConversationToolRequest) (bool, error)
}
