package agentsdk

import (
	"context"
	"encoding/json"
	"time"
)

const CapabilityConversationCollaborationV1 = "conversation.collaboration.v1"

// ConversationAgent is an independent worker configuration owned by one user.
// Explicit sharing grants other workspace users use of that configuration;
// ordinary conversations keep their caller's identity. A separate owner opt-in
// binds the identity used for delegated tasks; sharing alone never does so.
// A delegation grants responsibility for one task, never ownership of an Agent.
type ConversationAgent struct {
	DelegationExecution string    `json:"delegation_execution,omitempty"`
	DelegationRoleKey   string    `json:"delegation_role_key,omitempty"`
	OwnerUserID         string    `json:"owner_user_id,omitempty"`
	Shared              bool      `json:"shared,omitempty"`
	SharedWithUserIDs   []string  `json:"shared_with_user_ids,omitempty"`
	DefinitionKey       string    `json:"definition_key,omitempty"`
	DefinitionVersion   string    `json:"definition_version,omitempty"`
	DefinitionDigest    string    `json:"definition_digest,omitempty"`
	ID                  string    `json:"id"`
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	Instructions        string    `json:"instructions"`
	Tools               []string  `json:"tools"`
	SkillKeys           []string  `json:"skill_keys"`
	ModelKey            string    `json:"model_key"`
	Enabled             bool      `json:"enabled"`
	MaxConcurrent       int       `json:"max_concurrent"`
	Revision            int64     `json:"revision"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ConversationAgentWrite struct {
	// Omitted preserves the binding; caller disables it, owner binds the
	// authenticated writer's current role. A client cannot supply that role.
	DelegationExecution *string `json:"delegation_execution,omitempty"`
	// Omitted preserves current sharing. An explicit empty list revokes it.
	SharedWithUserIDs *[]string `json:"shared_with_user_ids,omitempty"`
	DefinitionKey     string    `json:"definition_key,omitempty"`
	DefinitionVersion string    `json:"-"`
	DefinitionDigest  string    `json:"-"`
	ClientID          string    `json:"client_id"`
	ExpectedRevision  int64     `json:"expected_revision"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	Instructions      string    `json:"instructions"`
	Tools             []string  `json:"tools"`
	SkillKeys         []string  `json:"skill_keys"`
	ModelKey          string    `json:"model_key"`
	Enabled           bool      `json:"enabled"`
	MaxConcurrent     int       `json:"max_concurrent"`
}

type ConversationAgentPage struct {
	Definitions  []ConversationAgentDefinition `json:"definitions"`
	Skills       []ConversationSkillSummary    `json:"skills"`
	Availability []ConversationAgentCandidate  `json:"availability"`
	Items        []ConversationAgent           `json:"items"`
	Tools        []ConversationToolDefinition  `json:"tools"`
	Models       []string                      `json:"models"`
	Complete     bool                          `json:"complete"`
}

// The snapshot pins configuration at admission. Current access and enabled
// state are checked again on execution; changing configuration never silently
// changes an already accepted run.
type ConversationAgentSnapshot struct {
	// Present only for a task admitted under an explicit owner execution binding.
	DelegationRoleKey string                        `json:"delegation_role_key,omitempty"`
	OwnerUserID       string                        `json:"owner_user_id,omitempty"`
	ExecutionSubject  *ConversationExecutionSubject `json:"execution_subject,omitempty"`
	ID                string                        `json:"id"`
	Revision          int64                         `json:"revision"`
	ModelKey          string                        `json:"model_key"`
	ModelIdentity     ConversationModelIdentity     `json:"model_identity"`
	Profile           AgentSchema                   `json:"profile"`
	Skills            []SkillSchema                 `json:"skills,omitempty"`
	Digest            string                        `json:"digest"`
}

type ConversationTaskBrief struct {
	VerificationRules    []ConversationCompletionRule `json:"verification_rules,omitempty"`
	Version              int64                        `json:"version"`
	Goal                 string                       `json:"goal"`
	Deliverable          string                       `json:"deliverable"`
	Audience             string                       `json:"audience"`
	Constraints          []string                     `json:"constraints"`
	CompletionConditions []string                     `json:"completion_conditions"`
	Assumptions          []string                     `json:"assumptions"`
	DueAt                *time.Time                   `json:"due_at,omitempty"`
}

