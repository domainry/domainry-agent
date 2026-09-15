package agentsdk

import (
	"context"
	"time"
)

const (
	ConversationContextKindProjectInstructions = "project_instructions"
	ConversationContextKindBusinessRecord      = "business_record"
	ConversationContextKindFileReference       = "file_reference"
	ConversationContextKindHostData            = "host_data"

	ConversationContextScopeWorkspace    = "workspace"
	ConversationContextScopeConversation = "conversation"
	ConversationContextScopeTask         = "task"

	ConversationContextRefreshRun  = "run"
	ConversationContextRefreshStep = "step"

	ConversationContextTrustInstruction = "instruction"
	ConversationContextTrustData        = "data"
	ConversationContextTrustReference   = "reference"
)

// ConversationContextSourceDefinition is a deployment-owned registration.
// A model or API caller cannot create a source, change its order, mark data as
// trusted instructions, or select a broader scope. StablePrefix is restricted
// to run-frozen instructions so dynamic records remain after the cacheable
// prefix instead of invalidating or contaminating it.
type ConversationContextSourceDefinition struct {
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	Scope        string `json:"scope"`
	Refresh      string `json:"refresh"`
	Trust        string `json:"trust"`
	Order        int    `json:"order"`
	MaxBytes     int    `json:"max_bytes"`
	StablePrefix bool   `json:"stable_prefix,omitempty"`
}

// ConversationContextSourceRequest is assembled by Agent from the leased run.
// CurrentInput is the exact current user/task input used for retrieval; it is
// data for the registered source and does not grant access to any other scope.
type ConversationContextSourceRequest struct {
	Authority      ConversationAuthority `json:"authority"`
	ConversationID string                `json:"conversation_id"`
	RunID          string                `json:"run_id"`
	TaskID         string                `json:"task_id,omitempty"`
	Purpose        string                `json:"purpose"` // reply or step
	Step           int                   `json:"step,omitempty"`
	CurrentInput   string                `json:"current_input"`
}

// ConversationContextSourceContent is a current source-owned value. Content
// is stored only in the frozen model input; public run views expose the bounded
// reference below. Sources retain protected run provenance for reauthorization.
type ConversationContextSourceContent struct {
	Version   string                     `json:"version"`
	Content   string                     `json:"content"`
	UpdatedAt time.Time                  `json:"updated_at"`
	Sources   []ConversationRunReference `json:"sources,omitempty"`
}

type ConversationContextSourceReference struct {
	Key            string                     `json:"key"`
	Kind           string                     `json:"kind"`
	Scope          string                     `json:"scope"`
	Refresh        string                     `json:"refresh"`
	Trust          string                     `json:"trust"`
	Order          int                        `json:"order"`
	StablePrefix   bool                       `json:"stable_prefix,omitempty"`
	DefinitionHash string                     `json:"definition_hash"`
	Version        string                     `json:"version"`
	ContentHash    string                     `json:"content_hash"`
	MessageHash    string                     `json:"message_hash"`
	MessageIndex   int                        `json:"message_index"`
	UpdatedAt      time.Time                  `json:"updated_at"`
	Sources        []ConversationRunReference `json:"sources,omitempty"`
}

type ConversationContextSourceChange struct {
	Key             string    `json:"key"`
	PreviousVersion string    `json:"previous_version"`
	CurrentVersion  string    `json:"current_version"`
	ChangedAt       time.Time `json:"changed_at"`
}

// ConversationContextManifest is frozen with every model input/step. It is a
// safe provenance and ordering projection and never contains source content.
type ConversationContextManifest struct {
	Version          int                                  `json:"version"`
	Sources          []ConversationContextSourceReference `json:"sources"`
	Changes          []ConversationContextSourceChange    `json:"changes,omitempty"`
	StablePrefixHash string                               `json:"stable_prefix_hash"`
	DynamicHash      string                               `json:"dynamic_hash"`
	RefreshedAt      time.Time                            `json:"refreshed_at"`
}

// ConversationContextWindow reports byte pressure against Agent's configured
// input budget. ProviderSerialized is true only when the model exposes the
// pure exact-protocol sizing port; otherwise InputBytes is a conservative SDK
// snapshot size. Permille avoids floating-point ambiguity in persisted views.
type ConversationContextWindow struct {
	LimitBytes         int  `json:"limit_bytes"`
	InputBytes         int  `json:"input_bytes"`
	PressurePermille   int  `json:"pressure_permille"`
	ProviderSerialized bool `json:"provider_serialized"`
}

// ConversationContextSource is a read-only host boundary. Read must authorize
// Request.Authority for its declared workspace/conversation/task scope before
// returning content. Authorize then rechecks access to the exact frozen
// reference immediately before each model request. References and versions
// never grant authority by themselves. Task-scoped sources are not called for
// ordinary conversation runs that have no task identity.
type ConversationContextSource interface {
	ConversationContextSourceDefinition() ConversationContextSourceDefinition
	ReadConversationContext(context.Context, ConversationContextSourceRequest) (ConversationContextSourceContent, error)
	AuthorizeConversationContext(context.Context, ConversationContextSourceRequest, ConversationContextSourceReference) error
}
