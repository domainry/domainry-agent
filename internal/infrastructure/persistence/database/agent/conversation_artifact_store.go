package agent

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
	"github.com/domainry/domainry-orm/query"
)

func artifactScope(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("artifact_id", id))
}
func artifactSHA(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}
func (s *ConversationStore) artifactHead(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifact, error) {
	var out agentsdk.ConversationArtifact
	if !personalMemoryKey(id) {
		return out, conversationError("bad_request", "artifact_invalid")
	}
	found, err := s.executionRead(ctx, db, "_agent_artifacts", artifactScope(a, id), &out)
	if err == nil && !found {
		err = conversationError("not_found", "artifact_not_found")
	}
	return out, err
}
func (s *ConversationStore) artifactRecord(ctx context.Context, db conversationDB, id string, version int64, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	var out persistence.ConversationArtifactRecord
	head, err := s.artifactHead(ctx, db, id, a)
	if err != nil {
		return out, err
	}
	if version == 0 {
		version = head.Version
	}
	if version < 1 || version > head.Version {
		return out, conversationError("not_found", "artifact_version_not_found")
	}
	found, err := s.executionRead(ctx, db, "_agent_artifact_versions", query.And(artifactScope(a, id), query.Equal("version", version)), &out)
	if err == nil && !found {
		err = conversationError("not_found", "artifact_version_not_found")
	}
	return out, err
}
func (s *ConversationStore) ArtifactRecord(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	if err := conversationAuthority(a); err != nil {
		return persistence.ConversationArtifactRecord{}, err
	}
	return s.artifactRecord(ctx, s.store.Database(), id, version, a)
}

func (s *ConversationStore) artifactMutation(ctx context.Context, clientID, operation string, input any, a agentsdk.ConversationAuthority, apply func(*sql.Tx) (any, error), out any) error {
	if err := conversationAuthority(a); err != nil {
		return err
	}
	if !personalMemoryKey(clientID) {
		return conversationError("bad_request", "artifact_client_id_required")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var receipt struct {
			Hash   string
			Result json.RawMessage
		}
		hash := conversationHash([]any{operation, input})
		found, err := s.executionRead(ctx, tx, "_agent_artifact_mutations", query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("client_key", conversationHash(clientID))), &receipt)
		if err != nil {
			return err
		}
		if found {
			if receipt.Hash != hash {
				return conversationError("conflict", "idempotency_conflict")
			}
			return json.Unmarshal(receipt.Result, out)
		}
		value, err := apply(tx)
		if err != nil {
			return err
		}
		receipt.Hash, receipt.Result = hash, conversationJSON(value)
		statement, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_artifact_mutations").Columns("owner_key", "client_key", "created_at", "payload_json").Values(conversationOwner(a), conversationHash(clientID), time.Now().UnixMilli(), conversationJSON(receipt)).Build()
		if err = conversationExec(ctx, tx, statement, args, err); err != nil {
			return err
		}
		return json.Unmarshal(receipt.Result, out)
	})
}

func validateArtifactRecord(in persistence.ConversationArtifactRecord) error {
	m := in.Artifact
	if !artifact.ValidTitle(m.Title) || m.Kind != "markdown" && m.Kind != "table" && m.Kind != "chart" || !artifactSHA(m.SHA256) || m.Bytes < 1 || m.Bytes > artifact.MaxBytes || (len(in.Body) == 0) == (in.BodyRef == "") || !executionText(in.BodyRef, 1024, false) {
		return conversationError("bad_request", "artifact_invalid")
	}
	if len(in.Body) > 0 {
		if len(in.Body) > artifact.InlineBytes {
			return conversationError("bad_request", "artifact_storage_required")
		}
		content, err := artifact.Decode(in.Body)
		if err != nil {
			return err
		}
		if content.Kind != m.Kind || len(in.Body) != m.Bytes || artifact.Hash(in.Body) != m.SHA256 {
			return conversationError("bad_request", "artifact_content_mismatch")
		}
	}
	if in.Sources == nil || in.Sources.Version != 1 || len(in.Sources.Runs) > 256 || len(in.Sources.Omitted) != 0 {
		return conversationError("bad_request", "artifact_sources_invalid")
	}
	if m.SourceRunID != "" && m.SourceConversationID == "" {
		return conversationError("bad_request", "artifact_sources_invalid")
	}
	return nil
}

