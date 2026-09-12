package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) artifactAccess(ctx context.Context, a agentsdk.ConversationAuthority, key string, input any) (persistence.ConversationArtifactRepository, error) {
	return s.knowledgeService().ArtifactAccess(ctx, a, key, input)
}

func (s *ConversationService) artifactContent(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactContent, error) {
	return s.knowledgeService().ArtifactContent(ctx, record, a)
}

func (s *ConversationService) artifactView(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	return s.knowledgeService().ArtifactView(ctx, record, a)
}

func (s *ConversationService) Artifact(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	return s.knowledgeService().Artifact(ctx, id, version, a)
}

func (s *ConversationService) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	return s.knowledgeService().Artifacts(ctx, in, a)
}

func (s *ConversationService) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	return s.knowledgeService().ArtifactVersions(ctx, id, before, limit, a)
}

func (audit *conversationSourceAudit) artifactSources(ctx context.Context, record persistence.ConversationArtifactRecord) error {
	if record.Sources == nil || record.Sources.Version != 1 || len(record.Sources.Runs) > 256 || len(record.Sources.Omitted) != 0 {
		return conversationFailure("unavailable", "artifact_sources_invalid")
	}
	_, err := audit.sources(ctx, record.Sources)
	return err
}

// Read and validate the immutable body before using it for editing or export.
// An opaque host reference is never exposed in the public representation.

func artifactTool(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, definition := range agentsdk.ArtifactConversationTools() {
		if definition.Key == key {
			return definition, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}
