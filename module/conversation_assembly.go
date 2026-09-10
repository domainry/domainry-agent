package module

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
)

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
		}
	}
	service, err := application.NewConversationService(a.repository, a.model, a.runtimeID, options)
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
