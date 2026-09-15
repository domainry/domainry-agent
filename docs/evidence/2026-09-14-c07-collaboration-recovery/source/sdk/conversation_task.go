package agentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ConversationTaskStatusQueued = "queued"
const ConversationTaskStatusRunning = "running"
const ConversationTaskStatusCompleted = "completed"
const ConversationTaskStatusFailed = "failed"
const ConversationTaskStatusCancelled = "cancelled"
const ConversationTaskMaxChildrenPerRun = 4

const (
	CapabilityScheduledConversationTask       = "conversation.scheduled_task.start"
	ActionAgentScheduledConversationTaskStart = "agent.scheduled_conversation_task.start"
	ScheduledConversationTaskContractVersion  = "domainry-agent-scheduled-conversation-task-v1"
)

// ConversationTaskBudget is frozen when task_start is accepted. Every value is
// also bounded by the selected Agent profile and deployment configuration.
type ConversationTaskBudget struct {
	MaxSteps       int `json:"max_steps"`
	MaxToolCalls   int `json:"max_tool_calls"`
	MaxOutputBytes int `json:"max_output_bytes"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

// ConversationWorkBudget is shared by every delegated task rooted at one
// conversation. Transferring work or delegating another part cannot mint a
// fresh allowance. A nil cost limit leaves cost observable but token-bounded.
type ConversationWorkBudget struct {
	MaxInputTokens     int64    `json:"max_input_tokens"`
	MaxOutputTokens    int64    `json:"max_output_tokens"`
	MaxDurationSeconds int64    `json:"max_duration_seconds"`
	MaxModelCost       *float64 `json:"max_model_cost,omitempty"`
	Currency           string   `json:"currency,omitempty"`
}

// ConversationWorkUsage is the server-owned aggregate for one delegated work
// tree. CostKnown is false when any retained usage predates price accounting.
type ConversationWorkUsage struct {
	InputTokens          int64     `json:"input_tokens"`
	OutputTokens         int64     `json:"output_tokens"`
	DurationMilliseconds int64     `json:"duration_ms"`
	ModelCost            float64   `json:"model_cost"`
	Currency             string    `json:"currency,omitempty"`
	CostKnown            bool      `json:"cost_known"`
	StartedAt            time.Time `json:"started_at"`
}

// ConversationTaskToolScope records the exact tool contract and authorization
// revision that was allowed when a task was accepted. Execution still performs
// current authorization and availability checks before each use.
type ConversationTaskToolScope struct {
	Key                   string `json:"key"`
	Version               string `json:"version"`
	ActionKey             string `json:"action_key"`
	DefinitionHash        string `json:"definition_hash"`
	AuthorizationRevision string `json:"authorization_revision,omitempty"`
}

type ConversationTaskStart struct {
	Goal         string                     `json:"goal"`
	Input        string                     `json:"input"`
	AllowedTools []string                   `json:"allowed_tools"`
	Budget       ConversationTaskBudget     `json:"budget"`
	FollowUp     *ConversationFollowUpScope `json:"follow_up,omitempty"`
}

// ConversationFollowUpScope makes a scheduled follow-up explicit and bounded.
// Scheduler owns when it runs; Agent owns the observation protocol, change
// comparison and completion result. It is accepted only on trusted scheduled
// task requests, never on the ordinary task_start tool.
type ConversationFollowUpScope struct {
	CompletionCondition string `json:"completion_condition"`
}

const (
	ConversationFollowUpReportActive     = "active"
	ConversationFollowUpReportCompleted  = "completed"
	ConversationFollowUpEventChanged     = "changed"
	ConversationFollowUpEventCompleted   = "completed"
	ConversationFollowUpEventFailed      = "failed"
	ConversationFollowUpEventNeedsAction = "needs_action"
)

// ConversationFollowUpReport is the final, machine-readable result required
// from every successful follow-up run. Agent hashes Observation and compares it
// with the previous observation for the same owner and plan. Summary is the
// only model-authored text sent to Notification.
type ConversationFollowUpReport struct {
	Status      string `json:"status"`
	Observation string `json:"observation"`
	Summary     string `json:"summary"`
}

// ConversationFollowUpEvent is Agent's source-owned notification fact. It has
// no Notification event type, template, channel, recipient, route or provider
// fields; Runtime is the sole adapter to Notification SDK intents.
type ConversationFollowUpEvent struct {
	ID         string                `json:"id"`
	Kind       string                `json:"kind"`
	Authority  ConversationAuthority `json:"authority"`
	PlanID     string                `json:"plan_id"`
	TaskID     string                `json:"task_id"`
	RunID      string                `json:"run_id"`
	Goal       string                `json:"goal"`
	Summary    string                `json:"summary,omitempty"`
	Question   string                `json:"question,omitempty"`
	ErrorCode  string                `json:"error_code,omitempty"`
	Occurrence int                   `json:"occurrence"`
	OccurredAt time.Time             `json:"occurred_at"`
}

type ConversationFollowUpPublisher interface {
	PublishConversationFollowUp(context.Context, ConversationFollowUpEvent) error
}

// ConversationTask is an Agent-owned background execution requested from a
// durable conversation. Business effects remain owned by their tool services.
type ConversationTask struct {
	Handoff                 *ConversationDelegationHandoff `json:"handoff,omitempty"`
	StructuredInput         *ConversationStructuredInput   `json:"structured_input,omitempty"`
	InputSource             *ConversationRunReference      `json:"input_source,omitempty"`
	MaxInputBytes           int                            `json:"max_input_bytes,omitempty"`
	Dependencies            []ConversationTaskDependency   `json:"dependencies,omitempty"`
	AgreementRevision       int64                          `json:"agreement_revision,omitempty"`
	Requirements            ConversationAgentRequirements  `json:"requirements,omitempty"`
	Brief                   *ConversationTaskBrief         `json:"brief,omitempty"`
	Agent                   *ConversationAgentSnapshot     `json:"agent,omitempty"`
	DelegationID            string                         `json:"delegation_id,omitempty"`
	ExecutionConversationID string                         `json:"execution_conversation_id,omitempty"`
	ID                      string                         `json:"id"`
	Status                  string                         `json:"status"`
	Goal                    string                         `json:"goal"`
	Input                   string                         `json:"input"`
	ToolScope               []ConversationTaskToolScope    `json:"tool_scope"`
	Budget                  ConversationTaskBudget         `json:"budget"`
	FollowUp                *ConversationFollowUpScope     `json:"follow_up,omitempty"`
	SourceConversationID    string                         `json:"source_conversation_id"`
	SourceRunID             string                         `json:"source_run_id"`
	ExecutionRunID          string                         `json:"execution_run_id,omitempty"`
	ResultMessageID         string                         `json:"result_message_id,omitempty"`
	CompletionEventID       string                         `json:"completion_event_id,omitempty"`
	CompletionEventSeq      int64                          `json:"completion_event_seq,omitempty"`
	ErrorCode               string                         `json:"error_code,omitempty"`
	CreatedAt               time.Time                      `json:"created_at"`
	UpdatedAt               time.Time                      `json:"updated_at"`
	CompletedAt             *time.Time                     `json:"completed_at,omitempty"`
}

// ConversationTaskScheduleRef links an Agent task to the Scheduler window
// that requested it. These are correlation facts and never authorize the
// task, conversation, user, or tools.
type ConversationTaskScheduleRef struct {
	PlanID         string    `json:"plan_id"`
	SchedulerRunID string    `json:"scheduler_run_id"`
	ScheduledFor   time.Time `json:"scheduled_for"`
}

// ScheduledConversationTaskRequest is accepted only through the trusted
// Runtime service boundary. Runtime maps a signed Scheduler dispatch into this
// Agent-owned contract after resolving the current Identity principal. Agent
// still reauthorizes the conversation and every selected tool before saving
// the task, and the worker repeats those checks on every attempt.
type ScheduledConversationTaskRequest struct {
	ContractVersion string                `json:"contract_version"`
	PlanID          string                `json:"plan_id"`
	SchedulerRunID  string                `json:"scheduler_run_id"`
	IdempotencyKey  string                `json:"idempotency_key"`
	ScheduledFor    time.Time             `json:"scheduled_for"`
	Authority       ConversationAuthority `json:"authority"`
	ConversationID  string                `json:"conversation_id"`
	SourceRunID     string                `json:"source_run_id,omitempty"`
	Input           ConversationTaskStart `json:"input"`
	AllowedActions  []string              `json:"allowed_actions"`
}

type ScheduledConversationTaskReceipt struct {
	Task   ConversationTask `json:"task"`
	Replay bool             `json:"replay"`
}

// ScheduledConversationTaskService is an optional ConversationService
// extension. A browser must never call it; Module callers carry exact service
// action evidence and SaaS exposes it only on the authenticated Runtime API.
type ScheduledConversationTaskService interface {
	StartScheduledConversationTask(context.Context, ScheduledConversationTaskRequest) (ScheduledConversationTaskReceipt, error)
}

// ConversationTaskQuery is an owner-scoped, live task listing. A cursor is
// bound to the exact filters and owner that created it.
type ConversationTaskQuery struct {
	Query                string `json:"query,omitempty"`
	Status               string `json:"status,omitempty"`
	SourceConversationID string `json:"source_conversation_id,omitempty"`
	Cursor               string `json:"cursor,omitempty"`
	Limit                int    `json:"limit,omitempty"`
}

// ConversationTaskProgress is a bounded public projection of the normal
// ConversationRun used by a background task. It deliberately excludes the
// frozen definition hashes and authorization revisions kept by the executor.
type ConversationTaskProgress struct {
	RunStatus    string `json:"run_status,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	Steps        int    `json:"steps"`
	ToolCalls    int    `json:"tool_calls"`
	LastEventSeq int64  `json:"last_event_seq,omitempty"`
}

