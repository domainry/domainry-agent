package application

import (
	"context"
	"encoding/json"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	knowledge "github.com/domainry/domainry-knowledge/contract"
)

func (s *ConversationService) authorizedDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (context.Context, error) {
	if in.Version < 1 || !conversationKey(in.ArtifactID) {
		return ctx, conversationFailure("bad_request", "artifact_result_invalid")
	}
	record, err := s.authorizedDeliveryResult(ctx, id, sdk.ConversationDeliveryResultRead{DeliveryRevision: in.DeliveryRevision, ConversationResultRead: sdk.ConversationResultRead{Reference: in.Reference}}, a)
	if err != nil {
		return ctx, err
	}
	if !deliveryArtifactMatches(record, in) {
		return ctx, conversationFailure("forbidden", "delivery_artifact_not_released")
	}
	return deliverySourceContext(ctx, id), nil
}

// Source validation happens before this projection. Matching only artifact
// tools prevents a business response with an "artifact" field from releasing
// unrelated Knowledge resources.
func deliveryArtifactMatches(record persistence.ConversationToolExecution, in sdk.ConversationDeliveryArtifactRead) bool {
	if _, ok := artifactTool(record.Call.Name); !ok || record.Result == nil {
		return false
	}
	var value struct {
		Artifact sdk.ConversationArtifact       `json:"artifact"`
		Items    []sdk.ConversationArtifact     `json:"items"`
		Export   sdk.ConversationArtifactExport `json:"export"`
	}
	if json.Unmarshal(record.Result.Content, &value) != nil {
		return false
	}
	if record.Call.Name == "artifact_export" {
		return value.Export.ArtifactID == in.ArtifactID && value.Export.Version == in.Version && (in.ExportID == "" || value.Export.ID == in.ExportID)
	}
	if in.ExportID != "" {
		return false
	}
	if record.Call.Name == "artifact_list" || record.Call.Name == "artifact_versions" {
		for _, item := range value.Items {
			if item.ID == in.ArtifactID && item.Version == in.Version {
				return true
			}
		}
		return false
	}
	return value.Artifact.ID == in.ArtifactID && value.Artifact.Version == in.Version
}

func (s *ConversationService) ReadConversationDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactVersion, error) {
	if in.ExportID != "" {
		return sdk.ConversationArtifactVersion{}, conversationFailure("bad_request", "artifact_result_invalid")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	ctx, err := s.authorizedDeliveryArtifact(ctx, id, in, a)
	if err != nil {
		return sdk.ConversationArtifactVersion{}, err
	}
	return s.Artifact(ctx, in.ArtifactID, in.Version, a)
}

func (s *ConversationService) DownloadConversationDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactDownload, error) {
	if !conversationKey(in.ExportID) {
		return sdk.ConversationArtifactDownload{}, conversationFailure("bad_request", "artifact_export_invalid")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	ctx, err := s.authorizedDeliveryArtifact(ctx, id, in, a)
	if err != nil {
		return sdk.ConversationArtifactDownload{}, err
	}
	reader, ok := s.knowledgeService().(knowledge.ArtifactExportReader)
	if !ok {
		return sdk.ConversationArtifactDownload{}, conversationFailure("unavailable", "artifact_export_read_unavailable")
	}
	return reader.ReadArtifactExport(ctx, in.ExportID, a)
}

var _ sdk.ConversationDeliveryArtifactReader = (*ConversationService)(nil)
