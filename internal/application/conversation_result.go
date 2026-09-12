package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func resultReadAvailable(in agentsdk.ConversationStepRequest) bool {
	for _, definition := range in.Tools {
		if definition.Key == "tool_result_read" {
			return true
		}
	}
	return false
}

// The source's current definition and authorization are mandatory even when
// the reader itself is permitted. Nested reads retain their original sources.
func (s *ConversationService) authorizedConversationResult(ctx context.Context, ref agentsdk.ConversationResultReference, a agentsdk.ConversationAuthority, current map[string]conversationCompiledTool, seen map[string]bool, recipient string) (agentsdk.ConversationToolResult, error) {
	var empty agentsdk.ConversationToolResult
	repo, ok := s.repo.(persistence.ConversationResultRepository)
	if !ok {
		return empty, conversationFailure("unavailable", "result_read_unavailable")
	}
	source, err := repo.ConversationResult(ctx, ref, a)
	if err != nil {
		return empty, err
	}
	if source.Result == nil || source.State != "completed" || source.Call.ID != ref.CallID || source.Step != ref.Step || conversationDigest(source.Result) != ref.SHA256 {
		return empty, conversationFailure("conflict", "result_reference_changed")
	}
	if err = s.authorizeConversationRecord(ctx, ref.ConversationID, ref.RunID, source, a, current, seen, recipient); err != nil {
		return empty, err
	}
	return *source.Result, nil
}

func (s *ConversationService) readConversationToolResult(ctx context.Context, in agentsdk.ConversationToolRequest) agentsdk.ConversationToolResult {
	var args agentsdk.ConversationResultRead
	if json.Unmarshal([]byte(in.Call.Arguments), &args) != nil {
		return personalToolFailure("result_reference_invalid")
	}
	if args.Reference.RunID == in.RunID && args.Reference.ConversationID == in.ConversationID && args.Reference.Step >= in.Step {
		return personalToolFailure("result_reference_invalid")
	}
	_, current, err := s.executionCatalog(ctx, in.Authority)
	if err != nil {
		return personalToolFailure("result_read_failed")
	}
	result, err := s.authorizedConversationResult(ctx, args.Reference, in.Authority, current, map[string]bool{}, in.ConversationID)
	if err != nil {
		var coded *agentsdk.Error
		if errors.As(err, &coded) {
			return personalToolFailure(strings.TrimPrefix(coded.Code, "agent.conversation."))
		}
		return personalToolFailure("result_read_failed")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return personalToolFailure("result_read_failed")
	}
	if args.MaxBytes == 0 {
		args.MaxBytes = 4096
	}
	if args.MaxBytes < 256 || args.MaxBytes > 8192 || args.Offset < 0 || args.Offset > len(raw) || args.Offset < len(raw) && !utf8.RuneStart(raw[args.Offset]) {
		return personalToolFailure("result_offset_invalid")
	}
	end := min(len(raw), args.Offset+args.MaxBytes)
	// max_bytes is an upper bound. Escaped content may need a smaller slice
	// so the requested page is useful as model input, rather than immediately
	// becoming another oversized result requiring a second reference lookup.
	for {
		for end < len(raw) && !utf8.RuneStart(raw[end]) {
			end--
		}
		out, err := personalToolResult(agentsdk.ConversationResultSlice{Reference: args.Reference, JSONText: string(raw[args.Offset:end]), Offset: args.Offset, NextOffset: end, TotalBytes: len(raw), Complete: end == len(raw)})
		if err != nil {
			return personalToolFailure("result_read_failed")
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			return personalToolFailure("result_read_failed")
		}
		if len(encoded) <= min(8192, s.options.ContextBytes/4) {
			return out
		}
		if end <= args.Offset {
			return personalToolFailure("result_page_budget_exceeded")
		}
		end = args.Offset + (end-args.Offset)/2
	}
}
