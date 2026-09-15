package modulehost

import agentsdk "github.com/domainry/domainry-agent-sdk"

// DeferredConversationHost is an optional persistence-host capability. When
// true, OpenModule must not start conversation workers or publish conversation
// services/adapters until BindConversationHost (or the legacy full
// BindApplicationHost) supplies the live application ports. Hosts must finish
// binding before serving HTTP or accessing Conversations.
type DeferredConversationHost interface {
	DeferConversationHostBinding() bool
}

// ConversationApplicationHost supplies current authorization and optional
// business reads after the host's application services have been assembled.
// It is separate from TaskHost: ongoing conversations have no business task,
// process or entrypoint identity. A nil business source exposes no business tools.
// Hosts may also implement agentsdk.ConversationToolAvailability to supply live
// connection/tool switches without changing this required application contract.
// The returned authorizer must also implement ConversationExecutionAuthorizer,
// unless the module was configured with an explicit execution authorizer. This
// includes text-only models; binding without current execution policy fails.
// For configured library retrieval, the returned authorizer may also implement
// KnowledgeLibraryAuthorizer. Explicit module library policy takes precedence.
type ConversationApplicationHost interface {
	ConversationAuthorizer() agentsdk.ConversationToolAuthorizer
	ConversationBusinessSource() agentsdk.ConversationBusinessSource
}

// ConversationToolComposer optionally attaches host-selected tool families at
// startup. It receives the already assembled host; implementations preserve
// current result authorization. Definitions are consumed before profile validation and
// no conversation worker may start before composition has succeeded.
type ConversationToolComposer interface {
	ConversationToolDefinitions() []agentsdk.ConversationToolDefinition
	AssembleConversationTools(agentsdk.ConversationToolHost) (agentsdk.ConversationToolHost, error)
}

// ConversationFollowUpHost optionally supplies Runtime's source-neutral sink
// for Agent-owned follow-up facts. Agent never imports Notification contracts.
type ConversationFollowUpHost interface {
	ConversationFollowUpPublisher() agentsdk.ConversationFollowUpPublisher
}

// ConversationApplicationHostBinder is the optional conversation-only startup
// boundary. The persistence host must opt into DeferredConversationHost.
// Binding once publishes conversation services/adapters and starts recovery;
// nil or incomplete hosts, repeated binding and binding after Close fail.
// It does not bind Interactive, Task, Proposal, Audit or Analysis ports and does
// not replace an already running conversation service. Finish binding before
// serving HTTP or reading the binding's services, descriptor or adapters.
type ConversationApplicationHostBinder interface {
	BindConversationHost(ConversationApplicationHost) error
}
