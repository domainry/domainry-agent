package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) datasourceAccess(ctx context.Context, id, op string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().DatasourceAccess(ctx, id, op, a)
}

func (s *ConversationService) datasourceDefinitions(ctx context.Context, library string, a agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDatasourceDefinition, error) {
	return s.knowledgeService().DatasourceDefinitions(ctx, library, a)
}

func (s *ConversationService) KnowledgeLibrarySources(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrarySources, error) {
	return s.knowledgeService().KnowledgeLibrarySources(ctx, id, after, limit, a)
}

func (s *ConversationService) BindKnowledgeLibrarySource(ctx context.Context, id string, in agentsdk.KnowledgeLibrarySourceWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().BindKnowledgeLibrarySource(ctx, id, in, a)
}
