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
func artifactExportScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return knowledgemodule.CompatArtifactExportScope(a, id)
}

func (s *ConversationStore) artifactExport(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	return s.knowledgeStore().CompatArtifactExport(ctx, db, id, a)
}

func (s *ConversationStore) ArtifactExport(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	return s.knowledgeStore().ArtifactExport(ctx, id, a)
}

func (s *ConversationStore) SaveArtifactExport(ctx context.Context, in persistence.ConversationArtifactExportWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	return s.knowledgeStore().SaveArtifactExport(ctx, in, a)
}

func (s *ConversationStore) saveArtifactExport(ctx context.Context, tx *sql.Tx, in persistence.ConversationArtifactExportWrite, a agentsdk.ConversationAuthority) (any, error) {
	return s.knowledgeStore().CompatSaveArtifactExport(ctx, tx, in, a)
}

// The application rechecks export action and source access before calling
// this method. Expiry and the download audit update are checked atomically.
func (s *ConversationStore) RecordArtifactDownload(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	return s.knowledgeStore().RecordArtifactDownload(ctx, id, a)
}
