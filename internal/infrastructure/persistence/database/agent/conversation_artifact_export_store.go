package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/artifact"
	"github.com/domainry/domainry-agent-sdk/persistence"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
)

const generatedArtifactKind = "generated"

type generatedArtifactMetadata struct {
	ArtifactID       string `json:"artifact_id"`
	Version          int64  `json:"version"`
	Format           string `json:"format"`
	FormulaGuarded   bool   `json:"formula_guarded,omitempty"`
	Downloads        int64  `json:"downloads"`
	LastDownloadedAt int64  `json:"last_downloaded_at,omitempty"`
}

func (s *ConversationStore) sharedArtifactsReady() error {
	if s == nil || s.artifactStore == nil || s.artifactContent == nil || s.artifactWriter == nil {
		return conversationError("unavailable", "artifact_storage_unavailable")
	}
	return nil
}

func (s *ConversationStore) artifactExport(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	if !personalMemoryKey(id) {
		return out, conversationError("bad_request", "artifact_export_invalid")
	}
	if err := s.sharedArtifactsReady(); err != nil {
		return out, err
	}
	value, found, err := s.artifactStore.ByID(sharedartifact.WithExecutor(ctx, db), a.WorkspaceID, id)
	if err != nil {
		return out, err
	}
	if !found || !generatedArtifactOwnedBy(value, a) {
		return out, conversationError("not_found", "artifact_export_not_found")
	}
	if err := s.requireGeneratedArtifactSubjectBinding(ctx, db, value, a); err != nil {
		return out, err
	}
	return generatedArtifactExport(value)
}

func (s *ConversationStore) ArtifactExport(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationArtifactExport{}, err
	}
	return s.artifactExport(ctx, s.store.Database(), id, a)
}

func (s *ConversationStore) SaveArtifactExport(ctx context.Context, in persistence.ConversationArtifactExportWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	in.Export.ID = ""
	in.Export.CreatedAt = time.Time{}
	in.Export.ExpiresAt = time.Time{}
	in.Export.Downloads = 0
	in.Export.LastDownloadedAt = nil
	key, err := artifactRequestKey(in.RequestSHA256, in)
	if err != nil {
		return out, err
	}
	err = s.artifactMutation(ctx, in.ClientID, "export", key, a, func(tx *sql.Tx) (any, error) {
		return s.saveArtifactExport(ctx, tx, in, a)
	}, &out)
	return out, err
}

