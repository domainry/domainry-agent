package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-knowledge/extraction"
)

func (h *knowledgeConversationHost) invokeExtraction(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	var args agentsdk.KnowledgeExtractionArguments
	if json.Unmarshal([]byte(in.Call.Arguments), &args) != nil {
		return personalToolFailure("extraction_plan_invalid"), nil
	}
	if extraction.Validate(args.KnowledgeExtractionPlan) != nil {
		failure := personalToolFailure("extraction_plan_invalid")
		failure.Content, _ = json.Marshal(map[string]any{"error": "extraction_plan_invalid", "issues": extraction.Explain(args.KnowledgeExtractionPlan)})
		return failure, nil
	}
	selected, selectErr := h.extractionSource(in, &args)
	if selectErr != nil {
		return personalToolFailure("knowledge_access_denied"), nil
	}
	normalizer, ok := selected.(agentsdk.KnowledgeExtractionContentSource)
	if !ok {
		return personalToolFailure("knowledge_extraction_content_unavailable"), nil
	}
	var source agentsdk.ConversationKnowledgeResult
	var err error
	if args.LibraryID != "" {
		scoped, ok := selected.(agentsdk.ConversationLibraryKnowledgeSource)
		if !ok {
			return personalToolFailure("knowledge_access_denied"), nil
		}
		source, err = scoped.ReadLibraryKnowledge(ctx, args.LibraryID, args.DocumentID, in.Authority)
	} else {
		source, err = selected.ReadKnowledge(ctx, args.DocumentID, in.Authority)
	}
	if err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "knowledge_failed")), nil
	}
	if source.LibraryID != args.LibraryID || source.DocumentID != args.DocumentID || source.Operation != "fetch" || source.Query != "" {
		return personalToolFailure("knowledge_response_invalid"), nil
	}
	passages, err := normalizer.KnowledgeExtractionPassages(ctx, source, in.Authority)
	if err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "knowledge_extraction_content_unavailable")), nil
	}
	output, err := buildKnowledgeExtraction(ctx, args, source, passages)
	if err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "extraction_failed")), nil
	}
	// Recheck remote/current access and the live action before any extracted
	// content becomes a model input, result preview or persisted source.
	if err = selected.RevalidateKnowledge(ctx, source, in.Authority); err != nil {
		return personalToolFailure(conversationModelFailureCode(err, "knowledge_access_denied")), nil
	}
	auth, err := h.AuthorizeConversationTool(ctx, in)
	if err != nil || !auth.Granted || auth.ConfirmationRequired {
		return personalToolFailure("tool_access_denied"), nil
	}
	return personalToolResult(output)
}
func buildKnowledgeExtraction(ctx context.Context, args agentsdk.KnowledgeExtractionArguments, source agentsdk.ConversationKnowledgeResult, passages []agentsdk.KnowledgeDocumentPassage) (agentsdk.KnowledgeExtractionResult, error) {
	out := agentsdk.KnowledgeExtractionResult{Operation: "extract", DocumentID: args.DocumentID, LibraryID: args.LibraryID, PlanSHA256: conversationDigest(args.KnowledgeExtractionPlan), Citations: []agentsdk.ConversationCitation{}, Source: source}
	for _, p := range passages {
		if p.DocumentID != args.DocumentID {
			return out, conversationFailure("unavailable", "knowledge_response_invalid")
		}
	}
	data, err := extraction.Extract(ctx, args.KnowledgeExtractionPlan, passages)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		code := "extraction_failed"
		if e, ok := err.(extraction.Error); ok {
			code = string(e)
		}
		return out, conversationFailure("bad_request", code)
	}
	references := map[int]string{}
	attach := func(cell *agentsdk.KnowledgeExtractionCell) {
		for i := range cell.Evidence {
			span := &cell.Evidence[i]
			passage := passages[span.Passage]
			if passage.Location != nil {
				location := *passage.Location
				for _, sourceCell := range passage.Cells {
					if span.Start >= sourceCell.Start && span.End <= sourceCell.End {
						location.Cell = sourceCell.Address
						break
					}
				}
				span.Location = &location
			}
			id := references[span.Passage]
			if id == "" {
				p := passages[span.Passage]
				start, end := span.Start-80, span.End+160
				if start < 0 {
					start = 0
				}
				if end > len(p.Content) {
					end = len(p.Content)
				}
				for start < span.Start && !utf8.RuneStart(p.Content[start]) {
					start++
				}
				for end > span.End && end < len(p.Content) && !utf8.RuneStart(p.Content[end]) {
					end--
				}
				excerpt := p.Content[start:end]
				id = "kc_" + conversationDigest([]any{"knowledge_extract.v1", source.ScopeSHA256, source.LibraryID, source.DocumentID, out.PlanSHA256, span.Passage, start, end, excerpt})[:32]
				references[span.Passage] = id
				out.Citations = append(out.Citations, agentsdk.ConversationCitation{ID: id, Provider: "agent_knowledge_extract", KBID: source.KBID, LibraryID: source.LibraryID, Operation: "fetch", DocumentID: source.DocumentID, Title: p.Title, URL: p.URL, Location: p.Location, Excerpt: excerpt, ExcerptTruncated: start > 0 || end < len(p.Content)})
			}
			span.CitationID = id
		}
	}
	for i := range data.Fields {
		attach(&data.Fields[i])
	}
	for i := range data.Tables {
		for j := range data.Tables[i].Rows {
			for c := range data.Tables[i].Rows[j] {
				attach(&data.Tables[i].Rows[j][c])
			}
		}
	}
	out.Data = data
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > agentsdk.KnowledgeExtractionTool().MaxOutputBytes {
		return out, conversationFailure("unavailable", "extraction_context_exceeded")
	}
	return out, nil
}
func (h *knowledgeConversationHost) authorizeExtractionResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	legacy := conversationDigest(in.Definition) == conversationDigest(agentsdk.LegacyKnowledgeExtractionTool())
	if in.Call.Name != "knowledge_extract" || in.Definition.Key != "knowledge_extract" || !knownKnowledgeDefinition(in.Definition) {
		return conversationFailure("conflict", "extraction_result_invalid")
	}
	var args agentsdk.KnowledgeExtractionArguments
	var saved agentsdk.KnowledgeExtractionResult
	decoder := json.NewDecoder(bytes.NewReader(result.Content))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&saved) != nil {
		return conversationFailure("conflict", "extraction_result_invalid")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || json.Unmarshal([]byte(in.Call.Arguments), &args) != nil || extraction.Validate(args.KnowledgeExtractionPlan) != nil || legacy && args.AttachmentID != "" {
		return conversationFailure("conflict", "extraction_result_invalid")
	}
	selected, selectErr := h.extractionSource(in, &args)
	if selectErr != nil {
		return selectErr
	}
	if saved.Operation != "extract" || saved.DocumentID != args.DocumentID || saved.LibraryID != args.LibraryID || saved.Source.DocumentID != args.DocumentID || saved.Source.LibraryID != args.LibraryID || saved.Source.Operation != "fetch" || saved.Source.Query != "" {
		return conversationFailure("conflict", "extraction_result_invalid")
	}
	normalizer, ok := selected.(agentsdk.KnowledgeExtractionContentSource)
	if !ok {
		return conversationFailure("unavailable", "knowledge_extraction_content_unavailable")
	}
	fullSource := saved.Source
	passages, err := normalizer.KnowledgeExtractionPassages(ctx, fullSource, in.Authority)
	if err != nil {
		return err
	}
	expected, err := buildKnowledgeExtraction(ctx, args, fullSource, passages)
	if err != nil {
		return err
	}
	if conversationDigest(expected) != conversationDigest(saved) {
		return conversationFailure("conflict", "extraction_result_invalid")
	}
	return selected.RevalidateKnowledge(ctx, fullSource, in.Authority)
}

func (h *knowledgeConversationHost) extractionSource(in agentsdk.ConversationToolRequest, args *agentsdk.KnowledgeExtractionArguments) (agentsdk.ConversationKnowledgeSource, error) {
	if args.AttachmentID != "" || args.DocumentID == "" || h.source == nil {
		return nil, conversationFailure("forbidden", "knowledge_access_denied")
	}
	return h.source, nil
}