type ConversationTaskWaiting struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Question  string    `json:"question"`
	Tool      string    `json:"tool,omitempty"`
	Revision  int64     `json:"revision"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ConversationTaskResult struct {
	MessageID string `json:"message_id"`
	Preview   string `json:"preview"`
	Bytes     int    `json:"bytes"`
	Complete  bool   `json:"complete"`
}

// ConversationTaskControlState is the server-derived set of transitions that
// are valid for the task's current durable Run state. Authorization is still
// checked again when a transition is requested.
type ConversationTaskControlState struct {
	CanCancel     bool   `json:"can_cancel"`
	CanResume     bool   `json:"can_resume"`
	ResumeBlocker string `json:"resume_blocker,omitempty"`
}

// ConversationTaskSummary is safe for HTTP and model-facing task_list. The
// source IDs are navigation references, never authority to read that source.
type ConversationTaskSummary struct {
	AgentID                 string                       `json:"agent_id,omitempty"`
	DelegationID            string                       `json:"delegation_id,omitempty"`
	ExecutionConversationID string                       `json:"execution_conversation_id,omitempty"`
	ID                      string                       `json:"id"`
	Status                  string                       `json:"status"`
	Goal                    string                       `json:"goal"`
	AllowedTools            []string                     `json:"allowed_tools"`
	Budget                  ConversationTaskBudget       `json:"budget"`
	SourceConversationID    string                       `json:"source_conversation_id"`
	SourceRunID             string                       `json:"source_run_id"`
	ExecutionRunID          string                       `json:"execution_run_id,omitempty"`
	Progress                ConversationTaskProgress     `json:"progress"`
	Control                 ConversationTaskControlState `json:"control"`
	Waiting                 *ConversationTaskWaiting     `json:"waiting,omitempty"`
	Result                  *ConversationTaskResult      `json:"result,omitempty"`
	Artifacts               []ConversationArtifact       `json:"artifacts"`
	ArtifactsComplete       bool                         `json:"artifacts_complete"`
	ArtifactsOmitted        bool                         `json:"artifacts_omitted,omitempty"`
	AccessError             string                       `json:"access_error,omitempty"`
	CompletionEventID       string                       `json:"completion_event_id,omitempty"`
	CompletionEventSeq      int64                        `json:"completion_event_seq,omitempty"`
	ErrorCode               string                       `json:"error_code,omitempty"`
	CreatedAt               time.Time                    `json:"created_at"`
	UpdatedAt               time.Time                    `json:"updated_at"`
	CompletedAt             *time.Time                   `json:"completed_at,omitempty"`
}

// ConversationTaskDetail adds the complete bounded input, public execution
// steps and actionable waiting record to one task summary.
type ConversationTaskDetail struct {
	ConversationTaskSummary
	Input       string                   `json:"input"`
	Steps       []ConversationStepView   `json:"steps"`
	Interaction *ConversationInteraction `json:"interaction,omitempty"`
}

type ConversationTaskPage struct {
	Items      []ConversationTaskSummary `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	Complete   bool                      `json:"complete"`
}