func (s *ConversationStore) saveArtifactExport(ctx context.Context, tx *sql.Tx, in persistence.ConversationArtifactExportWrite, a agentsdk.ConversationAuthority) (_ any, err error) {
	if err := s.sharedArtifactsReady(); err != nil {
		return nil, err
	}
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	value := in.Export
	value.Downloads, value.LastDownloadedAt = 0, nil
	if value.Version < 1 || !artifactSHA(value.SHA256) || value.Bytes < 0 || value.Bytes > 2*artifact.MaxBytes || in.TTLSeconds < 1 || in.TTLSeconds > 24*3600 {
		return nil, conversationError("bad_request", "artifact_export_invalid")
	}
	if len(in.Content) != value.Bytes || artifact.Hash(in.Content) != value.SHA256 {
		return nil, conversationError("bad_request", "artifact_export_mismatch")
	}
	record, err := s.artifactRecord(ctx, tx, value.ArtifactID, value.Version, a)
	if err != nil {
		return nil, err
	}
	extension, mime := "", ""
	switch {
	case value.Format == "markdown" && record.Artifact.Kind == "markdown":
		extension, mime = ".md", "text/markdown; charset=utf-8"
	case value.Format == "csv" && (record.Artifact.Kind == "table" || record.Artifact.Kind == "chart"):
		extension, mime = ".csv", "text/csv; charset=utf-8"
	default:
		return nil, conversationError("bad_request", "artifact_export_format_invalid")
	}
	filename := fmt.Sprintf("%s-v%d%s", record.Artifact.ID, record.Artifact.Version, extension)
	if value.Filename != filename || value.ContentType != mime {
		return nil, conversationError("bad_request", "artifact_export_invalid")
	}
	if len(record.Body) > 0 {
		content, decodeErr := artifact.Decode(record.Body)
		if decodeErr != nil {
			return nil, decodeErr
		}
		data, exportErr := artifact.Export(content, value.Format)
		if exportErr != nil {
			return nil, exportErr
		}
		if value.SHA256 != artifact.Hash(data.Data) || value.Bytes != len(data.Data) || value.FormulaGuarded != data.FormulaGuarded || string(in.Content) != string(data.Data) {
			return nil, conversationError("bad_request", "artifact_export_mismatch")
		}
	}

	value.ID = "exp_" + conversationHash([]string{conversationOwner(a), in.ClientID})[:32]
	value.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	value.ExpiresAt = value.CreatedAt.Add(time.Duration(in.TTLSeconds) * time.Second)
	metadata, err := json.Marshal(generatedArtifactMetadata{
		ArtifactID: value.ArtifactID, Version: value.Version, Format: value.Format,
		FormulaGuarded: value.FormulaGuarded,
	})
	if err != nil {
		return nil, err
	}
	info, err := s.artifactWriter.PutImmutable(ctx, a.WorkspaceID, "agent-generated:"+value.ID, in.Content)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		current, found, readErr := s.artifactStore.ByID(context.WithoutCancel(ctx), a.WorkspaceID, value.ID)
		if readErr == nil && (!found || current.StorageReference != info.Reference) {
			_ = s.artifactContent.Delete(context.WithoutCancel(ctx), a.WorkspaceID, info.Reference)
		}
	}()
	if strings.TrimSpace(info.Reference) == "" || !strings.EqualFold(info.SHA256, value.SHA256) || info.Size != int64(value.Bytes) {
		return nil, conversationError("unavailable", "artifact_content_mismatch")
	}

	artifactContext := sharedartifact.WithExecutor(ctx, tx)
	registered, _, err := s.artifactStore.Register(artifactContext, sharedartifact.Artifact{
		ID: value.ID, WorkspaceID: a.WorkspaceID, Owner: sharedartifact.OwnerAgent, Kind: generatedArtifactKind,
		IdempotencyKey: generatedArtifactIdempotencyKey(a, in.ClientID), CreatedBy: a.UserID,
		Filename: value.Filename, MediaType: value.ContentType, ContentSHA256: value.SHA256,
		SizeBytes: int64(value.Bytes), StorageReference: info.Reference,
		Status: sharedartifact.StatusAvailable, ExpiresAt: value.ExpiresAt, ScanStatus: sharedartifact.ScanNotRequired,
		AuthorizationScopeSHA256: conversationOwner(a), Metadata: metadata,
		CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt,
	})
	if err != nil {
		current, found, readErr := s.artifactStore.ByID(artifactContext, a.WorkspaceID, value.ID)
		if readErr != nil || !found || !sameGeneratedArtifactRequest(current, value, a) {
			return nil, err
		}
		registered = current
	}
	if !sameGeneratedArtifactRequest(registered, value, a) {
		return nil, conversationError("conflict", "idempotency_conflict")
	}
	if err := s.bindGeneratedArtifactSubject(artifactContext, registered, a); err != nil {
		return nil, err
	}
	if record.Artifact.SourceConversationID != "" {
		if err := s.bindGeneratedArtifactConversation(artifactContext, registered, record.Artifact.SourceConversationID); err != nil {
			return nil, err
		}
	}
	return generatedArtifactExport(registered)
}

