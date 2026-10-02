package module

import (
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	actioncontract "github.com/domainry/domainry-foundation/action"
)

func (b *binding) BindConversationHost(host modulehost.ConversationApplicationHost) error {
	b.assemblyMu.Lock()
	defer b.assemblyMu.Unlock()
	if b.closed {
		return fmt.Errorf("Agent Module binding is closed")
	}
	if b.pendingConversations == nil {
		return fmt.Errorf("Agent conversations are not awaiting host binding")
	}
	if host == nil || host.ConversationAuthorizer() == nil {
		return fmt.Errorf("deferred Agent conversations require a current application authorizer")
	}
	if err := b.openConversations(b.pendingConversations, host); err != nil {
		return err
	}
	b.pendingConversations = nil
	b.adapters = append(b.adapters, b.conversationAdapter)
	return nil
}

var _ modulehost.ConversationApplicationHostBinder = (*binding)(nil)

type conversationAssembly struct {
	repository          persistence.ConversationRepository
	model               agentsdk.ConversationModel
	runtimeID, timezone string
	options             ConversationOptions
}

func bindHostExecutionRuntimes(options *ConversationOptions, model agentsdk.ConversationModel, host modulehost.ConversationApplicationHost) {
	if options == nil || host == nil {
		return
	}
	if _, capable := model.(agentsdk.ConversationAgentModel); !capable {
		return
	}
	if code, ok := host.(modulehost.ConversationCodeHost); ok && options.CodeRuntime == nil {
		options.CodeRuntime = code.ConversationCodeRuntime()
	}
	if coding, ok := host.(modulehost.ConversationCodingHost); ok && options.CodingRuntime == nil {
		options.CodingRuntime = coding.ConversationCodingRuntime()
	}
}

// This is startup-only assembly. The host binds before publishing the service;
// no running conversation service or frozen execution input is hot-swapped.
func (b *binding) openConversations(a *conversationAssembly, host modulehost.ConversationApplicationHost) error {
	options := a.options
	var boundToolActions []actioncontract.ActionDefinition
	if host != nil {
		if options.PersonalAuthorizer == nil {
			options.PersonalAuthorizer = host.ConversationAuthorizer()
		}
		if options.LibraryAuthorizer == nil {
			options.LibraryAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.KnowledgeLibraryAuthorizer)
		}
		if options.SourceAuthorizer == nil {
			options.SourceAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.KnowledgeDocumentSourceAuthorizer)
		}
		if options.AttachmentAuthorizer == nil {
			options.AttachmentAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.ConversationAttachmentAuthorizer)
		}
		if options.ExecutionAuthorizer == nil {
			options.ExecutionAuthorizer, _ = options.PersonalAuthorizer.(agentsdk.ConversationExecutionAuthorizer)
		}
		if options.CollaborationAuthorizer == nil {
			options.CollaborationAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.ConversationCollaborationAuthorizer)
		}
		if options.FollowUpPublisher == nil {
			if followUps, ok := host.(modulehost.ConversationFollowUpHost); ok {
				options.FollowUpPublisher = followUps.ConversationFollowUpPublisher()
			}
		}
		if lifecycle, ok := host.(modulehost.ConversationLifecycleHost); ok {
			options.LifecycleExtensions = append(options.LifecycleExtensions, lifecycle.ConversationLifecycleExtensions()...)
		}
		if options.ExecutionAuthorizer == nil {
			options.ExecutionAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.ConversationExecutionAuthorizer)
		}
		if options.ExecutionAuthorizer == nil {
			return fmt.Errorf("Agent conversation application host requires current execution authorization")
		}
		// Tool definitions are part of the product's authorization contract, not
		// a side effect of having a model provider configured. Publish their
		// Actions during host binding even for a management-only conversation
		// surface; actual tool assembly and execution still require a tool-capable
		// model below.
		if composer, ok := host.(modulehost.ConversationToolComposer); ok {
			var err error
			var definitions []agentsdk.ConversationToolDefinition
			options, definitions, err = conversationassembly.ComposeHostTools(options, composer)
			if err != nil {
				return err
			}
			boundToolActions, err = agentsdk.ConversationToolAuthorizationActions(definitions)
			if err != nil {
				return fmt.Errorf("compile host conversation tool authorization: %w", err)
			}
			staticActions, staticErr := agentsdk.AgentAuthorizationActions()
			if staticErr != nil {
				return staticErr
			}
			if _, err = mergeAuthorizationActions(staticActions, boundToolActions); err != nil {
				return err
			}
		}
		if _, capable := a.model.(agentsdk.ConversationAgentModel); capable {
			if options.ToolAvailability == nil {
				options.ToolAvailability, _ = host.(agentsdk.ConversationToolAvailability)
			}
			if options.ToolHost == nil {
				var err error
				options.ToolHost, err = application.NewPersonalConversationHost(a.repository, options.PersonalAuthorizer, a.timezone)
				if err != nil {
					return err
				}
			}
			// Runtime advertises optional execution worlds independently of this
			// Agent's model. A management-only conversation surface (for example
			// Knowledge libraries) must not inherit an unusable code Runtime: code
			// and coding execution both require a tool-capable model and ToolHost.
			bindHostExecutionRuntimes(&options, a.model, host)
			if options.Business == nil {
				options.Business = host.ConversationBusinessSource()
			}
		}
	}
	if a.model != nil && options.ExecutionAuthorizer == nil {
		return fmt.Errorf("Agent conversation model requires current execution authorization")
	}
	service, err := conversationassembly.NewService(a.repository, a.model, a.runtimeID, options)
	if err != nil {
		return err
	}
	adapter, err := agenthttp.NewConversationAdapter(service, a.runtimeID)
	if err != nil {
		service.Close()
		return err
	}
	b.conversations, b.conversationAdapter = service, adapter
	b.conversationToolActions = cloneActionDefinitions(boundToolActions)
	return nil
}