type ConversationStructuredInput struct {
	Schema json.RawMessage `json:"schema,omitempty"`
	Data   json.RawMessage `json:"data"`
}

type ConversationDelegationCreate struct {
	StructuredInput *ConversationStructuredInput  `json:"structured_input,omitempty"`
	Dependencies    []ConversationDependencyInput `json:"dependencies,omitempty"`
	Requirements    ConversationAgentRequirements `json:"requirements,omitempty"`
	WorkBudget      *ConversationWorkBudget       `json:"work_budget,omitempty"`
	ToolRequest     *ConversationToolRequest      `json:"-"`
	ClientID        string                        `json:"client_id"`
	ConversationID  string                        `json:"conversation_id"`
	AgentID         string                        `json:"agent_id"`
	Purpose         string                        `json:"purpose"`
	Brief           ConversationTaskBrief         `json:"brief"`
	Input           string                        `json:"input"`
	Budget          ConversationTaskBudget        `json:"budget"`
	OutputSchema    json.RawMessage               `json:"output_schema,omitempty"`
}

type ConversationDelegation struct {
	SubjectExited            bool                                `json:"subject_exited,omitempty"`
	OwnerUserID              string                              `json:"owner_user_id,omitempty"`
	ParticipantsRevision     int64                               `json:"participants_revision,omitempty"`
	Participants             []ConversationDelegationParticipant `json:"participants,omitempty"`
	ExecutionSubject         *ConversationExecutionSubject       `json:"execution_subject,omitempty"`
	Disagreements            []ConversationDisagreementSummary   `json:"disagreements,omitempty"`
	DisagreementsOmitted     bool                                `json:"disagreements_omitted,omitempty"`
	Verification             *ConversationDeliveryVerification   `json:"verification,omitempty"`
	AssignmentNumber         int64                               `json:"assignment_number,omitempty"`
	Handoff                  *ConversationDelegationHandoff      `json:"handoff,omitempty"`
	StructuredInput          *ConversationStructuredInput        `json:"structured_input,omitempty"`
	InputSource              *ConversationRunReference           `json:"input_source,omitempty"`
	Dependencies             []ConversationTaskDependency        `json:"dependencies"`
	PendingChanges           []ConversationRequirementChange     `json:"pending_changes"`
	AgreementRevision        int64                               `json:"agreement_revision,omitempty"`
	AdoptedAgreementRevision int64                               `json:"adopted_agreement_revision"`
	AdoptedAt                *time.Time                          `json:"adopted_at,omitempty"`
	BriefSource              *ConversationRunReference           `json:"brief_source,omitempty"`
	Requirements             ConversationAgentRequirements       `json:"requirements,omitempty"`
	WorkBudget               *ConversationWorkBudget             `json:"work_budget,omitempty"`
	WorkUsage                *ConversationWorkUsage              `json:"work_usage,omitempty"`
	RootConversationID       string                              `json:"root_conversation_id"`
	SourceAgent              *ConversationAgentSnapshot          `json:"source_agent,omitempty"`
	ID                       string                              `json:"id"`
	FromAgentID              string                              `json:"from_agent_id"`
	ToAgentID                string                              `json:"to_agent_id"`
	SourceConversationID     string                              `json:"source_conversation_id"`
	SourceRunID              string                              `json:"source_run_id,omitempty"`
	ConversationID           string                              `json:"conversation_id"`
	TaskID                   string                              `json:"task_id"`
	Purpose                  string                              `json:"purpose"`
	Brief                    ConversationTaskBrief               `json:"brief"`
	Input                    string                              `json:"input"`
	Budget                   ConversationTaskBudget              `json:"budget"`
	OutputSchema             json.RawMessage                     `json:"output_schema,omitempty"`
	Status                   string                              `json:"status"`
	Revision                 int64                               `json:"revision"`
	Delivery                 *ConversationDelegationDelivery     `json:"delivery,omitempty"`
	Decision                 string                              `json:"decision,omitempty"`
	CreatedAt                time.Time                           `json:"created_at"`
	UpdatedAt                time.Time                           `json:"updated_at"`
}