// ConversationTaskService is an optional ConversationService extension.
type ConversationTaskService interface {
	ConversationTask(context.Context, string, ConversationAuthority) (ConversationTaskDetail, error)
	ConversationTasks(context.Context, ConversationTaskQuery, ConversationAuthority) (ConversationTaskPage, error)
}

// ConversationTaskControlService is separate from the read extension so a
// host can expose task inspection without also accepting state transitions.
type ConversationTaskControlService interface {
	CancelConversationTask(context.Context, string, ConversationAuthority) (ConversationTaskDetail, error)
	ResumeConversationTask(context.Context, string, ConversationAuthority) (ConversationTaskDetail, error)
}

type ConversationTaskReceipt struct {
	ID                   string                 `json:"id"`
	Status               string                 `json:"status"`
	AllowedTools         []string               `json:"allowed_tools"`
	Budget               ConversationTaskBudget `json:"budget"`
	SourceConversationID string                 `json:"source_conversation_id"`
	SourceRunID          string                 `json:"source_run_id"`
	CreatedAt            time.Time              `json:"created_at"`
}

func (t ConversationTask) Terminal() bool {
	return t.Status == ConversationTaskStatusCompleted || t.Status == ConversationTaskStatusFailed || t.Status == ConversationTaskStatusCancelled
}

