package agentsdk

import (
	"context"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	"time"
)

// Conversation is the durable chat capability, with an optional execution port. It is independent of
// InteractiveRunner (one-shot business routing) and TaskRunner (background work).
const CapabilityConversationV1 = "conversation.v1"
const CapabilityConversationStreamV1 = "conversation.stream.v1"

type ConversationAuthority = toolsdk.Authority

type Conversation struct {
	DelegationID  string    `json:"delegation_id,omitempty"`
	AgentID       string    `json:"agent_id,omitempty"`
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	RuntimeID     string    `json:"runtime_id"`
	WorkspaceID   string    `json:"workspace_id"`
	UserID        string    `json:"user_id"`
	Archived      bool      `json:"archived"`
	MemoryEnabled bool      `json:"memory_enabled"`
	LastSeq       int64     `json:"last_seq"`
	ActiveRunID   string    `json:"active_run_id,omitempty"`
	SummaryID     string    `json:"summary_id,omitempty"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ConversationMessage struct {
	PeerEvent        *ConversationPeerEvent `json:"peer_event,omitempty"`
	Citations        []ConversationCitation `json:"citations,omitempty"`    // current, authorized read projection
	AccessError      string                 `json:"access_error,omitempty"` // read projection; original content is retained internally
	InteractionID    string                 `json:"interaction_id,omitempty"`
	ID               string                 `json:"id"`
	ConversationID   string                 `json:"conversation_id"`
	RunID            string                 `json:"run_id"`
	Seq              int64                  `json:"seq"`
	Role             string                 `json:"role"`
	Content          string                 `json:"content"`
	BackgroundTaskID string                 `json:"background_task_id,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
}

type ConversationRun struct {
	Agent              *ConversationAgentSnapshot `json:"agent,omitempty"`
	AccessError        string                     `json:"access_error,omitempty"` // source data is withheld from this projection
	ID                 string                     `json:"id"`
	ConversationID     string                     `json:"conversation_id"`
	ClientMessageID    string                     `json:"client_message_id"`
	RequestHash        string                     `json:"-"`
	Status             string                     `json:"status"` // queued, running, completed, failed, cancelled
	UserSeq            int64                      `json:"user_seq"`
	AssistantMessageID string                     `json:"assistant_message_id,omitempty"`
	Attempt            int                        `json:"attempt"`
	// Draft belongs to Attempt and is never included in conversation history.
	DraftText      string                     `json:"draft_text,omitempty"`
	DraftBytes     int                        `json:"draft_bytes"`
	LastEventSeq   int64                      `json:"last_event_seq"`
	Steps          []ConversationStepView     `json:"steps,omitempty"`
	Interaction    *ConversationInteraction   `json:"interaction,omitempty"`
	LastInputSeq   int64                      `json:"last_input_seq,omitempty"`
	WriteScope     *ConversationWriteScope    `json:"write_scope,omitempty"`
	BackgroundTask *ConversationTaskExecution `json:"background_task,omitempty"`
	Model          string                     `json:"model,omitempty"`
	Usage          map[string]any             `json:"usage,omitempty"`
	// CorrelationID is the stable Agent run identity passed to every tool owner.
	// Audit is a bounded, safe projection of execution facts; it never contains
	// model text, tool arguments/results, credentials, or authorization evidence.
	CorrelationID             string                      `json:"correlation_id,omitempty"`
	Metrics                   ConversationRunMetrics      `json:"metrics"`
	Audit                     []ConversationRunAuditEvent `json:"audit,omitempty"`
	AuditComplete             bool                        `json:"audit_complete"`
	StartedAt                 *time.Time                  `json:"started_at,omitempty"`
	CompletedAt               *time.Time                  `json:"completed_at,omitempty"`
	DurationMilliseconds      int64                       `json:"duration_ms"`
	QueueDurationMilliseconds int64                       `json:"queue_duration_ms"`
	ErrorCode                 string                      `json:"error_code,omitempty"`
	CreatedAt                 time.Time                   `json:"created_at"`
	UpdatedAt                 time.Time                   `json:"updated_at"`
}

type ConversationRunMetrics struct {
	Steps                 int `json:"steps"`
	ModelCalls            int `json:"model_calls"`
	ToolCalls             int `json:"tool_calls"`
	ToolAttempts          int `json:"tool_attempts"`
	ParallelToolBatches   int `json:"parallel_tool_batches"`
	ParallelToolCalls     int `json:"parallel_tool_calls"`
	PeakParallelTools     int `json:"peak_parallel_tools"`
	AuthorizationChecks   int `json:"authorization_checks"`
	ConfirmationDecisions int `json:"confirmation_decisions"`
}

