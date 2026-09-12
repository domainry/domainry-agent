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
func artifactScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return knowledgemodule.CompatArtifactScope(a, id)
}

func artifactSHA(value string) bool { return knowledgemodule.CompatArtifactSHA(value) }

func (s *ConversationStore) artifactHead(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifact, error) {
	return s.knowledgeStore().CompatArtifactHead(ctx, db, id, a)
}

func (s *ConversationStore) artifactRecord(ctx context.Context, db conversationDB, id string, version int64, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	return s.knowledgeStore().CompatArtifactRecord(ctx, db, id, version, a)
}

func (s *ConversationStore) ArtifactRecord(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	return s.knowledgeStore().ArtifactRecord(ctx, id, version, a)
}

func (s *ConversationStore) artifactMutation(ctx context.Context, clientID, operation string, input any, a agentsdk.ConversationAuthority, apply func(*sql.Tx) (any, error), out any) error {
	return s.knowledgeStore().CompatArtifactMutation(ctx, clientID, operation, input, a, apply, out)
}

func validateArtifactRecord(in persistence.ConversationArtifactRecord) error {
	return knowledgemodule.CompatValidateArtifactRecord(in)
}

func (s *ConversationStore) saveArtifact(ctx context.Context, tx *sql.Tx, in persistence.ConversationArtifactWrite, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	return s.knowledgeStore().CompatSaveArtifact(ctx, tx, in, a)
}

func (s *ConversationStore) SaveArtifact(ctx context.Context, in persistence.ConversationArtifactWrite, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	return s.knowledgeStore().SaveArtifact(ctx, in, a)
}

func artifactRequestKey(digest string, fallback any) (any, error) {
	return knowledgemodule.CompatArtifactRequestKey(digest, fallback)
}
