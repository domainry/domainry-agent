package agentsdk

import (
	"context"
	"encoding/json"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	"time"
)

// Conversation execution is an optional capability. The existing text-only
// ConversationModel and Interactive/Task contracts retain their semantics.
const CapabilityConversationExecutionV1 = "conversation.execution.v1"

type ConversationExecutionStatusProvider interface{ ConversationExecutionEnabled() bool }

// Bounded public projections. These never contain model input snapshots,
// policy bundles, credentials, or opaque provider continuation blocks.
type ConversationOutcomeInspectionView struct {
	Status    string    `json:"status"`
	ActorID   string    `json:"actor_id"`
	CheckedAt time.Time `json:"checked_at"`
}

type ConversationToolView struct {
	AccessError          string                             `json:"access_error,omitempty"`
	ReusedFrom           *ConversationResultReference       `json:"reused_from,omitempty"`
	OutcomeInspection    *ConversationOutcomeInspectionView `json:"outcome_inspection,omitempty"`
	Effect               string                             `json:"effect,omitempty"`     // trusted registration: read or write
	Completion           string                             `json:"completion,omitempty"` // accepted: invocation acknowledged; business work remains pending
	Citations            []ConversationCitation             `json:"citations,omitempty"`
	ID                   string                             `json:"id"`
	Name                 string                             `json:"name"`
	Arguments            string                             `json:"arguments"`
	Status               string                             `json:"status"`
	ErrorCode            string                             `json:"error_code,omitempty"`
	ResourceID           string                             `json:"resource_id,omitempty"`
	ResultPreview        string                             `json:"result_preview,omitempty"`
	ResultTruncated      bool                               `json:"result_truncated,omitempty"`
	ResultReference      *ConversationResultReference       `json:"result_reference,omitempty"`
	Authorization        *ConversationAuthorizationView     `json:"authorization,omitempty"`
	Confirmation         *ConversationConfirmationView      `json:"confirmation,omitempty"`
	StartedAt            *time.Time                         `json:"started_at,omitempty"`
	CompletedAt          *time.Time                         `json:"completed_at,omitempty"`
	DurationMilliseconds int64                              `json:"duration_ms"`
}
type ConversationStepView struct {
	Number               int                    `json:"number"`
	Attempt              int                    `json:"attempt"`
	Status               string                 `json:"status"`
	Text                 string                 `json:"text"`
	Calls                []ConversationToolView `json:"calls"`
	ToolExecution        string                 `json:"tool_execution,omitempty"`
	ParallelToolCalls    int                    `json:"parallel_tool_calls,omitempty"`
	Usage                map[string]any         `json:"usage,omitempty"`
	StartedAt            *time.Time             `json:"started_at,omitempty"`
	CompletedAt          *time.Time             `json:"completed_at,omitempty"`
	DurationMilliseconds int64                  `json:"duration_ms"`
}

type ConversationAuthorizationView struct {
	Status   string `json:"status"` // granted, confirmation_required, denied or failed
	Revision string `json:"revision,omitempty"`
	Checks   int    `json:"checks"`
}

