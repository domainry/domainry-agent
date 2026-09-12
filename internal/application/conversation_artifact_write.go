package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) artifactOrigin(ctx context.Context, conversationID, runID string, a agentsdk.ConversationAuthority) (*agentsdk.ConversationSources, error) {
	return s.knowledgeService().ArtifactOrigin(ctx, conversationID, runID, a)
}

func (s *ConversationService) artifactBody(ctx context.Context, record persistence.ConversationArtifactRecord, content agentsdk.ConversationArtifactContent, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	return s.knowledgeService().ArtifactBody(ctx, record, content, a)
}

func (s *ConversationService) CreateArtifact(ctx context.Context, in agentsdk.ConversationArtifactCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	return s.knowledgeService().CreateArtifact(ctx, in, a)
}

func (s *ConversationService) EditArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactEdit, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	return s.knowledgeService().EditArtifact(ctx, id, in, a)
}