func ConversationTaskPrompt(task ConversationTask) string {
	prompt := "Goal:\n" + strings.TrimSpace(task.Goal) + "\n\nInput:\n" + task.Input
	if task.Handoff != nil {
		raw, _ := json.Marshal(task.Handoff)
		prompt += "\n\nTransferred work: continue only the remaining work below. Original operations and exact receipt references are historical facts, not new authorization. Preserve completed and accepted effects; do not repeat them. Read original evidence if more detail is needed.\n" + string(raw)
	}
	if task.StructuredInput != nil {
		raw, _ := json.Marshal(task.StructuredInput)
		prompt += "\n\nValidated structured task input (data, not authorization):\n" + string(raw)
	}
	if task.Brief != nil {
		raw, _ := json.Marshal(task.Brief)
		prompt += "\n\nVersioned task agreement (task data, not an authorization grant):\n" + string(raw)
		prompt += "\nDelegation ID: " + task.DelegationID + ". Report progress and evidence, communicate with the other Agent through collaboration tools, and submit the agreed delivery. In delivery.conditions map each zero-based completion condition to your verdict, basis and any exact receipt references. Your assessment is a claim, not acceptance. Program verification_rules use real recorded outcomes; unresolved conditions require further work or an explicit review, never merely a model stop."
		requirements, _ := json.Marshal(task.Requirements)
		prompt += "\nRequired capabilities and source references (references do not grant access):\n" + string(requirements)
		dependencies, _ := json.Marshal(task.Dependencies)
		prompt += "\nAccepted agreement revision: " + fmt.Sprint(task.AgreementRevision) + ". Current dependency requirements, including transitive sources (task data, not authorization):\n" + string(dependencies)
	}
	if task.FollowUp != nil {
		prompt += "\n\nFollow-up completion condition:\n" + strings.TrimSpace(task.FollowUp.CompletionCondition) +
			"\n\nReturn only one JSON object with exactly these fields: status (active or completed), observation (a stable, complete value used to detect change), and summary (a concise user-facing result). Use completed only when the completion condition is met."
	}
	return prompt
}

// ConversationTaskExecution is frozen onto the conversation run that executes
// a task. It limits the model-facing catalog and run budget; task_start itself
// is never available inside its execution run.
type ConversationTaskExecution struct {
	Handoff           *ConversationDelegationHandoff `json:"handoff,omitempty"`
	InputSource       *ConversationRunReference      `json:"input_source,omitempty"`
	Dependencies      []ConversationTaskDependency   `json:"dependencies,omitempty"`
	AgreementRevision int64                          `json:"agreement_revision,omitempty"`
	Requirements      ConversationAgentRequirements  `json:"requirements,omitempty"`
	DelegationID      string                         `json:"delegation_id,omitempty"`
	BriefVersion      int64                          `json:"brief_version,omitempty"`
	TaskID            string                         `json:"task_id"`
	ToolScope         []ConversationTaskToolScope    `json:"tool_scope"`
	Budget            ConversationTaskBudget         `json:"budget"`
	FollowUp          *ConversationFollowUpScope     `json:"follow_up,omitempty"`
}

