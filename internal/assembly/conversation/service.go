// Package conversation composes the generic Agent with optional local capabilities.
package conversation

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	knowledge "github.com/domainry/domainry-knowledge-sdk/contract"
)

func NewService(repo persistence.ConversationRepository, model sdk.ConversationModel, runtime string, options application.ConversationOptions) (*application.ConversationService, error) {
	if options.KnowledgeRuntime == nil {
		if provider, ok := repo.(interface{ KnowledgeRuntime() knowledge.Runtime }); ok {
			options.KnowledgeRuntime = provider.KnowledgeRuntime()
		}
	}
	return application.NewConversationService(repo, model, runtime, options)
}
