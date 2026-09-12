// Package conversation composes the generic Agent with optional local capabilities.
package conversation

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	knowledge "github.com/domainry/domainry-knowledge/module"
)

func NewService(repo persistence.ConversationRepository, model sdk.ConversationModel, runtime string, options application.ConversationOptions) (*application.ConversationService, error) {
	if options.KnowledgeFactory == nil {
		options.KnowledgeFactory = knowledge.NewFactory()
	}
	return application.NewConversationService(repo, model, runtime, options)
}