type ConversationDelegationDelivery struct {
	Conditions        []ConversationConditionAssessment `json:"conditions,omitempty"`
	AgreementRevision int64                             `json:"agreement_revision,omitempty"`
	BriefVersion      int64                             `json:"brief_version"`
	Summary           string                            `json:"summary"`
	Data              json.RawMessage                   `json:"data,omitempty"`
	Evidence          []ConversationRunReference        `json:"evidence"`
	Unresolved        []string                          `json:"unresolved"`
}

type ConversationOutcomeInspectionRequest struct {
	RunID  string `json:"run_id"`
	Step   int    `json:"step"`
	CallID string `json:"call_id"`
}

type ConversationDelegationUpdate struct {
	ContractPublication *ConversationContractPublication          `json:"contract_publication,omitempty"`
	Publication         *ConversationDeliveryPublication          `json:"publication,omitempty"`
	Participants        *[]ConversationDelegationParticipantInput `json:"participants,omitempty"`
	Disagreement        *ConversationDisagreementChange           `json:"disagreement,omitempty"`
	Review              *ConversationDeliveryReview               `json:"review,omitempty"`
	Transfer            *ConversationDelegationTransfer           `json:"transfer,omitempty"`
	Inspection          *ConversationOutcomeInspectionRequest     `json:"inspection,omitempty"`
	StructuredInput     *ConversationStructuredInput              `json:"structured_input,omitempty"`
	Dependencies        *[]ConversationDependencyInput            `json:"dependencies,omitempty"`
	ToolRequest         *ConversationToolRequest                  `json:"-"`
	ClientID            string                                    `json:"client_id"`
	ExpectedRevision    int64                                     `json:"expected_revision"`
	Action              string                                    `json:"action"`
	Reason              string                                    `json:"reason"`
	Brief               *ConversationTaskBrief                    `json:"brief,omitempty"`
	Delivery            *ConversationDelegationDelivery           `json:"delivery,omitempty"`
}

type ConversationAgentMessage struct {
	// The authenticated sender, independent of whether the displayed speaker
	// is a user or an Agent. Set only by the server, and rechecked at intake.
	SenderUserID         string                          `json:"sender_user_id,omitempty"`
	SenderRoleKey        string                          `json:"sender_role_key,omitempty"`
	ParticipantUserID    string                          `json:"participant_user_id,omitempty"`
	ParticipantRoleKey   string                          `json:"participant_role_key,omitempty"`
	ParticipantRevision  int64                           `json:"participant_revision,omitempty"`
	Documents            []ConversationDocumentReference `json:"documents,omitempty"`
	DocumentsOmitted     bool                            `json:"documents_omitted,omitempty"`
	DisagreementID       string                          `json:"disagreement_id,omitempty"`
	DisagreementRevision int64                           `json:"disagreement_revision,omitempty"`
	AgreementRevision    int64                           `json:"agreement_revision"`
	DeliveryMode         string                          `json:"delivery_mode"`
	AfterRunID           string                          `json:"after_run_id,omitempty"`
	ReplyToID            string                          `json:"reply_to_id,omitempty"`
	AnsweredQuestionIDs  []string                        `json:"answered_question_ids,omitempty"`
	AnsweredByID         string                          `json:"answered_by_id,omitempty"`
	Superseded           bool                            `json:"superseded"`
	Change               *ConversationRequirementChange  `json:"change,omitempty"`
	Source               *ConversationRunReference       `json:"source,omitempty"`
	ID                   string                          `json:"id"`
	DelegationID         string                          `json:"delegation_id"`
	FromAgentID          string                          `json:"from_agent_id,omitempty"`
	FromUserID           string                          `json:"from_user_id,omitempty"`
	ToAgentID            string                          `json:"to_agent_id"`
	ConversationID       string                          `json:"conversation_id"`
	Kind                 string                          `json:"kind"`
	Content              string                          `json:"content"`
	BriefVersion         int64                           `json:"brief_version"`
	ConsumedAtStep       int                             `json:"consumed_at_step"`
	ConsumedByRunID      string                          `json:"consumed_by_run_id,omitempty"`
	ConsumedAt           *time.Time                      `json:"consumed_at,omitempty"`
	CreatedAt            time.Time                       `json:"created_at"`
}