// ConversationExecutionLimits are deployment-owned admission limits. Agent
// persistence applies them atomically for the exact runtime/workspace/user
// scope; callers cannot select a different scope through request payloads.
type ConversationExecutionLimits struct {
	MaxQueuedPerUser       int `json:"max_queued_per_user"`
	MaxQueuedPerWorkspace  int `json:"max_queued_per_workspace"`
	MaxRunningPerUser      int `json:"max_running_per_user"`
	MaxRunningPerWorkspace int `json:"max_running_per_workspace"`
}

// ConversationExecutionCapacity is a current aggregate. It contains counts
// only and does not expose another user's runs or task contents.
type ConversationExecutionCapacity struct {
	UserQueued       int `json:"user_queued"`
	WorkspaceQueued  int `json:"workspace_queued"`
	UserRunning      int `json:"user_running"`
	WorkspaceRunning int `json:"workspace_running"`
}

type ConversationRunAuditEvent struct {
	Seq                   int64     `json:"seq"`
	Type                  string    `json:"type"` // run, model, tool, authorization, confirmation
	Status                string    `json:"status"`
	Step                  int       `json:"step"`
	Attempt               int       `json:"attempt,omitempty"`
	CallID                string    `json:"call_id,omitempty"`
	Tool                  string    `json:"tool,omitempty"`
	ActionKey             string    `json:"action_key,omitempty"`
	AuthorizationRevision string    `json:"authorization_revision,omitempty"`
	InteractionID         string    `json:"interaction_id,omitempty"`
	ActorID               string    `json:"actor_id,omitempty"`
	ErrorCode             string    `json:"error_code,omitempty"`
	DurationMilliseconds  int64     `json:"duration_ms,omitempty"`
	OccurredAt            time.Time `json:"occurred_at"`
}

func (r ConversationRun) Terminal() bool {
	return r.Status == "completed" || r.Status == "failed" || r.Status == "cancelled"
}
func (r ConversationRun) Waiting() bool {
	return r.Status == "waiting_user" || r.Status == "waiting_confirmation" || r.Status == "needs_reconciliation"
}

