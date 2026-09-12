package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	"time"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func documentLeaseAuthority(l persistence.KnowledgeDocumentLease) agentsdk.ConversationAuthority {
	return knowledgemodule.CompatDocumentLeaseAuthority(l)
}

func (s *ConversationStore) queueDocumentWork(ctx context.Context, tx *sql.Tx, r persistence.KnowledgeDocumentRecord) error {
	return s.knowledgeStore().CompatQueueDocumentWork(ctx, tx, r)
}

func (s *ConversationStore) ClaimKnowledgeDocumentWork(ctx context.Context, runtime, owner string, now time.Time, ttl time.Duration) (out persistence.KnowledgeDocumentLease, found bool, err error) {
	return s.knowledgeStore().ClaimKnowledgeDocumentWork(ctx, runtime, owner, now, ttl)
}

func (s *ConversationStore) documentWork(ctx context.Context, db conversationDB, l persistence.KnowledgeDocumentLease) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().CompatDocumentWork(ctx, db, l)
}

func (s *ConversationStore) KnowledgeDocumentWorkRecord(ctx context.Context, l persistence.KnowledgeDocumentLease) (persistence.KnowledgeDocumentRecord, error) {
	return s.knowledgeStore().KnowledgeDocumentWorkRecord(ctx, l)
}

func knowledgeDocumentPutRequestID(r persistence.KnowledgeDocumentRecord) string {
	return knowledgemodule.CompatKnowledgeDocumentPutRequestID(r)
}

func (s *ConversationStore) StartKnowledgeDocumentPut(ctx context.Context, l persistence.KnowledgeDocumentLease) (out persistence.KnowledgeDocumentRecord, started bool, err error) {
	return s.knowledgeStore().StartKnowledgeDocumentPut(ctx, l)
}

func (s *ConversationStore) StartKnowledgeDocumentDelete(ctx context.Context, l persistence.KnowledgeDocumentLease) error {
	return s.knowledgeStore().StartKnowledgeDocumentDelete(ctx, l)
}

func (s *ConversationStore) ApplyKnowledgeDocumentProgress(ctx context.Context, l persistence.KnowledgeDocumentLease, in persistence.KnowledgeDocumentProgress) error {
	return s.knowledgeStore().ApplyKnowledgeDocumentProgress(ctx, l, in)
}
