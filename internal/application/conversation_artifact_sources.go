package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func decodeArtifactToolResult(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return conversationFailure("conflict", "artifact_result_invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return conversationFailure("conflict", "artifact_result_invalid")
	}
	return nil
}

func (h *PersonalConversationHost) AuthorizeConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	if _, ok := artifactTool(in.Call.Name); !ok || result.Status != "completed" {
		return nil
	}
	if h.artifacts == nil {
		return conversationFailure("unavailable", "artifacts_unavailable")
	}
	_, err := h.artifacts.sourceAudit(in.Authority, in.ConversationID).artifactToolRecord(ctx, agentsdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID}, persistence.ConversationToolExecution{Step: in.Step, Call: in.Call, Definition: in.Definition, Result: &result})
	return err
}

func (audit *conversationSourceAudit) artifactToolRecord(ctx context.Context, owner agentsdk.ConversationRunReference, execution persistence.ConversationToolExecution) ([]agentsdk.ConversationRunReference, error) {
	definition, ok := artifactTool(execution.Call.Name)
	if !ok || conversationDigest(definition) != conversationDigest(execution.Definition) {
		return nil, conversationFailure("conflict", "tool_changed")
	}
	policy := audit.s.options.PersonalAuthorizer
	repo, ok := audit.s.repo.(persistence.ConversationArtifactRepository)
	if policy == nil || !ok {
		return nil, conversationFailure("unavailable", "artifacts_unavailable")
	}
	request := agentsdk.ConversationToolRequest{Authority: audit.a, ConversationID: owner.ConversationID, RunID: owner.RunID, CorrelationID: owner.RunID, Step: execution.Step, Call: execution.Call, Definition: definition}
	decision, err := audit.s.authorizeConversationTool(ctx, policy, request)
	if err != nil {
		return nil, err
	}
	if !decision.Granted {
		return nil, conversationFailure("forbidden", "tool_access_denied")
	}
	if definition.Key == "artifact_edit" || definition.Key == "artifact_export" {
		request = artifactReadAuthorizationRequest(request)
		decision, err = audit.s.authorizeConversationTool(ctx, policy, request)
		if err != nil {
			return nil, err
		}
		if !decision.Granted {
			return nil, conversationFailure("forbidden", "tool_access_denied")
		}
	}
	var roots []agentsdk.ConversationRunReference
	check := func(meta agentsdk.ConversationArtifact) (persistence.ConversationArtifactRecord, error) {
		record, err := repo.ArtifactRecord(ctx, meta.ID, meta.Version, audit.a)
		if err != nil {
			return record, err
		}
		if meta.Version < 1 || conversationDigest(record.Artifact) != conversationDigest(meta) || record.Sources == nil || record.Sources.Version != 1 || len(record.Sources.Omitted) != 0 {
			return record, conversationFailure("conflict", "artifact_result_invalid")
		}
		part, err := audit.sources(ctx, record.Sources)
		roots = mergeConversationSources(roots, part)
		return record, err
	}
	switch definition.Key {
	case "artifact_create", "artifact_edit":
		var result struct {
			Artifact agentsdk.ConversationArtifact `json:"artifact"`
		}
		if err = decodeArtifactToolResult(execution.Result.Content, &result); err != nil {
			return nil, err
		}
		if execution.Result.ResourceID != result.Artifact.ID {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		if definition.Key == "artifact_create" {
			// A creation receipt becomes a reference to a saved resource. Its
			// later disclosure also requires read access to that exact version.
			read, _ := artifactTool("artifact_read")
			request.Definition = read
			request.Call = agentsdk.ConversationToolCall{ID: execution.Call.ID, Name: read.Key, Arguments: conversationJSONText(map[string]any{"id": result.Artifact.ID, "version": result.Artifact.Version})}
			decision, err = audit.s.authorizeConversationTool(ctx, policy, request)
			if err != nil {
				return nil, err
			}
			if !decision.Granted {
				return nil, conversationFailure("forbidden", "tool_access_denied")
			}
		}
		if _, err = check(result.Artifact); err != nil {
			return nil, err
		}
	case "artifact_read":
		var result agentsdk.ConversationArtifactReadResult
		var args agentsdk.ConversationArtifactRead
		if err = decodeArtifactToolResult(execution.Result.Content, &result); err != nil || json.Unmarshal([]byte(execution.Call.Arguments), &args) != nil {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		if args.ID != result.Artifact.ID || args.Version != 0 && args.Version != result.Artifact.Version {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		record, err := check(result.Artifact)
		if err != nil {
			return nil, err
		}
		content, err := audit.s.artifactContent(ctx, record, audit.a)
		if err != nil {
			return nil, err
		}
		expected, err := artifactReadPage(agentsdk.ConversationArtifactVersion{Artifact: record.Artifact, Content: content}, args)
		if err != nil || conversationDigest(expected) != conversationDigest(result) {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
	case "artifact_list", "artifact_versions":
		var items []agentsdk.ConversationArtifact
		if definition.Key == "artifact_list" {
			var page agentsdk.ConversationArtifactPage
			err = decodeArtifactToolResult(execution.Result.Content, &page)
			items = page.Items
		} else {
			var page agentsdk.ConversationArtifactVersions
			err = decodeArtifactToolResult(execution.Result.Content, &page)
			items = page.Items
		}
		if err != nil || len(items) > 50 {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		for _, item := range items {
			if _, err = check(item); err != nil {
				return nil, err
			}
		}
	case "artifact_export":
		var result struct {
			Export agentsdk.ConversationArtifactExport `json:"export"`
		}
		var args struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
			Format  string `json:"format"`
		}
		if err = decodeArtifactToolResult(execution.Result.Content, &result); err != nil || json.Unmarshal([]byte(execution.Call.Arguments), &args) != nil || args.ID != result.Export.ArtifactID || args.Version != result.Export.Version || args.Format != result.Export.Format || execution.Result.ResourceID != args.ID {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		metadata, err := repo.ArtifactExport(ctx, result.Export.ID, audit.a)
		if err != nil {
			return nil, err
		}
		if result.Export.Downloads != 0 || result.Export.LastDownloadedAt != nil {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		metadata.Downloads, metadata.LastDownloadedAt = 0, nil
		if conversationDigest(metadata) != conversationDigest(result.Export) {
			return nil, conversationFailure("conflict", "artifact_result_invalid")
		}
		record, err := repo.ArtifactRecord(ctx, args.ID, args.Version, audit.a)
		if err != nil {
			return nil, err
		}
		if _, err = check(record.Artifact); err != nil {
			return nil, err
		}
	}
	// Preserve this exact access dependency in summaries and derived replies,
	// including owner-created content without an underlying knowledge source.
	if owner.ConversationID != "" && owner.RunID != "" {
		roots = mergeConversationSources(roots, []agentsdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: execution.Step + 2}})
	}
	return roots, nil
}

var _ agentsdk.ConversationToolResultAuthorizer = (*PersonalConversationHost)(nil)
