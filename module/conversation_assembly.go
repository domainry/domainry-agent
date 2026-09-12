package module

import (
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
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

// This is startup-only assembly. The host binds before publishing the service;
// no running conversation service or frozen execution input is hot-swapped.
func (b *binding) openConversations(a *conversationAssembly, host modulehost.ConversationApplicationHost) error {
	options := a.options
	if host != nil {
		if options.PersonalAuthorizer == nil {
			options.PersonalAuthorizer = host.ConversationAuthorizer()
		}
		if options.ExecutionAuthorizer == nil {
			options.ExecutionAuthorizer, _ = options.PersonalAuthorizer.(agentsdk.ConversationExecutionAuthorizer)
		}
		if options.FollowUpPublisher == nil {
			if followUps, ok := host.(modulehost.ConversationFollowUpHost); ok {
				options.FollowUpPublisher = followUps.ConversationFollowUpPublisher()
			}
		}
		if options.ExecutionAuthorizer == nil {
			options.ExecutionAuthorizer, _ = host.ConversationAuthorizer().(agentsdk.ConversationExecutionAuthorizer)
		}
		if options.ExecutionAuthorizer == nil {
			return fmt.Errorf("Agent conversation application host requires current execution authorization")
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
			if options.Business == nil {
				options.Business = host.ConversationBusinessSource()
			}
			if composer, ok := host.(modulehost.ConversationToolComposer); ok {
				var err error
				options, err = conversationassembly.ComposeHostTools(options, composer)
				if err != nil {
					return err
				}
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
	return nil
}
