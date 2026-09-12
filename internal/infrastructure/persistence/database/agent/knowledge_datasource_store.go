package agent

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func (s *ConversationStore) KnowledgeDatasourceBinding(ctx context.Context, scope agentsdk.KnowledgeDocumentStorageScope) (out persistence.KnowledgeDatasourceBinding, found bool, err error) {
	return s.knowledgeStore().KnowledgeDatasourceBinding(ctx, scope)
}

func (s *ConversationStore) BindKnowledgeDatasource(ctx context.Context, id string, in persistence.KnowledgeDatasourceAssignment, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	return s.knowledgeStore().BindKnowledgeDatasource(ctx, id, in, a)
}

func documentScopeForLibrary(id string, a agentsdk.ConversationAuthority) agentsdk.KnowledgeDocumentStorageScope {
	return knowledgemodule.CompatDocumentScopeForLibrary(id, a)
}