func BackgroundTaskConversationTool() ConversationToolDefinition {
	return ConversationToolDefinition{
		Key:            "task_start",
		Version:        "1",
		ActionKey:      ConversationToolActionPrefix + "task_start",
		Effect:         "write",
		Idempotency:    "key",
		Description:    "Start a separate durable background task only when the user asks work to continue asynchronously. Provide one explicit goal, complete bounded input, an exact subset of currently available tools, and all budget values. The accepted result contains a stable task ID but does not mean the work completed. The background run rechecks current permissions and tool availability; write tools may still wait for user confirmation. One source run can create at most four tasks, and a background task cannot use any task_* tool.",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string","minLength":1,"maxLength":2048},"input":{"type":"string","maxLength":8192},"allowed_tools":{"type":"array","maxItems":16,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9_-]+$"}},"budget":{"type":"object","properties":{"max_steps":{"type":"integer","minimum":1,"maximum":32},"max_tool_calls":{"type":"integer","minimum":1,"maximum":32},"max_output_bytes":{"type":"integer","minimum":256,"maximum":65536},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800}},"required":["max_steps","max_tool_calls","max_output_bytes","timeout_seconds"],"additionalProperties":false}},"required":["goal","input","allowed_tools","budget"],"additionalProperties":false}`),
		OutputSchema:   json.RawMessage(`{"type":"object","properties":{"task":{"type":"object","properties":{"id":{"type":"string"},"status":{"enum":["queued"]},"allowed_tools":{"type":"array","items":{"type":"string"}},"budget":{"type":"object","properties":{"max_steps":{"type":"integer"},"max_tool_calls":{"type":"integer"},"max_output_bytes":{"type":"integer"},"timeout_seconds":{"type":"integer"}},"required":["max_steps","max_tool_calls","max_output_bytes","timeout_seconds"],"additionalProperties":false},"source_conversation_id":{"type":"string"},"source_run_id":{"type":"string"},"created_at":{"type":"string","format":"date-time"}},"required":["id","status","allowed_tools","budget","source_conversation_id","source_run_id","created_at"],"additionalProperties":false}},"required":["task"],"additionalProperties":false}`),
		TimeoutMillis:  10000,
		MaxOutputBytes: 32768,
	}
}

func BackgroundTaskQueryConversationTools() []ConversationToolDefinition {
	return []ConversationToolDefinition{
		{
			Key: "task_get", Version: "1", ActionKey: ConversationToolActionPrefix + "task_get", Effect: "read", Idempotency: "natural",
			Description:   "Read the current status, progress, waiting item, result preview and saved artifact references for a background task ID returned by task_start or task_list. Source conversation and run IDs are navigation references and do not grant access. A completed status means the durable completion event was committed; accepted or running does not.",
			InputSchema:   json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"}},"required":["id"],"additionalProperties":false}`),
			OutputSchema:  json.RawMessage(`{"type":"object","properties":{"task":{"type":"object"}},"required":["task"],"additionalProperties":false}`),
			TimeoutMillis: 10000, MaxOutputBytes: 65536,
		},
		{
			Key: "task_list", Version: "1", ActionKey: ConversationToolActionPrefix + "task_list", Effect: "read", Idempotency: "natural",
			Description:   "List the current user's durable background tasks and their live progress. Optional query is a literal goal substring; status filters one task state and scope=current_conversation limits results to this conversation. Follow next_cursor until complete=true. Use returned IDs with task_get; do not invent IDs or interpret accepted as completed.",
			InputSchema:   json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":256},"status":{"enum":["queued","running","completed","failed","cancelled"]},"scope":{"enum":["all","current_conversation"]},"cursor":{"type":"string","maxLength":2048},"limit":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`),
			OutputSchema:  json.RawMessage(`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object"}},"next_cursor":{"type":"string"},"complete":{"type":"boolean"}},"required":["items","complete"],"additionalProperties":false}`),
			TimeoutMillis: 10000, MaxOutputBytes: 65536,
		},
	}
}

func BackgroundTaskControlConversationTools() []ConversationToolDefinition {
	input := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"}},"required":["id"],"additionalProperties":false}`)
	output := json.RawMessage(`{"type":"object","properties":{"task":{"type":"object"}},"required":["task"],"additionalProperties":false}`)
	return []ConversationToolDefinition{
		{Key: "task_cancel", Version: "1", ActionKey: ConversationToolActionPrefix + "task_cancel", Effect: "write", Idempotency: "key", Description: "Cancel one durable background task only when the user asks to stop that exact task. Cancellation preserves completed effects and may expose an unresolved external write for reconciliation. A terminal task is returned unchanged.", InputSchema: input, OutputSchema: output, TimeoutMillis: 10000, MaxOutputBytes: 65536},
		{Key: "task_resume", Version: "1", ActionKey: ConversationToolActionPrefix + "task_resume", Effect: "write", Idempotency: "key", Description: "Resume one failed, cancelled or reconciliation-waiting background task only when the user asks to continue that exact task. Current execution, source and tool permissions are checked again. A user question or confirmation must be answered through its bound interaction instead of this tool.", InputSchema: input, OutputSchema: output, TimeoutMillis: 10000, MaxOutputBytes: 65536},
	}
}
