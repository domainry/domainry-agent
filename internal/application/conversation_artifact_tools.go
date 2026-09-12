package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-knowledge/artifact"
)

func (h *PersonalConversationHost) sourceService() *ConversationService {
	if h.artifacts != nil {
		return h.artifacts
	}
	return &ConversationService{repo: h.repo, options: ConversationOptions{Knowledge: h.knowledge, PersonalAuthorizer: h.authorizer}}
}

func artifactReadAuthorizationRequest(in agentsdk.ConversationToolRequest) agentsdk.ConversationToolRequest {
	request := in
	request.Definition, _ = artifactTool("artifact_read")
	request.Call = agentsdk.ConversationToolCall{}
	if in.Call.Arguments != "" {
		var args struct {
			ID              string `json:"id"`
			Version         int64  `json:"version"`
			ExpectedVersion int64  `json:"expected_version"`
		}
		_ = json.Unmarshal([]byte(in.Call.Arguments), &args)
		version := args.Version
		if in.Call.Name == "artifact_edit" {
			version = args.ExpectedVersion
		}
		request.Call = agentsdk.ConversationToolCall{ID: in.Call.ID, Name: "artifact_read", Arguments: conversationJSONText(map[string]any{"id": args.ID, "version": version})}
	}
	return request
}

func (h *PersonalConversationHost) invokeArtifactTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	s := h.artifacts
	if in.Definition.Effect == "write" {
		prepared, err := s.prepareArtifactTool(ctx, in)
		if err != nil {
			// No artifact/version/export transaction has started. A missing ID,
			// invalid edit or failed source read is a definite preparation failure,
			// not an uncertain external write requiring user reconciliation.
			return artifactPreparationFailure(err), nil
		}
		// Preparation may involve source services or blob storage. Reauthorize
		// immediately before the transaction rechecks the live execution lease.
		auth, err := h.AuthorizeConversationTool(ctx, in)
		if err != nil {
			return artifactPreparationFailure(err), nil
		}
		if !auth.Granted || auth.ConfirmationRequired {
			return personalToolFailure("tool_access_denied"), nil
		}
		return h.repo.(persistence.ConversationArtifactMutationRepository).ApplyArtifactTool(ctx, in, prepared)
	}
	switch in.Call.Name {
	case "artifact_list":
		var query agentsdk.ConversationArtifactQuery
		if err := json.Unmarshal([]byte(in.Call.Arguments), &query); err != nil {
			return personalToolFailure("arguments_invalid"), nil
		}
		page, err := s.Artifacts(ctx, query, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return personalToolResult(page)
	case "artifact_read":
		var query agentsdk.ConversationArtifactRead
		if err := json.Unmarshal([]byte(in.Call.Arguments), &query); err != nil {
			return personalToolFailure("arguments_invalid"), nil
		}
		value, err := s.Artifact(ctx, query.ID, query.Version, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		page, err := artifactReadPage(value, query)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return personalToolResult(page)
	case "artifact_versions":
		var query struct {
			ID     string `json:"id"`
			Before int64  `json:"before"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(in.Call.Arguments), &query); err != nil {
			return personalToolFailure("arguments_invalid"), nil
		}
		page, err := s.ArtifactVersions(ctx, query.ID, query.Before, query.Limit, in.Authority)
		if err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return personalToolResult(page)
	}
	return personalToolFailure("tool_access_denied"), nil
}

func artifactPreparationFailure(err error) agentsdk.ConversationToolResult {
	code := "artifact_preparation_failed"
	message := "No artifact change or export was committed. Preparation failed; inspect the current source and tool arguments before retrying."
	var failure *agentsdk.Error
	if errors.As(err, &failure) {
		candidate := strings.TrimPrefix(failure.Code, "agent.conversation.")
		switch candidate {
		case "artifact_not_found", "artifact_version_not_found":
			code = candidate
			message = "No artifact change or export was committed. Locate the actual artifact ID and version using artifact_list or artifact_versions; do not invent an ID."
		case "artifact_patch_invalid", "artifact_text_ambiguous":
			code = candidate
			message = "No artifact change was committed. Read the current artifact text and use an exact, unambiguous replacement with the expected version."
		case "artifact_export_format_invalid", "artifact_content_invalid", "artifact_too_large", "artifact_invalid", "artifact_version_invalid", "tool_access_denied":
			code = candidate
		}
	}
	raw, _ := json.Marshal(map[string]string{"error": code, "message": message})
	return agentsdk.ConversationToolResult{Status: "failed", ErrorCode: code, Content: raw}
}

func (s *ConversationService) prepareArtifactTool(ctx context.Context, in agentsdk.ConversationToolRequest) (persistence.ConversationArtifactToolMutation, error) {
	var out persistence.ConversationArtifactToolMutation
	clientID := "tool_" + conversationDigest(in.IdempotencyKey)
	digest := conversationDigest([]any{in.Definition, in.Call})
	if in.Call.Name == "artifact_export" {
		var args struct {
			ID string `json:"id"`
			agentsdk.ConversationArtifactExportRequest
		}
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return out, err
		}
		version, err := s.Artifact(ctx, args.ID, args.Version, in.Authority)
		if err != nil {
			return out, err
		}
		data, err := artifact.Export(version.Content, args.Format)
		if err != nil {
			return out, err
		}
		value := agentsdk.ConversationArtifactExport{ArtifactID: args.ID, Version: args.Version, Format: args.Format, Filename: fmt.Sprintf("%s-v%d%s", args.ID, args.Version, data.Extension), ContentType: data.ContentType, SHA256: artifact.Hash(data.Data), Bytes: len(data.Data), FormulaGuarded: data.FormulaGuarded}
		out.Export = &persistence.ConversationArtifactExportWrite{ClientID: clientID, RequestSHA256: digest, TTLSeconds: int64(s.options.ArtifactExportTTL / time.Second), Export: value}
		return out, nil
	}
	if !conversationKey(in.ConversationID) || !conversationKey(in.RunID) || in.Step < 0 || in.Step >= 256 {
		return out, conversationFailure("bad_request", "artifact_source_run_required")
	}
	sources := &agentsdk.ConversationSources{Version: 1, Runs: []agentsdk.ConversationRunReference{{ConversationID: in.ConversationID, RunID: in.RunID, BeforeStep: in.Step + 1}}}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	if _, err := s.sourceAudit(in.Authority, in.ConversationID).sources(ctx, sources); err != nil {
		return out, err
	}
	var record persistence.ConversationArtifactRecord
	var content agentsdk.ConversationArtifactContent
	var expected int64
	switch in.Call.Name {
	case "artifact_create":
		var args agentsdk.ConversationArtifactCreate
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return out, err
		}
		if !artifact.ValidTitle(args.Title) {
			return out, conversationFailure("bad_request", "artifact_invalid")
		}
		record = persistence.ConversationArtifactRecord{Artifact: agentsdk.ConversationArtifact{Title: args.Title, SourceConversationID: in.ConversationID, SourceRunID: in.RunID}, Sources: sources}
		content = args.Content
	case "artifact_edit":
		var args struct {
			ID string `json:"id"`
			agentsdk.ConversationArtifactEdit
		}
		if err := json.Unmarshal([]byte(in.Call.Arguments), &args); err != nil {
			return out, err
		}
		var err error
		record, err = s.repo.(persistence.ConversationArtifactRepository).ArtifactRecord(ctx, args.ID, args.ExpectedVersion, in.Authority)
		if err != nil {
			return out, err
		}
		previous, err := s.artifactView(ctx, record, in.Authority)
		if err != nil {
			return out, err
		}
		content, err = artifact.Edit(previous.Content, args.Patch)
		if err != nil {
			return out, err
		}
		if args.Patch.Title != nil {
			record.Artifact.Title = *args.Patch.Title
		}
		sources.Runs = mergeConversationSources(record.Sources.Runs, sources.Runs)
		record.Sources = sources
		expected = args.ExpectedVersion
	default:
		return out, conversationFailure("forbidden", "tool_access_denied")
	}
	record, err := s.artifactBody(ctx, record, content, in.Authority)
	if err != nil {
		return out, err
	}
	out.Write = &persistence.ConversationArtifactWrite{ClientID: clientID, RequestSHA256: digest, ExpectedVersion: expected, Record: record}
	return out, nil
}

func artifactReadPage(value agentsdk.ConversationArtifactVersion, in agentsdk.ConversationArtifactRead) (agentsdk.ConversationArtifactReadResult, error) {
	out := agentsdk.ConversationArtifactReadResult{Artifact: value.Artifact, Offset: in.Offset, NextOffset: in.Offset}
	if in.Offset < 0 {
		return out, conversationFailure("bad_request", "artifact_offset_invalid")
	}
	if value.Content.Kind == "markdown" {
		body := value.Content.Markdown
		if in.Offset > len(body) || in.Offset < len(body) && !utf8.RuneStart(body[in.Offset]) || len(in.Columns) != 0 {
			return out, conversationFailure("bad_request", "artifact_offset_invalid")
		}
		if in.MaxBytes == 0 {
			in.MaxBytes = 8192
		}
		if in.MaxBytes < 256 || in.MaxBytes > 16384 {
			return out, conversationFailure("bad_request", "artifact_query_invalid")
		}
		end := min(in.Offset+in.MaxBytes, len(body))
		for end < len(body) && !utf8.RuneStart(body[end]) {
			end--
		}
		out.Markdown, out.NextOffset, out.Complete = body[in.Offset:end], end, end == len(body)
		return out, nil
	}
	table := value.Content.Table
	if table == nil || in.Offset > len(table.Rows) {
		return out, conversationFailure("bad_request", "artifact_offset_invalid")
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if in.Limit < 1 || in.Limit > 100 {
		return out, conversationFailure("bad_request", "artifact_query_invalid")
	}
	selected := map[string]bool{}
	for _, key := range in.Columns {
		if selected[key] {
			return out, conversationFailure("bad_request", "artifact_columns_invalid")
		}
		selected[key] = true
	}
	out.Table = &agentsdk.ConversationArtifactTable{Columns: []agentsdk.ConversationArtifactColumn{}, Rows: [][]*string{}}
	indexes := []int{}
	for i, column := range table.Columns {
		if len(selected) == 0 || selected[column.Key] {
			out.Table.Columns = append(out.Table.Columns, column)
			indexes = append(indexes, i)
		}
	}
	if len(selected) > 0 && len(indexes) != len(selected) {
		return out, conversationFailure("bad_request", "artifact_columns_invalid")
	}
	out.Projected, out.Chart = len(indexes) != len(table.Columns), value.Content.Chart
	for i := in.Offset; i < len(table.Rows) && i < in.Offset+in.Limit; i++ {
		row := make([]*string, 0, len(indexes))
		for _, index := range indexes {
			row = append(row, table.Rows[i][index])
		}
		out.Table.Rows = append(out.Table.Rows, row)
		raw, _ := json.Marshal(out)
		if len(raw) > 512*1024 {
			out.Table.Rows = out.Table.Rows[:len(out.Table.Rows)-1]
			out.RowTooLarge = len(out.Table.Rows) == 0
			break
		}
		out.NextOffset = i + 1
	}
	out.Complete = out.NextOffset == len(table.Rows)
	return out, nil
}
