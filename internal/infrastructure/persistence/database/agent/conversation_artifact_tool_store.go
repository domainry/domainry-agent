package agent

import (
	"context"
	"database/sql"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func (s *ConversationStore) ApplyArtifactTool(ctx context.Context, in agentsdk.ConversationToolRequest, prepared persistence.ConversationArtifactToolMutation) (agentsdk.ConversationToolResult, error) {
	return s.applyLocalTool(ctx, in, agentsdk.ArtifactConversationTools(), func(tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
		var zero agentsdk.ConversationToolResult
		clientID := "tool_" + conversationHash(call.IdempotencyKey)
		digest := conversationHash([]any{call.Definition, call.Call})
		if call.Call.Name == "artifact_export" {
			var args struct {
				ID      string `json:"id"`
				Version int64  `json:"version"`
				Format  string `json:"format"`
			}
			if json.Unmarshal([]byte(call.Call.Arguments), &args) != nil || prepared.Export == nil || prepared.Write != nil {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
			write := *prepared.Export
			if write.ClientID != clientID || write.RequestSHA256 != digest || write.Export.ArtifactID != args.ID || write.Export.Version != args.Version || write.Export.Format != args.Format {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
			value, err := s.saveArtifactExport(ctx, tx, write, claim.Authority)
			if err != nil {
				return zero, err
			}
			return agentsdk.ConversationToolResult{Status: "completed", ResourceID: args.ID, Content: conversationJSON(map[string]any{"export": value})}, nil
		}
		if prepared.Write == nil || prepared.Export != nil {
			return zero, conversationError("conflict", "tool_input_conflict")
		}
		write := *prepared.Write
		if write.ClientID != clientID || write.RequestSHA256 != digest || write.Record.Sources == nil {
			return zero, conversationError("conflict", "tool_input_conflict")
		}
		current := agentsdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID, BeforeStep: in.Step + 1}
		included := false
		for _, source := range write.Record.Sources.Runs {
			included = included || source == current
		}
		if !included {
			return zero, conversationError("conflict", "artifact_sources_invalid")
		}
		switch call.Call.Name {
		case "artifact_create":
			var args agentsdk.ConversationArtifactCreate
			if json.Unmarshal([]byte(call.Call.Arguments), &args) != nil || write.ExpectedVersion != 0 || write.Record.Artifact.ID != "" || write.Record.Artifact.Title != args.Title || len(write.Record.Sources.Runs) != 1 || write.Record.Artifact.SourceConversationID != in.ConversationID || write.Record.Artifact.SourceRunID != in.RunID {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
			raw, hash, err := artifact.Encode(args.Content)
			if err != nil || hash != write.Record.Artifact.SHA256 || len(raw) != write.Record.Artifact.Bytes || args.Content.Kind != write.Record.Artifact.Kind {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
		case "artifact_edit":
			var args struct {
				ID string `json:"id"`
				agentsdk.ConversationArtifactEdit
			}
			if json.Unmarshal([]byte(call.Call.Arguments), &args) != nil || args.ID != write.Record.Artifact.ID || args.ExpectedVersion != write.ExpectedVersion {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
			previous, err := s.artifactRecord(ctx, tx, args.ID, args.ExpectedVersion, claim.Authority)
			if err != nil {
				return zero, err
			}
			title := previous.Artifact.Title
			if args.Patch.Title != nil {
				title = *args.Patch.Title
			}
			if write.Record.Artifact.Title != title || write.Record.Artifact.SourceConversationID != previous.Artifact.SourceConversationID || write.Record.Artifact.SourceRunID != previous.Artifact.SourceRunID {
				return zero, conversationError("conflict", "tool_input_conflict")
			}
			// Inline bodies can be independently checked inside the transaction.
			// External immutable bodies were read and hashed by the trusted host.
			if len(previous.Body) > 0 {
				body, err := artifact.Decode(previous.Body)
				if err != nil {
					return zero, err
				}
				body, err = artifact.Edit(body, args.Patch)
				if err != nil {
					return zero, err
				}
				raw, hash, err := artifact.Encode(body)
				if err != nil || hash != write.Record.Artifact.SHA256 || len(raw) != write.Record.Artifact.Bytes || body.Kind != write.Record.Artifact.Kind {
					return zero, conversationError("conflict", "tool_input_conflict")
				}
			}
		default:
			return zero, conversationError("forbidden", "tool_access_denied")
		}
		saved, err := s.saveArtifact(ctx, tx, write, claim.Authority)
		if err != nil {
			return zero, err
		}
		return agentsdk.ConversationToolResult{Status: "completed", ResourceID: saved.Artifact.ID, Content: conversationJSON(map[string]any{"artifact": saved.Artifact})}, nil
	})
}

var _ persistence.ConversationArtifactMutationRepository = (*ConversationStore)(nil)
