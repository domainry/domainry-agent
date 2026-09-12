package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func knowledgeDocumentReservationID(library, client string, namespace string, a agentsdk.ConversationAuthority) string {
	return knowledgemodule.CompatKnowledgeDocumentReservationID(library, client, namespace, a)
}

func validAttachmentOrigin(origin persistence.KnowledgeAttachmentOrigin) bool {
	return knowledgemodule.CompatValidAttachmentOrigin(origin)
}

func (s *ConversationStore) FindKnowledgeAttachmentImport(ctx context.Context, library, client string, origin persistence.KnowledgeAttachmentOrigin, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, found bool, err error) {
	return s.knowledgeStore().FindKnowledgeAttachmentImport(ctx, library, client, origin, a)
}

// Check within the same transaction that reserves/commits the target document.
// A deletion that wins before commit cannot publish an independent library copy.
func (s *ConversationStore) checkDocumentAttachmentOrigin(ctx context.Context, tx *sql.Tx, origin *persistence.KnowledgeAttachmentOrigin, doc agentsdk.KnowledgeDocument, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().CompatCheckDocumentAttachmentOrigin(ctx, tx, origin, doc, a)
}
