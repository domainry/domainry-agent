package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

// A source is an exact run, not whichever conversation happens to be current
// when an HTTP request is retried. Its immutable ledger supplies the access
// dependencies; a client cannot provide a supposedly public Sources object.
func (s *ConversationService) artifactOrigin(ctx context.Context, conversationID, runID string, a agentsdk.ConversationAuthority) (*agentsdk.ConversationSources, error) {
	sources := &agentsdk.ConversationSources{Version: 1, Runs: []agentsdk.ConversationRunReference{}}
	if conversationID == "" && runID == "" {
		return sources, nil
	}
	if !conversationKey(conversationID) || !conversationKey(runID) {
		return nil, conversationFailure("bad_request", "artifact_source_run_required")
	}
	ref := agentsdk.ConversationRunReference{ConversationID: conversationID, RunID: runID}
	run, err := s.repo.Run(ctx, conversationID, runID, a)
	if err != nil {
		return nil, err
	}
	if run.Status != "completed" {
		return nil, conversationFailure("conflict", "artifact_source_not_finished")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	if _, err := s.sourceAudit(a).run(ctx, ref); err != nil {
		return nil, err
	}
	sources.Runs = []agentsdk.ConversationRunReference{ref}
	return sources, nil
}

func (s *ConversationService) artifactBody(ctx context.Context, record persistence.ConversationArtifactRecord, content agentsdk.ConversationArtifactContent, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	raw, hash, err := artifact.Encode(content)
	if err != nil {
		return persistence.ConversationArtifactRecord{}, err
	}
	record.Artifact.Kind, record.Artifact.SHA256, record.Artifact.Bytes = content.Kind, hash, len(raw)
	record.Body, record.BodyRef = nil, ""
	if len(raw) <= artifact.InlineBytes {
		record.Body = raw
		return record, nil
	}
	if s.options.ArtifactStorage == nil {
		return persistence.ConversationArtifactRecord{}, conversationFailure("unavailable", "artifact_storage_required")
	}
	record.BodyRef, err = s.options.ArtifactStorage.PutArtifactContent(ctx, hash, raw, a)
	if err != nil {
		return persistence.ConversationArtifactRecord{}, conversationFailure("unavailable", "artifact_storage_unavailable")
	}
	// A successful host write is not sufficient evidence that the reference
	// actually resolves to these bytes in the current owner's namespace.
	if _, err = s.artifactContent(ctx, record, a); err != nil {
		return persistence.ConversationArtifactRecord{}, err
	}
	return record, nil
}

func (s *ConversationService) CreateArtifact(ctx context.Context, in agentsdk.ConversationArtifactCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	repo, err := s.artifactAccess(ctx, a, "artifact_create", in)
	if err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || !artifact.ValidTitle(in.Title) {
		return out, conversationFailure("bad_request", "artifact_invalid")
	}
	sources, err := s.artifactOrigin(ctx, in.SourceConversationID, in.SourceRunID, a)
	if err != nil {
		return out, err
	}
	record, err := s.artifactBody(ctx, persistence.ConversationArtifactRecord{Artifact: agentsdk.ConversationArtifact{Title: in.Title, SourceConversationID: in.SourceConversationID, SourceRunID: in.SourceRunID}, Sources: sources}, in.Content, a)
	if err != nil {
		return out, err
	}
	record, err = repo.SaveArtifact(ctx, persistence.ConversationArtifactWrite{ClientID: in.ClientID, RequestSHA256: conversationDigest([]any{"create", in}), Record: record}, a)
	if err != nil {
		return out, err
	}
	return s.artifactView(ctx, record, a)
}

func (s *ConversationService) EditArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactEdit, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	repo, err := s.artifactAccess(ctx, a, "artifact_edit", map[string]any{"id": id, "expected_version": in.ExpectedVersion, "patch": in.Patch})
	if err != nil {
		return out, err
	}
	if _, err = s.artifactAccess(ctx, a, "artifact_read", map[string]any{"id": id, "version": in.ExpectedVersion}); err != nil {
		return out, err
	}
	if !conversationKey(in.ClientID) || in.ExpectedVersion < 1 {
		return out, conversationFailure("bad_request", "artifact_invalid")
	}
	// Read the expected immutable version, so replay can return its original
	// receipt even after later edits; storage performs CAS for a fresh command.
	record, err := repo.ArtifactRecord(ctx, id, in.ExpectedVersion, a)
	if err != nil {
		return out, err
	}
	previous, err := s.artifactView(ctx, record, a)
	if err != nil {
		return out, err
	}
	content, err := artifact.Edit(previous.Content, in.Patch)
	if err != nil {
		return out, err
	}
	if in.Patch.Title != nil {
		record.Artifact.Title = *in.Patch.Title
	}
	record, err = s.artifactBody(ctx, record, content, a)
	if err != nil {
		return out, err
	}
	record, err = repo.SaveArtifact(ctx, persistence.ConversationArtifactWrite{ClientID: in.ClientID, RequestSHA256: conversationDigest([]any{"edit", id, in}), ExpectedVersion: in.ExpectedVersion, Record: record}, a)
	if err != nil {
		return out, err
	}
	return s.artifactView(ctx, record, a)
}
