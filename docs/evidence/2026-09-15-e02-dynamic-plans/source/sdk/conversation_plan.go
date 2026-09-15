package agentsdk

import (
	"context"
	"encoding/json"
	"time"
)

const (
	ConversationPlanStepPending     = "pending"
	ConversationPlanStepInProgress  = "in_progress"
	ConversationPlanStepCompleted   = "completed"
	ConversationPlanStepBlocked     = "blocked"
	ConversationPlanStepSkipped     = "skipped"
	ConversationPlanStepNeedsReview = "needs_review"
)

// ConversationArtifactReference points at immutable Knowledge-owned artifact
// metadata. The artifact body and revision history remain owned by Knowledge.
type ConversationArtifactReference struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	SHA256  string `json:"sha256"`
}

// ConversationPlanExecutor is filled by Agent from the accepted task and run.
// It is never accepted from model arguments as an authority grant.
type ConversationPlanExecutor struct {
	AgentID string `json:"agent_id"`
	RunID   string `json:"run_id"`
}

// ConversationPlanStep is one durable unit of planned work. DependsOn contains
// step IDs from the same plan. Evidence and Artifacts are immutable references,
// not copies of business or Knowledge content.
type ConversationPlanStep struct {
	ID                string                          `json:"id"`
	Title             string                          `json:"title"`
	Status            string                          `json:"status"`
	DependsOn         []string                        `json:"depends_on"`
	Input             string                          `json:"input"`
	ExpectedOutput    string                          `json:"expected_output"`
	RequirementFields []string                        `json:"requirement_fields"`
	Executor          ConversationPlanExecutor        `json:"executor"`
	Evidence          []ConversationResultReference   `json:"evidence"`
	Artifacts         []ConversationArtifactReference `json:"artifacts"`
	Outcome           string                          `json:"outcome,omitempty"`
	Blocker           string                          `json:"blocker,omitempty"`
}

// ConversationPlan is immutable by version. A task points at the newest plan,
// while every prior version remains available through ConversationTaskPlans.
// Its instructions organize work and never authorize a business operation.
type ConversationPlan struct {
	TaskID            string                    `json:"task_id"`
	Version           int64                     `json:"version"`
	AgreementRevision int64                     `json:"agreement_revision"`
	Reason            string                    `json:"reason"`
	Steps             []ConversationPlanStep    `json:"steps"`
	Source            *ConversationRunReference `json:"source,omitempty"`
	CreatedAt         time.Time                 `json:"created_at"`
}

// ConversationPlanStepUpdate deliberately omits Executor. Agent derives the
// executor from the currently leased background run.
type ConversationPlanStepUpdate struct {
	ID                string                          `json:"id"`
	Title             string                          `json:"title"`
	Status            string                          `json:"status"`
	DependsOn         []string                        `json:"depends_on"`
	Input             string                          `json:"input"`
	ExpectedOutput    string                          `json:"expected_output"`
	RequirementFields []string                        `json:"requirement_fields"`
	Evidence          []ConversationResultReference   `json:"evidence"`
	Artifacts         []ConversationArtifactReference `json:"artifacts"`
	Outcome           string                          `json:"outcome,omitempty"`
	Blocker           string                          `json:"blocker,omitempty"`
}

type ConversationPlanUpdate struct {
	ClientID          string                       `json:"client_id"`
	ExpectedVersion   int64                        `json:"expected_version"`
	AgreementRevision int64                        `json:"agreement_revision"`
	Reason            string                       `json:"reason"`
	Steps             []ConversationPlanStepUpdate `json:"steps"`
}

type ConversationPlanHistory struct {
	Items      []ConversationPlan `json:"items"`
	NextBefore int64              `json:"next_before,omitempty"`
	Complete   bool               `json:"complete"`
}

type ConversationTaskPlanService interface {
	ConversationTaskPlans(context.Context, string, int64, ConversationAuthority) (ConversationPlanHistory, error)
}

func ConversationPlanUpdateTool() ConversationToolDefinition {
	step := `{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9_.:-]+$"},"title":{"type":"string","minLength":1,"maxLength":512},"status":{"enum":["pending","in_progress","completed","blocked","skipped","needs_review"]},"depends_on":{"type":"array","maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9_.:-]+$"}},"input":{"type":"string","maxLength":2048},"expected_output":{"type":"string","minLength":1,"maxLength":2048},"requirement_fields":{"type":"array","maxItems":9,"uniqueItems":true,"items":{"enum":["goal","deliverable","audience","constraints","completion_conditions","assumptions","due_at","input","dependencies"]}},"evidence":{"type":"array","maxItems":32,"items":{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":96},"run_id":{"type":"string","minLength":1,"maxLength":96},"step":{"type":"integer","minimum":0},"call_id":{"type":"string","minLength":1,"maxLength":96},"sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["conversation_id","run_id","step","call_id","sha256"],"additionalProperties":false}},"artifacts":{"type":"array","maxItems":32,"items":{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":96},"version":{"type":"integer","minimum":1},"sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["id","version","sha256"],"additionalProperties":false}},"outcome":{"type":"string","maxLength":2048},"blocker":{"type":"string","maxLength":2048}},"required":["id","title","status","depends_on","input","expected_output","requirement_fields","evidence","artifacts"],"additionalProperties":false}`
	return ConversationToolDefinition{
		Key: "plan_update", Version: "1", ActionKey: ConversationToolActionPrefix + "plan_update", Effect: "write", Idempotency: "key",
		Description:   "Create or replace the complete durable execution plan for the current background task. Simple tasks may finish without a plan. For multi-step work, call this before effects and again after a new user/Agent message, tool result or failure changes later work. Preserve every completed step and its exact evidence/artifact references. expected_version is 0 for the first plan and then the current version. The agreement revision must match the executing task. A plan records guidance and evidence only: it never grants a tool, permission, confirmation or execution authority.",
		InputSchema:   json.RawMessage(`{"type":"object","properties":{"client_id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"},"expected_version":{"type":"integer","minimum":0},"agreement_revision":{"type":"integer","minimum":1},"reason":{"type":"string","minLength":1,"maxLength":2048},"steps":{"type":"array","minItems":1,"maxItems":32,"items":` + step + `}},"required":["client_id","expected_version","agreement_revision","reason","steps"],"additionalProperties":false}`),
		OutputSchema:  json.RawMessage(`{"type":"object","properties":{"plan":{"type":"object"}},"required":["plan"],"additionalProperties":false}`),
		TimeoutMillis: 10000, MaxOutputBytes: 262144,
	}
}
