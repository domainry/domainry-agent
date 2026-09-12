package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	"github.com/domainry/domainry-orm/query"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func documentScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return knowledgemodule.CompatDocumentScope(a, id)
}

func validKnowledgeDocumentID(id string) bool {
	return knowledgemodule.CompatValidKnowledgeDocumentID(id)
}

func documentWriting(l agentsdk.KnowledgeLibrary) error {
	return knowledgemodule.CompatDocumentWriting(l)
}

func (s *ConversationStore) ActivateKnowledgeDocumentSource(ctx context.Context, scope agentsdk.KnowledgeDocumentStorageScope, source string) error {
	return s.knowledgeStore().ActivateKnowledgeDocumentSource(ctx, scope, source)
}

func (s *ConversationStore) activateDocumentSource(ctx context.Context, tx *sql.Tx, scope agentsdk.KnowledgeDocumentStorageScope, source string) error {
	return s.knowledgeStore().CompatActivateDocumentSource(ctx, tx, scope, source)
}

func (s *ConversationStore) documentLibrarySource(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (string, error) {
	return s.knowledgeStore().CompatDocumentLibrarySource(ctx, db, id, a)
}

func (s *ConversationStore) KnowledgeDocumentLibrarySource(ctx context.Context, id string, a agentsdk.ConversationAuthority) (string, error) {
	return s.knowledgeStore().KnowledgeDocumentLibrarySource(ctx, id, a)
}

func (s *ConversationStore) KnowledgeSourceManaged(ctx context.Context, source string) (bool, error) {
	return s.knowledgeStore().KnowledgeSourceManaged(ctx, source)
}

func (s *ConversationStore) document(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().CompatDocument(ctx, db, id, a)
}

func (s *ConversationStore) KnowledgeDocumentRecord(ctx context.Context, id string, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().KnowledgeDocumentRecord(ctx, id, a)
}

func (s *ConversationStore) KnowledgeDocumentByRemoteID(ctx context.Context, library, source, remote string, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().KnowledgeDocumentByRemoteID(ctx, library, source, remote, a)
}

func (s *ConversationStore) saveDocument(ctx context.Context, tx *sql.Tx, r *persistence.KnowledgeDocumentRecord, expected int64) error {
	return s.knowledgeStore().CompatSaveDocument(ctx, tx, r, expected)
}

func (s *ConversationStore) ReserveKnowledgeDocument(ctx context.Context, in persistence.KnowledgeDocumentReserve, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().ReserveKnowledgeDocument(ctx, in, a)
}

func (s *ConversationStore) CommitKnowledgeDocumentContent(ctx context.Context, id string, expected int64, ref string, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().CommitKnowledgeDocumentContent(ctx, id, expected, ref, a)
}

func (s *ConversationStore) KnowledgeDocuments(ctx context.Context, library, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeDocumentPage, err error) {
	return s.knowledgeStore().KnowledgeDocuments(ctx, library, after, limit, a)
}

func (s *ConversationStore) RequestKnowledgeDocumentDeletion(ctx context.Context, id string, expected int64, a agentsdk.ConversationAuthority) (out persistence.KnowledgeDocumentRecord, err error) {
	return s.knowledgeStore().RequestKnowledgeDocumentDeletion(ctx, id, expected, a)
}
