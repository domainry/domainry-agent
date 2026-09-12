package agent

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
)

// Legacy API forwarding. Knowledge owns the data implementation.
type artifactCursor = knowledgemodule.CompatArtifactCursor

func (s *ConversationStore) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	return s.knowledgeStore().Artifacts(ctx, in, a)
}

func (s *ConversationStore) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	return s.knowledgeStore().ArtifactVersions(ctx, id, before, limit, a)
}