type ConversationAgentMessageSend struct {
	Documents         []ConversationDocumentReference `json:"documents,omitempty"`
	AgreementRevision int64                           `json:"agreement_revision,omitempty"`
	Kind              string                          `json:"kind,omitempty"`
	DeliveryMode      string                          `json:"delivery_mode,omitempty"`
	ReplyToID         string                          `json:"reply_to_id,omitempty"`
	ToolRequest       *ConversationToolRequest        `json:"-"`
	ExecutionAgent    *ConversationAgentSnapshot      `json:"-"`
	ClientID          string                          `json:"client_id"`
	ToAgentID         string                          `json:"to_agent_id"`
	Content           string                          `json:"content"`
	BriefVersion      int64                           `json:"brief_version"`
}

type ConversationDelegationDetail struct {
	DeliveryOmitted     bool                                    `json:"delivery_omitted,omitempty"`
	ContractOmitted     bool                                    `json:"contract_omitted,omitempty"`
	ContractPublication *ConversationContractPublicationReceipt `json:"contract_publication,omitempty"`
	Publication         *ConversationDeliveryPublicationReceipt `json:"publication,omitempty"`
	// set_participants returns a narrow write receipt. Read the delegation
	// separately for its currently authorized agreement and execution content.
	ParticipantsOnly bool `json:"participants_only,omitempty"`
	// Participant management returns the original write receipt. Obtain live
	// content separately under the current grant and source permissions.
	ManagementOnly   bool                               `json:"management_only,omitempty"`
	Access           *ConversationCollaborationAccess   `json:"access,omitempty"`
	Assignments      []ConversationDelegationAssignment `json:"assignments,omitempty"`
	DependencyStates []ConversationDependencyState      `json:"dependency_states"`
	ConversationDelegation
	MessagesComplete bool                       `json:"messages_complete"`
	Task             *ConversationTaskDetail    `json:"task,omitempty"`
	Messages         []ConversationAgentMessage `json:"messages"`
}

type ConversationDelegationPage struct {
	Items    []ConversationDelegationDetail `json:"items"`
	Complete bool                           `json:"complete"`
}

type ConversationCollaborationService interface {
	ConversationCollaborationAccess(context.Context, ConversationAuthority) (ConversationCollaborationAuthorization, error)
	ConversationDisagreementHistory(context.Context, string, string, int64, ConversationAuthority) (ConversationDisagreementHistory, error)
	ConversationDeliveryHistory(context.Context, string, int64, ConversationAuthority) (ConversationDeliveryHistory, error)
	ConversationAgreementHistory(context.Context, string, int64, ConversationAuthority) (ConversationAgreementHistory, error)
	MatchConversationAgents(context.Context, ConversationAgentMatchRequest, ConversationAuthority) (ConversationAgentMatchPage, error)
	ConversationAgents(context.Context, ConversationAuthority) (ConversationAgentPage, error)
	WriteConversationAgent(context.Context, string, ConversationAgentWrite, ConversationAuthority) (ConversationAgent, error)
	CreateConversationDelegation(context.Context, ConversationDelegationCreate, ConversationAuthority) (ConversationDelegationDetail, error)
	ConversationDelegations(context.Context, string, ConversationAuthority) (ConversationDelegationPage, error)
	ConversationDelegation(context.Context, string, ConversationAuthority) (ConversationDelegationDetail, error)
	UpdateConversationDelegation(context.Context, string, ConversationDelegationUpdate, ConversationAuthority) (ConversationDelegationDetail, error)
	SendConversationAgentMessage(context.Context, string, ConversationAgentMessageSend, ConversationAuthority) (ConversationAgentMessage, error)
}

// PeerEvent marks a server-triggered continuation in conversation history.
// It is never accepted from ConversationSend or treated as new user consent.
type ConversationPeerEvent struct {
	MessageID    string `json:"message_id"`
	DelegationID string `json:"delegation_id"`
	FromAgentID  string `json:"from_agent_id,omitempty"`
	ToAgentID    string `json:"to_agent_id"`
	Kind         string `json:"kind"`
}
