package persistence

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type ConversationDelegationAdmission struct {
	// Supplied only by the trusted admission service after the receiving
	// subject's own authorization. This is not a public request parameter.
	ExecutionAuthority *agentsdk.ConversationAuthority `json:"-"`
	SourceAgent        agentsdk.ConversationAgentSnapshot
	Request            agentsdk.ConversationDelegationCreate
	FromAgentID        string
	SourceRunID        string
	Agent              agentsdk.ConversationAgentSnapshot
	Task               agentsdk.ConversationTask
}

type ConversationDelegationTransferAdmission struct {
	Request agentsdk.ConversationDelegationUpdate
	Agent   agentsdk.ConversationAgentSnapshot
	Task    agentsdk.ConversationTask
	Handoff agentsdk.ConversationDelegationHandoff
}

type ConversationDeliveryVerificationRepository interface {
	ConversationDeliveryHistory(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationDeliveryHistory, error)
	PreviewConversationDeliveryVerification(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationDeliveryVerification, error)
}

type ConversationDelegationParticipantRepository interface {
	SetConversationDelegationParticipants(context.Context, string, agentsdk.ConversationDelegationUpdate, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error)
	SupersedeConversationParticipantMessage(context.Context, string, agentsdk.ConversationAuthority) error
}

// Internal execution routing for an already admitted delegation. These values
// are not policy decisions and must never replace the actual reader's identity.
type ConversationDelegationAuthorities struct {
	Issuer   agentsdk.ConversationAuthority
	Executor agentsdk.ConversationAuthority
}

type ConversationDelegationExecutionRepository interface {
	ConversationDelegationAuthorities(context.Context, string, agentsdk.ConversationAuthority) (ConversationDelegationAuthorities, error)
	ConversationDelegationTask(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error)
}

// An immutable, narrowly scoped evidence release created with the submitted
// contract, message or delivery. The application still checks current sharing,
// collaboration and data-reader policy; this is only ledger routing metadata.
type ConversationSourceRelease struct {
	DelegationID string
	Purpose      string
	Reference    agentsdk.ConversationRunReference
	Producer     agentsdk.ConversationAuthority
	// Publisher can differ from the immutable producer's role. Legacy entries
	// require a publisher recovered from a recorded publication; an unknown
	// publisher never grants access and requires an explicit new submission.
	Publisher *agentsdk.ConversationAuthority `json:",omitempty"`
}

type ConversationSourceReleaseRepository interface {
	ConversationSourceReleases(context.Context, agentsdk.ConversationRunReference, agentsdk.ConversationAuthority) ([]ConversationSourceRelease, error)
}

type ConversationDelegationTransferRepository interface {
	ConversationDelegationTransferReceipt(context.Context, string, agentsdk.ConversationDelegationUpdate, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, bool, error)
	ConversationDelegationAssignments(context.Context, string, agentsdk.ConversationAuthority) ([]agentsdk.ConversationDelegationAssignment, error)
	PrepareConversationDelegationHandoff(context.Context, string, int64, string, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationHandoff, error)
	TransferConversationDelegation(context.Context, string, ConversationDelegationTransferAdmission, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error)
	ReuseConversationDelegationEffect(context.Context, ConversationClaim, int, agentsdk.ConversationToolCall, agentsdk.ConversationToolDefinition) (agentsdk.ConversationToolResult, bool, error)
}

// Collaboration is persisted by the Agent owner. Mutation IDs and revisions
// are checked in the same transaction as the resulting messages/task records.
type ConversationCollaborationRepository interface {
	ConversationAgreementHistory(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationAgreementHistory, error)
	ConversationPeerInbox(context.Context, string, agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgentMessage, error)
	LaunchConversationPeerMessage(context.Context, string) (agentsdk.ConversationRun, bool, error)
	ConversationAgents(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgent, error)
	ConversationAgent(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error)
	WriteConversationAgent(context.Context, string, agentsdk.ConversationAgentWrite, agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error)
	CreateConversationDelegation(context.Context, ConversationDelegationAdmission, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error)
	ConversationDelegations(context.Context, string, agentsdk.ConversationAuthority) ([]agentsdk.ConversationDelegation, error)
	ConversationDelegation(context.Context, string, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error)
	UpdateConversationDelegation(context.Context, string, agentsdk.ConversationDelegationUpdate, agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error)
	ConversationAgentMessages(context.Context, string, agentsdk.ConversationAuthority) ([]agentsdk.ConversationAgentMessage, error)
	SendConversationAgentMessage(context.Context, string, agentsdk.ConversationAgentMessageSend, string, agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMessage, error)
}

// Private canonical-record lookup for explicit republishing. It does not
// project history or authorize any source access on its own.
type ConversationDeliveryPublicationRepository interface {
	ConversationDeliveryPublicationRecord(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationDeliveryRecord, error)
}

// Legacy declarations can be recovered only from an exact original admission
// or an owned immutable run/task snapshot for this agreement and assignment.
type ConversationContractPublicationRecord struct {
	Agreement    agentsdk.ConversationAgreementRevision
	Requirements agentsdk.ConversationAgentRequirements
}

func (r ConversationContractPublicationRecord) SourceReferences() []agentsdk.ConversationRunReference {
	refs := append([]agentsdk.ConversationRunReference{}, r.Requirements.Sources...)
	for _, ref := range []*agentsdk.ConversationRunReference{r.Agreement.Source, r.Agreement.InputSource, r.Agreement.ChangeSource} {
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	for _, edge := range r.Agreement.Dependencies {
		for _, ref := range []*agentsdk.ConversationRunReference{edge.Source, edge.InputSource} {
			if ref != nil {
				refs = append(refs, *ref)
			}
		}
	}
	return refs
}

type ConversationContractPublicationRepository interface {
	ConversationContractPublicationRecord(context.Context, string, int64, agentsdk.ConversationAuthority) (ConversationContractPublicationRecord, error)
	ConversationContractPublicationHistory(context.Context, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationContractPublicationHistory, error)
}

// Discovery aggregates live load for owned or explicitly shared Agent
// identities. History is a bounded sample belonging to the actual caller. No
// tool contents or other owners' task identities cross this port.
type ConversationAgentObservation struct {
	ConfigurationDigest string
	AgentID             string
	Revision            int64
	Model               agentsdk.ConversationModelIdentity
	TaskType            string
	Status              string
	Usage               map[string]any
	DurationMillis      int64
	ToolCalls           int
}
type ConversationAgentLoad struct{ Running, Queued, Waiting int }
type ConversationAgentObservations struct {
	Load            map[string]ConversationAgentLoad
	History         []ConversationAgentObservation
	HistoryComplete bool
}
type ConversationAgentDiscoveryRepository interface {
	ConversationAgentObservations(context.Context, agentsdk.ConversationAuthority) (ConversationAgentObservations, error)
}

// Immutable disagreement revisions are read independently from bounded summaries.
type ConversationDisagreementRepository interface {
	ConversationDisagreementHistory(context.Context, string, string, int64, agentsdk.ConversationAuthority) (agentsdk.ConversationDisagreementHistory, error)
}
