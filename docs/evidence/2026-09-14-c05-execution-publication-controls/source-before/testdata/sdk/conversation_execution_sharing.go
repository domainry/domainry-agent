package agentsdk

import "context"

// Execution sharing is explicit and confined to one admitted delegation run.
// It grants inspection only; private conversation and control ports remain separate.
type ConversationExecutionSharingService interface {
	PublishConversationDelegationExecution(context.Context, string, ConversationExecutionShare, ConversationAuthority) (ConversationExecutionPublication, error)
	ConversationDelegationExecutions(context.Context, string, ConversationAuthority) ([]ConversationExecutionPublication, error)
	ReadConversationDelegationExecution(context.Context, string, ConversationRunReference, ConversationAuthority) (ConversationRun, error)
	ReadConversationDelegationExecutionResult(context.Context, string, ConversationResultRead, ConversationAuthority) (ConversationResultSlice, error)
}

type ConversationExecutionShare struct {
	ToolRequest      *ConversationToolRequest `json:"-"`
	Reference        ConversationRunReference `json:"reference"`
	ExpectedRevision int64                    `json:"expected_revision"`
	ClientID         string                   `json:"client_id"`
	Reason           string                   `json:"reason"`
	Withdraw         bool                     `json:"withdraw,omitempty"`
}

type ConversationExecutionPublication struct {
	Reference ConversationRunReference `json:"reference"`
	Publisher ConversationAuthority    `json:"publisher"`
	Revision  int64                    `json:"revision,omitempty"`
	Withdrawn bool                     `json:"withdrawn,omitempty"`
}