func (s *ConversationStore) ArtifactExportContent(ctx context.Context, id string, a agentsdk.ConversationAuthority) ([]byte, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, err
	}
	if err := s.sharedArtifactsReady(); err != nil {
		return nil, err
	}
	value, found, err := s.artifactStore.ByID(ctx, a.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	if !found || !generatedArtifactOwnedBy(value, a) {
		return nil, conversationError("not_found", "artifact_export_not_found")
	}
	if !time.Now().UTC().Before(value.ExpiresAt) || value.Status != sharedartifact.StatusAvailable {
		return nil, conversationError("conflict", "artifact_export_expired")
	}
	reader, err := s.artifactContent.Open(ctx, value.WorkspaceID, value.StorageReference)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, value.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) != value.SizeBytes || artifact.Hash(content) != value.ContentSHA256 {
		return nil, conversationError("unavailable", "artifact_content_mismatch")
	}
	return content, nil
}

var errGeneratedArtifactDownloadCAS = errors.New("generated artifact download compare-and-swap conflict")

func (s *ConversationStore) RecordArtifactDownload(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	for attempt := 0; attempt < 16; attempt++ {
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			artifactContext := sharedartifact.WithExecutor(ctx, tx)
			value, found, err := s.artifactStore.ByID(artifactContext, a.WorkspaceID, id)
			if err != nil {
				return err
			}
			if !found || !generatedArtifactOwnedBy(value, a) {
				return conversationError("not_found", "artifact_export_not_found")
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			if !now.Before(value.ExpiresAt) || value.Status != sharedartifact.StatusAvailable {
				return conversationError("conflict", "artifact_export_expired")
			}
			metadata, err := decodeGeneratedArtifactMetadata(value)
			if err != nil {
				return err
			}
			if _, err = s.artifactRecord(ctx, tx, metadata.ArtifactID, metadata.Version, a); err != nil {
				return err
			}
			metadata.Downloads++
			metadata.LastDownloadedAt = now.UnixMilli()
			raw, err := json.Marshal(metadata)
			if err != nil {
				return err
			}
			updatedAt := now
			if !updatedAt.After(value.UpdatedAt) {
				updatedAt = value.UpdatedAt.Add(time.Nanosecond)
			}
			changed, err := s.artifactStore.Update(artifactContext, sharedartifact.Mutation{
				WorkspaceID: value.WorkspaceID, ID: value.ID, Owner: value.Owner, Kind: value.Kind,
				ExpectedStatus: value.Status, ExpectedScanStatus: value.ScanStatus, ExpectedUpdatedAt: value.UpdatedAt,
				Status: value.Status, ScanStatus: value.ScanStatus, ExpiresAt: value.ExpiresAt,
				Metadata: raw, UpdatedAt: updatedAt,
			})
			if err != nil {
				return err
			}
			if !changed {
				return errGeneratedArtifactDownloadCAS
			}
			value.Metadata, value.UpdatedAt = raw, updatedAt
			out, err = generatedArtifactExport(value)
			return err
		})
		if errors.Is(err, errGeneratedArtifactDownloadCAS) {
			continue
		}
		return out, err
	}
	return out, conversationError("conflict", "revision_conflict")
}

func generatedArtifactOwnedBy(value sharedartifact.Artifact, a agentsdk.ConversationAuthority) bool {
	return value.Owner == sharedartifact.OwnerAgent && value.Kind == generatedArtifactKind && value.WorkspaceID == a.WorkspaceID && value.CreatedBy == a.UserID && value.AuthorizationScopeSHA256 == conversationOwner(a)
}

func generatedArtifactIdempotencyKey(a agentsdk.ConversationAuthority, clientID string) string {
	return conversationHash([]string{"agent-generated", conversationOwner(a), clientID})
}

func decodeGeneratedArtifactMetadata(value sharedartifact.Artifact) (generatedArtifactMetadata, error) {
	var metadata generatedArtifactMetadata
	if err := json.Unmarshal(value.Metadata, &metadata); err != nil || !personalMemoryKey(metadata.ArtifactID) || metadata.Version < 1 || metadata.Format != "markdown" && metadata.Format != "csv" || metadata.Downloads < 0 {
		return metadata, conversationError("unavailable", "artifact_export_invalid")
	}
	return metadata, nil
}

