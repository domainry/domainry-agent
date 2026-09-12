package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func validKnowledgeDocumentOrigin(o persistence.KnowledgeDocumentOrigin) bool {
	return knowledgemodule.CompatValidKnowledgeDocumentOrigin(o)
}

func (s *ConversationStore) FindKnowledgeDocumentTransfer(ctx context.Context, library, client string, origin persistence.KnowledgeDocumentOrigin, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, found bool, err error) {
	return s.knowledgeStore().FindKnowledgeDocumentTransfer(ctx, library, client, origin, a)
}

func (s *ConversationStore) checkKnowledgeDocumentOrigin(ctx context.Context, tx *sql.Tx, origin *persistence.KnowledgeDocumentOrigin, target agentsdk.KnowledgeDocument, a agentsdk.ConversationAuthority) (source persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().CompatCheckKnowledgeDocumentOrigin(ctx, tx, origin, target, a)
}

func (s *ConversationStore) retireKnowledgeDocument(ctx context.Context, tx *sql.Tx, record *persistence.KnowledgeDocumentRecord, expected int64) error {
	return s.knowledgeStore().CompatRetireKnowledgeDocument(ctx, tx, record, expected)
}
