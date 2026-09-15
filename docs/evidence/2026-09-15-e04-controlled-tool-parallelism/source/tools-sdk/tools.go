// Package toolsdk defines tool contracts without depending on an Agent implementation.
package toolsdk

import (
	"context"
	"encoding/json"
	"time"
)

const (
	// ToolParallelismIndependentRead is an explicit tool-owner promise that
	// invocations have no ordering dependency or externally visible mutation.
	// The execution owner still chooses the batch size and authorizes every call.
	ToolParallelismIndependentRead = "independent_read"
)

type Authority struct {
	Known       bool   `json:"known"`
	RuntimeID   string `json:"runtime_id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	RoleKey     string `json:"role_key,omitempty"`
}

type Definition struct {
	Key          string          `json:"key"`
	Version      string          `json:"version"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	ActionKey    string          `json:"action_key"`
	Effect       string          `json:"effect"`      // read or write
	Idempotency  string          `json:"idempotency"` // natural, key or reconcile
	// Empty is serial. independent_read is valid only for read tools and lets
	// an execution owner run calls from the same model step concurrently.
	Parallelism    string `json:"parallelism,omitempty"`
	TimeoutMillis  int    `json:"timeout_ms"`
	MaxOutputBytes int    `json:"max_output_bytes"`
}

type Call struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // Complete JSON text; the executor validates the schema.
}

type Authorization struct {
	UserTimezone         string         `json:"user_timezone,omitempty"`
	Granted              bool           `json:"granted"`
	ConfirmationRequired bool           `json:"confirmation_required"`
	Revision             string         `json:"revision"`
	Evidence             map[string]any `json:"evidence,omitempty"`
}

type Confirmation struct {
	ID            string
	UserID        string
	ActionKey     string
	ToolVersion   string
	ArgumentsHash string
	ApprovedAt    time.Time
}

// ConfirmationVerifier is supplied by the execution owner. It verifies the
// persisted approval for this exact actor, run, call, definition and arguments.
// A structurally valid Confirmation value alone is never execution authority.
// Tools consume this port without importing the owner's implementation/store.
type ConfirmationVerifier interface {
	VerifyConversationToolConfirmation(context.Context, Request) (bool, error)
}

type Request struct {
	// ResultProducer is server-owned provenance for independent reading of a
	// published result. It never replaces Authority or authorizes execution.
	ResultProducer *Authority `json:"-"`
	// OutcomeInspectionToken permits only an owner-fenced receipt query, never an invocation.
	// It is local to the receipt inspector and must not cross unrelated RPC APIs.
	OutcomeInspectionToken string `json:"-"`
	Authority              Authority
	ConversationID         string
	RunID                  string
	// CorrelationID is assigned by the execution owner and propagated across
	// tool-owner boundaries. A model cannot supply or replace it.
	CorrelationID  string
	Step           int
	Call           Call
	Definition     Definition
	IdempotencyKey string
	ConfirmationID string
	Confirmation   *Confirmation
	// Server-only worker guard. Local transactional effects validate this
	// against persisted state; it is never accepted from model arguments.
	LeaseOwner string
	Fence      int64
}

type Result struct {
	// Source-owned disposition; never supplied by model arguments. An accepted
	// invocation may finish this tool call without finishing the business work.
	Completion string          `json:"completion,omitempty"`
	Status     string          `json:"status"` // completed, failed, pending or uncertain
	Content    json.RawMessage `json:"content,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	ResourceID string          `json:"resource_id,omitempty"`
}

type Host interface {
	ConversationTools(context.Context, Authority) ([]Definition, error)
	AuthorizeConversationTool(context.Context, Request) (Authorization, error)
	InvokeConversationTool(context.Context, Request) (Result, error)
	ReconcileConversationTool(context.Context, Request) (Result, error)
}
type ResultAuthorizer interface {
	AuthorizeConversationToolResult(context.Context, Request, Result) error
}

// ResultReadAuthorizer authorizes reading an already persisted result using
// current source/data permissions, independently of permission to execute the
// tool. Implementations must validate the exact definition, request, result and
// source scope without invoking or reconciling the operation. The caller must
// separately authorize access to the containing delivery or other resource.
// Missing support denies independent reading; ResultAuthorizer and successful
// execution authorization are not substitutes for this explicit policy.
type ResultReadAuthorizer interface {
	AuthorizeConversationToolResultRead(context.Context, Request, Result) error
}

// ResultReadUnsupportedCode indicates that no independent reading policy is
// registered. Callers may still use their normal execution-authorized path;
// this code never grants reading and is distinct from a source policy denial.
const ResultReadUnsupportedCode = "tool.result_read_unsupported"

// OutcomeInspector only observes the immutable receipt of this exact call and
// idempotency key. It MUST NOT invoke, retry, repair or submit an operation. A
// missing or still-changing receipt is uncertain, never proof of no effect.
// Registration is explicit because Reconcile may perform an idempotent write.
type OutcomeInspector interface {
	InspectConversationToolOutcome(context.Context, Request) (Result, error)
}