func generatedArtifactExport(value sharedartifact.Artifact) (agentsdk.ConversationArtifactExport, error) {
	metadata, err := decodeGeneratedArtifactMetadata(value)
	if err != nil {
		return agentsdk.ConversationArtifactExport{}, err
	}
	var lastDownloadedAt *time.Time
	if metadata.LastDownloadedAt != 0 {
		value := time.UnixMilli(metadata.LastDownloadedAt).UTC()
		lastDownloadedAt = &value
	}
	return agentsdk.ConversationArtifactExport{
		ID: value.ID, ArtifactID: metadata.ArtifactID, Version: metadata.Version, Format: metadata.Format,
		Filename: value.Filename, ContentType: value.MediaType, SHA256: value.ContentSHA256, Bytes: int(value.SizeBytes),
		FormulaGuarded: metadata.FormulaGuarded, CreatedAt: value.CreatedAt, ExpiresAt: value.ExpiresAt,
		Downloads: metadata.Downloads, LastDownloadedAt: lastDownloadedAt,
	}, nil
}

func sameGeneratedArtifactRequest(value sharedartifact.Artifact, expected agentsdk.ConversationArtifactExport, a agentsdk.ConversationAuthority) bool {
	if !generatedArtifactOwnedBy(value, a) || value.ID != expected.ID || value.Filename != expected.Filename || value.MediaType != expected.ContentType || value.ContentSHA256 != expected.SHA256 || value.SizeBytes != int64(expected.Bytes) || !value.ExpiresAt.Equal(expected.ExpiresAt) {
		return false
	}
	actual, err := generatedArtifactExport(value)
	return err == nil && actual.ArtifactID == expected.ArtifactID && actual.Version == expected.Version && actual.Format == expected.Format && actual.FormulaGuarded == expected.FormulaGuarded
}

func (s *ConversationStore) bindGeneratedArtifactSubject(ctx context.Context, value sharedartifact.Artifact, a agentsdk.ConversationAuthority) error {
	_, _, err := s.artifactStore.Bind(ctx, sharedartifact.Binding{
		ID: value.ID + ":subject", WorkspaceID: value.WorkspaceID, ArtifactID: value.ID,
		Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingSubject,
		ResourceType: "agent_user", ResourceID: a.UserID, CreatedAt: value.CreatedAt,
	})
	return err
}

func (s *ConversationStore) bindGeneratedArtifactConversation(ctx context.Context, value sharedartifact.Artifact, conversationID string) error {
	metadata, err := decodeGeneratedArtifactMetadata(value)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"artifact_id": metadata.ArtifactID, "artifact_version": metadata.Version})
	if err != nil {
		return err
	}
	_, _, err = s.artifactStore.Bind(ctx, sharedartifact.Binding{
		ID: value.ID + ":conversation", WorkspaceID: value.WorkspaceID, ArtifactID: value.ID,
		Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingConversation,
		ResourceType: "agent_conversation", ResourceID: conversationID, Metadata: raw, CreatedAt: value.CreatedAt,
	})
	return err
}

func (s *ConversationStore) requireGeneratedArtifactSubjectBinding(ctx context.Context, db conversationDB, value sharedartifact.Artifact, a agentsdk.ConversationAuthority) error {
	bindings, err := s.artifactStore.Bindings(sharedartifact.WithExecutor(ctx, db), value.WorkspaceID, value.ID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.Owner == sharedartifact.OwnerAgent && binding.Kind == sharedartifact.BindingSubject && binding.ResourceType == "agent_user" && binding.ResourceID == a.UserID {
			return nil
		}
	}
	return conversationError("not_found", "artifact_export_not_found")
}

var _ persistence.ConversationArtifactRepository = (*ConversationStore)(nil)
