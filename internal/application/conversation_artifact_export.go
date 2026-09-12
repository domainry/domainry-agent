package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) ExportArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactExportRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	return s.knowledgeService().ExportArtifact(ctx, id, in, a)
}

func (s *ConversationService) DownloadArtifact(ctx context.Context, exportID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactDownload, error) {
	return s.knowledgeService().DownloadArtifact(ctx, exportID, a)
}
