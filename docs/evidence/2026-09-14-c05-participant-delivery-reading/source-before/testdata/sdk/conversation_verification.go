package agentsdk

import (
	"encoding/json"
	"time"
)

// A rule belongs to one zero-based completion condition in this exact brief.
// Data checks validate submitted data, not its real-world truth. Receipt rules
// check immutable tool outcomes and the declared argument/result constraints.
type ConversationCompletionRule struct {
	Condition       int             `json:"condition"`
	Kind            string          `json:"kind"` // data, receipt
	Schema          json.RawMessage `json:"schema,omitempty"`
	Tool            string          `json:"tool,omitempty"`
	ArgumentsSchema json.RawMessage `json:"arguments_schema,omitempty"`
	ResultSchema    json.RawMessage `json:"result_schema,omitempty"`
	MinReceipts     int             `json:"min_receipts,omitempty"`
	Completion      string          `json:"completion,omitempty"` // completed (default), accepted
}

// Verdicts supplied by a recipient remain claims. The issuer or user separately
// assesses judgment conditions; a supplied verdict cannot override a program check.
type ConversationConditionAssessment struct {
	Condition int                           `json:"condition"`
	Verdict   string                        `json:"verdict"` // met, unmet, unknown
	Basis     string                        `json:"basis"`
	Receipts  []ConversationResultReference `json:"receipts,omitempty"`
}

type ConversationDeliveryReview struct {
	DeliveryDigest string                            `json:"delivery_digest"`
	Conditions     []ConversationConditionAssessment `json:"conditions"`
}

type ConversationCompletionCheck struct {
	Condition   int                           `json:"condition"`
	Requirement string                        `json:"requirement"`
	Method      string                        `json:"method"` // program, recipient, agent, user, pending
	Verdict     string                        `json:"verdict"`
	Basis       string                        `json:"basis"`
	Receipts    []ConversationResultReference `json:"receipts,omitempty"`
}

// Verification is produced by the server, never accepted as a caller's proof.
type ConversationDeliveryVerification struct {
	DeliveryDigest    string                        `json:"delivery_digest"`
	BriefVersion      int64                         `json:"brief_version"`
	AgreementRevision int64                         `json:"agreement_revision"`
	Checks            []ConversationCompletionCheck `json:"checks"`
	Ready             bool                          `json:"ready"`
	Blockers          []string                      `json:"blockers"`
	ActorID           string                        `json:"actor_id"`
	AgentID           string                        `json:"agent_id,omitempty"`
	Source            *ConversationRunReference     `json:"source,omitempty"`
	CheckedAt         time.Time                     `json:"checked_at"`
}

// Each submitted delivery and subsequent verification/acceptance is immutable.
type ConversationDeliveryRecord struct {
	Publication  *ConversationDeliveryPublicationReceipt `json:"publication,omitempty"`
	Revision     int64                                   `json:"revision"`
	Kind         string                                  `json:"kind"` // deliver, review_delivery, accept_delivery, legacy, republish_delivery
	Delivery     ConversationDelegationDelivery          `json:"delivery"`
	Verification ConversationDeliveryVerification        `json:"verification"`
	Reason       string                                  `json:"reason"`
}

type ConversationDeliveryHistory struct {
	Items      []ConversationDeliveryRecord `json:"items"`
	Complete   bool                         `json:"complete"`
	NextBefore int64                        `json:"next_before,omitempty"`
}