type ConversationEvent struct {
	RunID     string         `json:"run_id"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// The summary is a replaceable context projection. Original messages remain the
// source of truth, and through_seq is a complete-turn boundary.
type ConversationSummary struct {
	Sources        *ConversationSources       `json:"sources,omitempty"`
	Rebuild        bool                       `json:"rebuild,omitempty"` // trusted worker rebuilds from original messages after source access changes
	ID             string                     `json:"id"`
	ConversationID string                     `json:"conversation_id"`
	PreviousID     string                     `json:"previous_id,omitempty"`
	ThroughSeq     int64                      `json:"through_seq"`
	Content        ConversationSummaryContent `json:"content"`
	SourceHash     string                     `json:"source_hash"`
	Model          string                     `json:"model"`
	Version        int                        `json:"version"`
	CreatedAt      time.Time                  `json:"created_at"`
}

type ConversationSummaryContent struct {
	Goal        string   `json:"goal"`
	Constraints []string `json:"constraints"`
	Facts       []string `json:"facts"`
	Decisions   []string `json:"decisions"`
	OpenItems   []string `json:"open_items"`
}

// Memories are explicitly authored preferences, never automatically extracted
// by a tool-free conversation model.
type ConversationMemory struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Enabled   bool      `json:"enabled"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ConversationCreate struct {
	AgentID       string `json:"agent_id,omitempty"`
	ClientID      string `json:"client_id"`
	Title         string `json:"title,omitempty"`
	MemoryEnabled bool   `json:"memory_enabled"`
}
type ConversationUpdate struct {
	ExpectedRevision int64   `json:"expected_revision"`
	Title            *string `json:"title,omitempty"`
	Archived         *bool   `json:"archived,omitempty"`
	MemoryEnabled    *bool   `json:"memory_enabled,omitempty"`
}
type ConversationSend struct {
	// ExecutionAgent is prepared by the application, never accepted from JSON.
	ExecutionAgent  *ConversationAgentSnapshot `json:"-"`
	ClientMessageID string                     `json:"client_message_id"`
	Message         string                     `json:"message"`
	WriteScope      *ConversationWriteScope    `json:"write_scope,omitempty"`
}

// A scope is explicitly submitted by the authenticated user and frozen on one
// run. It never grants Identity permissions or access to another owner's data.
// PersonalMemory covers creating, updating, disabling and deleting personal
// memories for this request; it does not authorize unrelated business effects.
type ConversationWriteScope struct {
	PersonalMemory    bool `json:"personal_memory"`
	PersonalTodos     bool `json:"personal_todos,omitempty"`
	PersonalArtifacts bool `json:"personal_artifacts,omitempty"`
	BackgroundTasks   bool `json:"background_tasks,omitempty"`
}

// Grants are resource-specific. A memory grant must never authorize a todo or
// an unrelated future write tool merely because it is registered as personal.
func (s *ConversationWriteScope) Allows(tool string) bool {
	if s == nil {
		return false
	}
	switch tool {
	case "memory_save", "memory_forget":
		return s.PersonalMemory
	case "todo_create", "todo_update", "todo_delete":
		return s.PersonalTodos
	case "artifact_create", "artifact_edit", "artifact_export":
		return s.PersonalArtifacts
	case "task_start", "task_cancel", "task_resume", "task_update", "task_review", "plan_update", "completion_submit":
		return s.BackgroundTasks
	default:
		return false
	}
}

type ConversationQuery struct {
	Search          string `json:"search,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	BeforeID        string `json:"before_id,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}
type ConversationMessageQuery struct {
	BeforeSeq int64 `json:"before_seq,omitempty"`
	AfterSeq  int64 `json:"after_seq,omitempty"`
	Limit     int   `json:"limit,omitempty"`
}
type ConversationPage struct {
	Items      []Conversation `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type ConversationMessagePage struct {
	Items         []ConversationMessage `json:"items"`
	NextBeforeSeq int64                 `json:"next_before_seq,omitempty"`
	NextAfterSeq  int64                 `json:"next_after_seq,omitempty"`
}
type ConversationEventPage struct {
	Items    []ConversationEvent `json:"items"`
	NextSeq  int64               `json:"next_seq"`
	Terminal bool                `json:"terminal"`
}
type ConversationMemoryWrite struct {
	ID               string `json:"id,omitempty"`
	Title            string `json:"title"`
	Content          string `json:"content"`
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type ConversationService interface {
	Create(context.Context, ConversationCreate, ConversationAuthority) (Conversation, error)
	List(context.Context, ConversationQuery, ConversationAuthority) (ConversationPage, error)
	Get(context.Context, string, ConversationAuthority) (Conversation, error)
	Update(context.Context, string, ConversationUpdate, ConversationAuthority) (Conversation, error)
	Delete(context.Context, string, int64, ConversationAuthority) error
	Send(context.Context, string, ConversationSend, ConversationAuthority) (ConversationRun, error)
	Messages(context.Context, string, ConversationMessageQuery, ConversationAuthority) (ConversationMessagePage, error)
	Run(context.Context, string, string, ConversationAuthority) (ConversationRun, error)
	Events(context.Context, string, string, int64, int, ConversationAuthority) (ConversationEventPage, error)
	Cancel(context.Context, string, string, ConversationAuthority) (ConversationRun, error)
	Resume(context.Context, string, string, ConversationAuthority) (ConversationRun, error)
	Memories(context.Context, ConversationAuthority) ([]ConversationMemory, error)
	WriteMemory(context.Context, ConversationMemoryWrite, ConversationAuthority) (ConversationMemory, error)
	DeleteMemory(context.Context, string, int64, ConversationAuthority) error
}

// Optional extension: existing Binding implementations need not implement it.
type ConversationBinding interface{ Conversations() ConversationService }

type ConversationModelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type ConversationModelRequest struct {
	Sources        *ConversationSources       `json:"sources,omitempty"` // server-only; providers receive Messages
	Messages       []ConversationModelMessage `json:"messages"`
	Purpose        string                     `json:"purpose"` // reply or summary; neither permits tools
	IdempotencyKey string                     `json:"idempotency_key"`
	MaxOutputBytes int                        `json:"max_output_bytes"`
}
type ConversationModelResult struct {
	Content string         `json:"content"`
	Model   string         `json:"model,omitempty"`
	Usage   map[string]any `json:"usage,omitempty"`
}
type ConversationModel interface {
	GenerateConversation(context.Context, ConversationModelRequest) (ConversationModelResult, error)
}

// ConversationFactory lets a host discover explicit conversation configuration
// before opening modules. Persistent conversations do not require a legacy
// Agent, Task, Skill or entrypoint definition in the business manifest.
type ConversationFactory interface {
	ConversationEnabled() bool
}

// Optional extension for reply generation. The callback is synchronous and
// sequential. Implementations must stop on cancellation or callback error and
// return the exact concatenation of accepted deltas only on normal completion.
// Summarization continues to use GenerateConversation.
type ConversationStreamingModel interface {
	StreamConversation(context.Context, ConversationModelRequest, func(string) error) (ConversationModelResult, error)
}

// Readiness does not call a paid model endpoint. It checks local configuration,
// persistence and worker lifecycle; generation failures remain run failures.
type ConversationStatusProvider interface {
	ConversationReady(context.Context) error
	ConversationStreaming() bool
}

// ConversationCapacityProvider is optional so older hosts can keep the base
// conversation contract. Production Agent stores implement this projection.
type ConversationCapacityProvider interface {
	ConversationExecutionCapacity(context.Context, ConversationAuthority) (ConversationExecutionCapacity, error)
}
