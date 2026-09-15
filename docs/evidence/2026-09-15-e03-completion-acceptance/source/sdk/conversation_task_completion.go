package agentsdk

import (
	"context"
	"encoding/json"
	"time"
)

const (
	ConversationTaskCompletionModeLegacyResponse = "legacy_response"
	ConversationTaskCompletionModeAssessed       = "assessed"
)

// ConversationTaskCompletionSubmission is the executing Agent's structured
// claim about the current task agreement. References never grant authority.
type ConversationTaskCompletionSubmission struct {
	AgreementRevision int64                             `json:"agreement_revision"`
	Summary           string                            `json:"summary"`
	Data              json.RawMessage                   `json:"data,omitempty"`
	Conditions        []ConversationConditionAssessment `json:"conditions"`
	Artifacts         []ConversationArtifactReference   `json:"artifacts"`
	Source            *ConversationRunReference         `json:"source,omitempty"`
	SubmittedAt       time.Time                         `json:"submitted_at"`
}

// ConversationTaskCompletionRecord is immutable by Revision. Verification is
// produced by Agent from the exact agreement and original referenced outcomes.
type ConversationTaskCompletionRecord struct {
	Revision     int64                                `json:"revision"`
	Kind         string                               `json:"kind"` // agent_assessment, agent_review, user_review, execution_end, legacy_response
	Submission   ConversationTaskCompletionSubmission `json:"submission"`
	Verification ConversationDeliveryVerification     `json:"verification"`
	Reason       string                               `json:"reason"`
	RecordedAt   time.Time                            `json:"recorded_at"`
}

type ConversationTaskCompletionSubmit struct {
	ClientID          string                            `json:"client_id"`
	ExpectedRevision  int64                             `json:"expected_revision"`
	AgreementRevision int64                             `json:"agreement_revision"`
	Summary           string                            `json:"summary"`
	Data              json.RawMessage                   `json:"data,omitempty"`
	Conditions        []ConversationConditionAssessment `json:"conditions"`
	Artifacts         []ConversationArtifactReference   `json:"artifacts"`
}

type ConversationTaskCompletionReviewRequest struct {
	ClientID         string                     `json:"client_id"`
	ExpectedRevision int64                      `json:"expected_revision"`
	Reason           string                     `json:"reason"`
	Review           ConversationDeliveryReview `json:"review"`
	ToolRequest      *ConversationToolRequest   `json:"-"`
}

func ConversationTaskCompletionReviewTool() ConversationToolDefinition {
	assessment := `{"type":"object","properties":{"condition":{"type":"integer","minimum":0,"maximum":31},"verdict":{"enum":["met","unmet","unknown"]},"basis":{"type":"string","maxLength":4096},"receipts":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":96},"run_id":{"type":"string","minLength":1,"maxLength":96},"step":{"type":"integer","minimum":0},"call_id":{"type":"string","minLength":1,"maxLength":96},"sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["conversation_id","run_id","step","call_id","sha256"],"additionalProperties":false}}},"required":["condition","verdict","basis","receipts"],"additionalProperties":false}`
	return ConversationToolDefinition{
		Key: "task_review", Version: "1", ActionKey: ConversationToolActionPrefix + "task_review", Effect: "write", Idempotency: "key",
		Description:   "Review one assessed background task only when the user asks to evaluate that exact task. Read the task first and bind the current completion revision and verification delivery_digest. Decide every non-program condition from current evidence; program checks cannot be overridden. A met review can complete the task, while unmet or unknown conditions keep it awaiting review so the work can be continued.",
		InputSchema:   json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"},"client_id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"},"expected_revision":{"type":"integer","minimum":1},"reason":{"type":"string","minLength":1,"maxLength":4096},"review":{"type":"object","properties":{"delivery_digest":{"type":"string","pattern":"^[a-f0-9]{64}$"},"conditions":{"type":"array","minItems":1,"maxItems":32,"items":` + assessment + `}},"required":["delivery_digest","conditions"],"additionalProperties":false}},"required":["id","client_id","expected_revision","reason","review"],"additionalProperties":false}`),
		OutputSchema:  json.RawMessage(`{"type":"object","properties":{"task":{"type":"object"}},"required":["task"],"additionalProperties":false}`),
		TimeoutMillis: 10000, MaxOutputBytes: 131072,
	}
}

type ConversationTaskCompletionHistory struct {
	Items      []ConversationTaskCompletionRecord `json:"items"`
	NextBefore int64                              `json:"next_before,omitempty"`
	Complete   bool                               `json:"complete"`
}

type ConversationTaskCompletionService interface {
	ReviewConversationTaskCompletion(context.Context, string, ConversationTaskCompletionReviewRequest, ConversationAuthority) (ConversationTaskDetail, error)
	ConversationTaskCompletionHistory(context.Context, string, int64, ConversationAuthority) (ConversationTaskCompletionHistory, error)
}

func ConversationTaskCompletionSubmitTool() ConversationToolDefinition {
	ref := `{"type":"object","properties":{"conversation_id":{"type":"string","minLength":1,"maxLength":96},"run_id":{"type":"string","minLength":1,"maxLength":96},"step":{"type":"integer","minimum":0},"call_id":{"type":"string","minLength":1,"maxLength":96},"sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["conversation_id","run_id","step","call_id","sha256"],"additionalProperties":false}`
	assessment := `{"type":"object","properties":{"condition":{"type":"integer","minimum":0,"maximum":31},"verdict":{"enum":["met","unmet","unknown"]},"basis":{"type":"string","maxLength":4096},"receipts":{"type":"array","maxItems":16,"items":` + ref + `}},"required":["condition","verdict","basis","receipts"],"additionalProperties":false}`
	artifact := `{"type":"object","properties":{"id":{"type":"string","minLength":1,"maxLength":96},"version":{"type":"integer","minimum":1},"sha256":{"type":"string","pattern":"^[a-f0-9]{64}$"}},"required":["id","version","sha256"],"additionalProperties":false}`
	return ConversationToolDefinition{
		Key: "completion_submit", Version: "1", ActionKey: ConversationToolActionPrefix + "completion_submit", Effect: "write", Idempotency: "key",
		Description:   "Submit the structured completion assessment for the current background task before stopping. Cover every current completion condition by zero-based index, with met, unmet or unknown, a concrete basis, and exact tool receipts where relevant. Include every Knowledge artifact version relied on. Program rules are evaluated from original data and receipts and cannot be overridden by this claim. Use expected_revision 0 for the first submission and the current completion revision for a replacement. If anything is unmet or unknown, continue or leave it visibly awaiting review; a model stop alone never completes an assessed task.",
		InputSchema:   json.RawMessage(`{"type":"object","properties":{"client_id":{"type":"string","minLength":1,"maxLength":96,"pattern":"^[A-Za-z0-9_.:-]+$"},"expected_revision":{"type":"integer","minimum":0},"agreement_revision":{"type":"integer","minimum":1},"summary":{"type":"string","minLength":1,"maxLength":8192},"data":{},"conditions":{"type":"array","minItems":1,"maxItems":32,"items":` + assessment + `},"artifacts":{"type":"array","maxItems":32,"items":` + artifact + `}},"required":["client_id","expected_revision","agreement_revision","summary","conditions","artifacts"],"additionalProperties":false}`),
		OutputSchema:  json.RawMessage(`{"type":"object","properties":{"completion":{"type":"object"}},"required":["completion"],"additionalProperties":false}`),
		TimeoutMillis: 10000, MaxOutputBytes: 262144,
	}
}
