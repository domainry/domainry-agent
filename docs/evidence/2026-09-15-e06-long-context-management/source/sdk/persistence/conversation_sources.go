package persistence

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// A consistent owner-scoped snapshot for source access checks, without a
// worker lease. Never expose the input or ledger directly to a browser.
type ConversationSourceRepository interface {
	ConversationSourceSnapshot(context.Context, agentsdk.ConversationRunReference, agentsdk.ConversationAuthority) (ConversationSourceSnapshot, error)
}

// Server-only, owner-scoped provenance lookup for a deliberately shared root.
// It returns no input, output, lease or execution grant. Implementations must
// derive the authority from the immutable run rather than the requested role.
type ConversationSourceAuthorityRepository interface {
	ConversationSourceAuthority(context.Context, agentsdk.ConversationRunReference, agentsdk.ConversationAuthority) (agentsdk.ConversationAuthority, error)
}

type ConversationSourceSnapshot struct {
	// Immutable execution provenance for ledger routing. Current data and
	// operation authorization always retain the actual reader's authority.
	Authority    agentsdk.ConversationAuthority `json:"-"`
	StepSources  []ConversationStepSources
	StepContexts []ConversationStepContext
	Run          agentsdk.ConversationRun
	Input        *agentsdk.ConversationModelRequest
	Calls        []ConversationToolExecution
	Peers        []agentsdk.ConversationAgentMessage
}

type ConversationStepSources struct {
	Step    int
	Sources []agentsdk.ConversationRunReference
}

// ConversationStepContext is server-only frozen context needed to recheck a
// registered source before a derived historical reply or execution is read.
// Public source projections never expose Messages or manifest hashes.
type ConversationStepContext struct {
	Step     int
	Context  *agentsdk.ConversationContextManifest
	Messages []agentsdk.ConversationStepMessage
}
