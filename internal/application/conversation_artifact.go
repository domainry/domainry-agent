package application

import (
	"context"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func artifactTool(key string) (agentsdk.ConversationToolDefinition, bool) {
	for _, definition := range agentsdk.ArtifactConversationTools() {
		if definition.Key == key {
			return definition, true
		}
	}
	return agentsdk.ConversationToolDefinition{}, false
}

func (s *ConversationService) artifactAccess(ctx context.Context, a agentsdk.ConversationAuthority, key string, input any) (persistence.ConversationArtifactRepository, error) {
	if err := s.authorize(a); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationArtifactRepository)
	definition, known := artifactTool(key)
	if !ok || !known || s.options.PersonalAuthorizer == nil {
		return nil, conversationFailure("unavailable", "artifacts_unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	decision, err := s.options.PersonalAuthorizer.AuthorizeConversationTool(ctx, agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: key, Arguments: string(raw)}})
	if err != nil {
		return nil, err
	}
	if !decision.Granted || decision.ConfirmationRequired {
		return nil, conversationFailure("forbidden", "tool_access_denied")
	}
	return repo, nil
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
func (s *ConversationService) artifactContent(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactContent, error) {
	var zero agentsdk.ConversationArtifactContent
	if (len(record.Body) == 0) == (record.BodyRef == "") {
		return zero, conversationFailure("unavailable", "artifact_content_invalid")
	}
	raw := record.Body
	if record.BodyRef != "" {
		if s.options.ArtifactStorage == nil {
			return zero, conversationFailure("unavailable", "artifact_storage_required")
		}
		var err error
		raw, err = s.options.ArtifactStorage.ReadArtifactContent(ctx, record.BodyRef, a)
		if err != nil {
			return zero, conversationFailure("unavailable", "artifact_content_unavailable")
		}
	}
	if len(raw) != record.Artifact.Bytes || len(raw) > artifact.MaxBytes || artifact.Hash(raw) != record.Artifact.SHA256 {
		return zero, conversationFailure("unavailable", "artifact_content_mismatch")
	}
	content, err := artifact.Decode(raw)
	if err != nil || content.Kind != record.Artifact.Kind {
		return zero, conversationFailure("unavailable", "artifact_content_invalid")
	}
	return content, nil
}

func (s *ConversationService) artifactView(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	if err := s.sourceAudit(a).artifactSources(ctx, record); err != nil {
		return out, err
	}
	content, err := s.artifactContent(ctx, record, a)
	if err != nil {
		return out, err
	}
	return agentsdk.ConversationArtifactVersion{Artifact: record.Artifact, Content: content}, nil
}

func (s *ConversationService) Artifact(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	repo, err := s.artifactAccess(ctx, a, "artifact_read", map[string]any{"id": id, "version": version})
	if err != nil {
		return agentsdk.ConversationArtifactVersion{}, err
	}
	record, err := repo.ArtifactRecord(ctx, id, version, a)
	if err != nil {
		return agentsdk.ConversationArtifactVersion{}, err
	}
	return s.artifactView(ctx, record, a)
}

func (s *ConversationService) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	repo, err := s.artifactAccess(ctx, a, "artifact_list", in)
	if err != nil {
		return agentsdk.ConversationArtifactPage{}, err
	}
	page, err := repo.Artifacts(ctx, in, a)
	if err != nil {
		return agentsdk.ConversationArtifactPage{}, err
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	audit := s.sourceAudit(a)
	visible := make([]agentsdk.ConversationArtifact, 0, len(page.Items))
	for _, item := range page.Items {
		record, err := repo.ArtifactRecord(ctx, item.ID, item.Version, a)
		if err != nil {
			return agentsdk.ConversationArtifactPage{}, err
		}
		// Titles and revision metadata can also reveal protected source data.
		if err = audit.artifactSources(ctx, record); err != nil {
			page.Omitted = true
			continue
		}
		visible = append(visible, item)
	}
	if err = ctx.Err(); err != nil {
		return agentsdk.ConversationArtifactPage{}, err
	}
	page.Items = visible
	return page, nil
}

func (s *ConversationService) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	repo, err := s.artifactAccess(ctx, a, "artifact_versions", map[string]any{"id": id, "before": before, "limit": limit})
	if err != nil {
		return agentsdk.ConversationArtifactVersions{}, err
	}
	page, err := repo.ArtifactVersions(ctx, id, before, limit, a)
	if err != nil {
		return agentsdk.ConversationArtifactVersions{}, err
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	audit := s.sourceAudit(a)
	visible := make([]agentsdk.ConversationArtifact, 0, len(page.Items))
	for _, item := range page.Items {
		record, err := repo.ArtifactRecord(ctx, id, item.Version, a)
		if err != nil {
			return agentsdk.ConversationArtifactVersions{}, err
		}
		if err = audit.artifactSources(ctx, record); err != nil {
			page.Omitted = true
			continue
		}
		visible = append(visible, item)
	}
	if err = ctx.Err(); err != nil {
		return agentsdk.ConversationArtifactVersions{}, err
	}
	page.Items = visible
	return page, nil
}

var _ agentsdk.ConversationArtifactService = (*ConversationService)(nil)
