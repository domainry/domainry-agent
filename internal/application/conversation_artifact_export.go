package application

import (
	"context"
	"fmt"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func (s *ConversationService) ExportArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactExportRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	repo, err := s.artifactAccess(ctx, a, "artifact_export", map[string]any{"id": id, "version": in.Version, "format": in.Format})
	if err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || in.Version < 1 {
		return out, conversationFailure("bad_request", "artifact_export_invalid")
	}
	version, err := s.Artifact(ctx, id, in.Version, a)
	if err != nil {
		return out, err
	}
	data, err := artifact.Export(version.Content, in.Format)
	if err != nil {
		return out, err
	}
	out = agentsdk.ConversationArtifactExport{ArtifactID: id, Version: in.Version, Format: in.Format, Filename: fmt.Sprintf("%s-v%d%s", id, in.Version, data.Extension), ContentType: data.ContentType, SHA256: artifact.Hash(data.Data), Bytes: len(data.Data), FormulaGuarded: data.FormulaGuarded}
	return repo.SaveArtifactExport(ctx, persistence.ConversationArtifactExportWrite{ClientID: in.ClientID, RequestSHA256: conversationDigest([]any{"export", id, in}), TTLSeconds: int64(s.options.ArtifactExportTTL / time.Second), Export: out}, a)
}

func (s *ConversationService) DownloadArtifact(ctx context.Context, exportID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactDownload, error) {
	var out agentsdk.ConversationArtifactDownload
	if err := s.authorize(a); err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationArtifactRepository)
	if !ok {
		return out, conversationFailure("unavailable", "artifacts_unavailable")
	}
	metadata, err := repo.ArtifactExport(ctx, exportID, a)
	if err != nil {
		return out, err
	}
	if _, err = s.artifactAccess(ctx, a, "artifact_export", map[string]any{"id": metadata.ArtifactID, "version": metadata.Version, "format": metadata.Format}); err != nil {
		return out, err
	}
	version, err := s.Artifact(ctx, metadata.ArtifactID, metadata.Version, a)
	if err != nil {
		return out, err
	}
	data, err := artifact.Export(version.Content, metadata.Format)
	if err != nil {
		return out, err
	}
	if artifact.Hash(data.Data) != metadata.SHA256 || len(data.Data) != metadata.Bytes || data.ContentType != metadata.ContentType || data.FormulaGuarded != metadata.FormulaGuarded || metadata.Filename != fmt.Sprintf("%s-v%d%s", metadata.ArtifactID, metadata.Version, data.Extension) {
		return out, conversationFailure("unavailable", "artifact_export_mismatch")
	}
	// No download receipt is counted when policy, provenance or content fails.
	metadata, err = repo.RecordArtifactDownload(ctx, exportID, a)
	if err != nil {
		return out, err
	}
	return agentsdk.ConversationArtifactDownload{Export: metadata, Data: data.Data}, nil
}
