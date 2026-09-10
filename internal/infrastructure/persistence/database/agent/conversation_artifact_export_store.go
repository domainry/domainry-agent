package agent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
	"github.com/domainry/domainry-orm/query"
)

func artifactExportScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("export_id", id))
}
func (s *ConversationStore) artifactExport(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	if !personalMemoryKey(id) {
		return out, conversationError("bad_request", "artifact_export_invalid")
	}
	found, err := s.executionRead(ctx, db, "_agent_artifact_exports", artifactExportScope(a, id), &out)
	if err == nil && !found {
		err = conversationError("not_found", "artifact_export_not_found")
	}
	return out, err
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
	// These fields are server-owned and must not change the logical request's
	// identity when a caller repeats an export after a lost response.
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

func (s *ConversationStore) saveArtifactExport(ctx context.Context, tx *sql.Tx, in persistence.ConversationArtifactExportWrite, a agentsdk.ConversationAuthority) (any, error) {
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 3600
	}
	value := in.Export
	value.Downloads, value.LastDownloadedAt = 0, nil
	if value.Version < 1 || !artifactSHA(value.SHA256) || value.Bytes < 0 || value.Bytes > 2*artifact.MaxBytes || in.TTLSeconds < 1 || in.TTLSeconds > 24*3600 {
		return nil, conversationError("bad_request", "artifact_export_invalid")
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
		content, err := artifact.Decode(record.Body)
		if err != nil {
			return nil, err
		}
		data, err := artifact.Export(content, value.Format)
		if err != nil {
			return nil, err
		}
		if value.SHA256 != artifact.Hash(data.Data) || value.Bytes != len(data.Data) || value.FormulaGuarded != data.FormulaGuarded {
			return nil, conversationError("bad_request", "artifact_export_mismatch")
		}
	}
	value.ID = "exp_" + conversationHash([]string{conversationOwner(a), in.ClientID})[:32]
	value.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	value.ExpiresAt = value.CreatedAt.Add(time.Duration(in.TTLSeconds) * time.Second)
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_artifact_exports").Columns("owner_key", "export_id", "artifact_id", "expires_at", "download_count", "payload_json").Values(conversationOwner(a), value.ID, value.ArtifactID, value.ExpiresAt.UnixMilli(), 0, conversationJSON(value)).Build()
	if err = conversationExec(ctx, tx, statement, args, err); err != nil {
		return nil, err
	}
	return value, nil
}

// The application rechecks export action and source access before calling
// this method. Expiry and the download audit update are checked atomically.
func (s *ConversationStore) RecordArtifactDownload(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		value, err := s.artifactExport(ctx, tx, id, a)
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		if !now.Before(value.ExpiresAt) {
			return conversationError("conflict", "artifact_export_expired")
		}
		if _, err = s.artifactRecord(ctx, tx, value.ArtifactID, value.Version, a); err != nil {
			return err
		}
		previous := value.Downloads
		value.Downloads++
		value.LastDownloadedAt = &now
		statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), "_agent_artifact_exports").Set("download_count", value.Downloads).Set("payload_json", conversationJSON(value)).Where(query.And(artifactExportScope(a, id), query.Equal("download_count", previous))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return err
		}
		out = value
		return nil
	})
	return out, err
}

var _ persistence.ConversationArtifactRepository = (*ConversationStore)(nil)