func (s *ConversationStore) saveArtifact(ctx context.Context, tx *sql.Tx, in persistence.ConversationArtifactWrite, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	out := in.Record
	if err := validateArtifactRecord(out); err != nil {
		return out, err
	}
	if in.ExpectedVersion < 0 || in.ExpectedVersion >= 1000 || (out.Artifact.ID == "") != (in.ExpectedVersion == 0) {
		return out, conversationError("bad_request", "artifact_version_invalid")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if out.Artifact.SourceConversationID != "" {
		if _, err := s.get(ctx, tx, out.Artifact.SourceConversationID, a); err != nil {
			return out, err
		}
	}
	if out.Artifact.SourceRunID != "" {
		if _, err := s.runRow(ctx, tx, out.Artifact.SourceConversationID, out.Artifact.SourceRunID, a); err != nil {
			return out, err
		}
	}
	for _, ref := range out.Sources.Runs {
		if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 {
			return out, conversationError("bad_request", "artifact_sources_invalid")
		}
		if _, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, a); err != nil {
			return out, err
		}
	}
	if in.ExpectedVersion == 0 {
		out.Artifact.ID = "art_" + conversationHash([]string{conversationOwner(a), in.ClientID})[:32]
		out.Artifact.CreatedAt = now
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_artifacts").Projections(query.Project(query.CountAll())).Where(query.Equal("owner_key", conversationOwner(a))).Build()
		if err != nil {
			return out, err
		}
		var count int
		if err = tx.QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
			return out, err
		}
		if count >= 1000 {
			return out, conversationError("conflict", "artifact_limit")
		}
	} else {
		previous, err := s.artifactRecord(ctx, tx, out.Artifact.ID, in.ExpectedVersion, a)
		if err != nil {
			return out, err
		}
		out.Artifact.CreatedAt = previous.Artifact.CreatedAt
		if previous.Sources == nil || previous.Sources.Version != 1 {
			return out, conversationError("unavailable", "artifact_sources_invalid")
		}
		// Replacing content cannot discard provenance inherited from an old
		// revision. New source dependencies may only add to this set.
		present := map[agentsdk.ConversationRunReference]bool{}
		for _, ref := range out.Sources.Runs {
			present[ref] = true
		}
		for _, ref := range previous.Sources.Runs {
			if !present[ref] {
				return out, conversationError("bad_request", "artifact_sources_invalid")
			}
		}
	}
	out.Artifact.Version = in.ExpectedVersion + 1
	out.Artifact.UpdatedAt = now
	if in.ExpectedVersion == 0 {
		statement, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_artifacts").Columns("owner_key", "artifact_id", "version", "created_at", "source_conversation_id", "payload_json").Values(conversationOwner(a), out.Artifact.ID, out.Artifact.Version, out.Artifact.CreatedAt.UnixMilli(), out.Artifact.SourceConversationID, conversationJSON(out.Artifact)).Build()
		if err = conversationExec(ctx, tx, statement, args, err); err != nil {
			return out, err
		}
	} else {
		statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), "_agent_artifacts").Set("version", out.Artifact.Version).Set("source_conversation_id", out.Artifact.SourceConversationID).Set("payload_json", conversationJSON(out.Artifact)).Where(query.And(artifactScope(a, out.Artifact.ID), query.Equal("version", in.ExpectedVersion))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return out, err
		}
	}
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), "_agent_artifact_versions").Columns("owner_key", "artifact_id", "version", "payload_json").Values(conversationOwner(a), out.Artifact.ID, out.Artifact.Version, conversationJSON(out)).Build()
	if err = conversationExec(ctx, tx, statement, args, err); err != nil {
		return out, err
	}
	return out, nil
}
func (s *ConversationStore) SaveArtifact(ctx context.Context, in persistence.ConversationArtifactWrite, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	var out persistence.ConversationArtifactRecord
	key, err := artifactRequestKey(in.RequestSHA256, in)
	if err != nil {
		return out, err
	}
	err = s.artifactMutation(ctx, in.ClientID, "write", key, a, func(tx *sql.Tx) (any, error) { return s.saveArtifact(ctx, tx, in, a) }, &out)
	return out, err
}

func artifactRequestKey(digest string, fallback any) (any, error) {
	if digest == "" {
		return fallback, nil
	}
	if !artifactSHA(digest) {
		return nil, conversationError("bad_request", "artifact_request_invalid")
	}
	return struct{ RequestSHA256 string }{digest}, nil
}