type ConversationConfirmationView struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	RespondedBy string     `json:"responded_by,omitempty"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
}

// Tool definitions come from trusted host registration. A model chooses a key
// and arguments; it cannot supply the authorization, effect or retry policy.
type ConversationToolDefinition = toolsdk.Definition

type ConversationToolCall = toolsdk.Call

// ConversationParallelReadWidth returns the bounded worker count for a model
// step. Empty/legacy declarations, mixed batches and every write stay serial.
func ConversationParallelReadWidth(definitions []ConversationToolDefinition, calls []ConversationToolCall, limit int) int {
	if len(calls) < 2 || limit < 2 {
		return 1
	}
	byKey := make(map[string]ConversationToolDefinition, len(definitions))
	for _, definition := range definitions {
		byKey[definition.Key] = definition
	}
	for _, call := range calls {
		definition, exists := byKey[call.Name]
		if !exists || definition.Effect != "read" || definition.Parallelism != toolsdk.ToolParallelismIndependentRead {
			return 1
		}
	}
	return min(len(calls), limit)
}

// ProviderState carries protocol continuation blocks (e.g. signed thinking or
// encrypted reasoning). It is server-side execution state, never a browser
// input, tool result, memory, or user-visible reasoning transcript.
type ConversationStepMessage struct {
	Role          string                 `json:"role"`
	Content       string                 `json:"content"`
	ToolCalls     []ConversationToolCall `json:"tool_calls,omitempty"`
	ToolCallID    string                 `json:"tool_call_id,omitempty"`
	IsError       bool                   `json:"is_error,omitempty"`
	ProviderState json.RawMessage        `json:"provider_state,omitempty"`
	// Server-created linkage for compaction. Providers receive only Content;
	// model output must never supply or overwrite this field.
	ResultReference *ConversationResultReference `json:"result_reference,omitempty"`
}

type ConversationModelIdentity struct {
	Provider    string `json:"provider"`
	Protocol    string `json:"protocol"`
	Model       string `json:"model"`
	Fingerprint string `json:"fingerprint"` // Non-secret configuration identity for recovery.
}

// Optional pure sizing port for the exact serialized model request body.
// Internal authorization, provenance and receipt metadata stay in the frozen
// execution snapshot but do not count as input when the provider omits them.
// Implementations must use the same encoding as StreamConversationStep and
// must not perform network calls or effects. Hosts without this port retain
// conservative sizing of the complete SDK request.
type ConversationStepInputSizer interface {
	ConversationStepInputBytes(ConversationStepRequest) (int, error)
}

// ConversationModelOutputLimiter exposes the exact provider-side token cap so
// a delegated work ledger can reserve the same bound before a model call.
type ConversationModelOutputLimiter interface {
	ConversationModelMaxOutputTokens() int
}

type ConversationStepRequest struct {
	// ContextSources records server-assembled context provenance for later authorization checks.
	ContextSources   []ConversationRunReference   `json:"context_sources,omitempty"`
	InboxMessageIDs  []string                     `json:"inbox_message_ids,omitempty"`
	Messages         []ConversationStepMessage    `json:"messages"`
	Tools            []ConversationToolDefinition `json:"tools"`
	ModelIdentity    ConversationModelIdentity    `json:"model_identity"`
	IdempotencyKey   string                       `json:"idempotency_key"`
	MaxOutputBytes   int                          `json:"max_output_bytes"`
	MaxOutputTokens  int                          `json:"max_output_tokens,omitempty"`
	MaxArgumentBytes int                          `json:"max_argument_bytes"`
	MaxToolCalls     int                          `json:"max_tool_calls"`
	// Server-owned execution limit. Zero in an older frozen step means serial.
	MaxParallelTools int                            `json:"max_parallel_tools,omitempty"`
	Compaction       *ConversationContextCompaction `json:"compaction,omitempty"`
}

type ConversationStepResult struct {
	Message      ConversationStepMessage `json:"message"`
	FinishReason string                  `json:"finish_reason"` // stop or tool_calls
	Model        string                  `json:"model"`
	Usage        map[string]any          `json:"usage,omitempty"`
}

// Events are previews of one model step, not authorization to execute a tool.
// Only a normally completed StepResult can be submitted to the tool executor.
// Offset counts UTF-8 bytes within Text or this tool's argument stream.
type ConversationModelEvent struct {
	Type   string `json:"type"` // text.delta, tool.started, tool.arguments.delta
	Index  int    `json:"index,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Name   string `json:"name,omitempty"`
	Offset int    `json:"offset"`
	Delta  string `json:"delta,omitempty"`
}

// Callbacks are sequential and synchronous; a callback failure cancels the
// request. Incomplete/truncated streams must return an error and no result.
type ConversationAgentModel interface {
	ConversationModelIdentity() ConversationModelIdentity
	StreamConversationStep(context.Context, ConversationStepRequest, func(ConversationModelEvent) error) (ConversationStepResult, error)
}

// Execution authority is resolved by the host on each call, including resume.
// Granted means authorized now, and ConfirmationRequired is a separate policy
// decision; neither can be inferred from a tool's visibility in the catalog.
type ConversationToolAuthorization = toolsdk.Authorization

type ConversationToolRequest = toolsdk.Request

type ConversationToolResult = toolsdk.Result

// Narrow execution port for Conversation. It does not require a Workflow or
// an existing TaskDefinition; concrete business effects remain host-owned.
type ConversationToolHost interface {
	ConversationTools(context.Context, ConversationAuthority) ([]ConversationToolDefinition, error)
	AuthorizeConversationTool(context.Context, ConversationToolRequest) (ConversationToolAuthorization, error)
	InvokeConversationTool(context.Context, ConversationToolRequest) (ConversationToolResult, error)
	ReconcileConversationTool(context.Context, ConversationToolRequest) (ConversationToolResult, error)
}

type ConversationOutcomeInspector = toolsdk.OutcomeInspector

// Independent source-owned authorization of a persisted, released result.
// Access to its containing collaboration delivery must be authorized separately.
type ConversationToolResultReadAuthorizer = toolsdk.ResultReadAuthorizer
