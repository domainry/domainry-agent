package application

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type conversationResultPreview struct {
	Representation  string                               `json:"representation"`
	Status          string                               `json:"status"`
	ResourceID      string                               `json:"resource_id,omitempty"`
	ErrorCode       string                               `json:"error_code,omitempty"`
	Reference       agentsdk.ConversationResultReference `json:"reference"`
	TotalBytes      int                                  `json:"total_bytes"`
	JSONPreview     string                               `json:"json_preview"`
	PreviewBytes    int                                  `json:"preview_bytes"`
	ContentComplete bool                                 `json:"content_complete"`
}

// Compress only result bodies. Original user messages, assistant text, tool
// calls/arguments and opaque native continuation blocks remain byte-identical.
// Every call still has exactly its paired tool response. No model is asked to
// invent a summary of an operation's outcome or an unresolved user request.
func (s *ConversationService) compactConversationExecution(ctx context.Context, claim persistence.ConversationClaim, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepRequest, error) {
	original, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	_, storageReady := s.repo.(persistence.ConversationResultRepository)
	if !storageReady || !resultReadAvailable(in) {
		if len(original) > s.options.ContextBytes {
			return in, conversationFailure("rate_limited", "execution_context_exceeded")
		}
		return in, nil
	}
	current, err := compileConversationTools(in.Tools)
	if err != nil {
		return in, err
	}
	in.Messages = append([]agentsdk.ConversationStepMessage(nil), in.Messages...)
	latestCall := -1
	latestReads := map[string]bool{}
	for index, message := range in.Messages {
		if message.Role == "assistant" && len(message.ToolCalls) > 0 {
			latestCall = index
			latestReads = map[string]bool{}
			for _, call := range message.ToolCalls {
				latestReads[call.ID] = call.Name == "tool_result_read"
			}
		}
	}
	changed := map[int]bool{}
	for _, limit := range []int{min(8192, s.options.ContextBytes/4), 1024} {
		for index, message := range in.Messages {
			// The page explicitly requested in the latest step must be usable
			// on the next model call. Replacing it with another reference would
			// force a loop of reads whose requested content is never delivered.
			if index > latestCall && latestReads[message.ToolCallID] {
				continue
			}
			if message.Role != "tool" || message.ResultReference == nil || len(message.Content) <= limit {
				continue
			}
			result, err := s.authorizedConversationResult(ctx, *message.ResultReference, claim.Authority, current, map[string]bool{})
			if err != nil {
				return in, err
			}
			preview, err := previewConversationResult(result, *message.ResultReference, limit)
			if err != nil {
				return in, err
			}
			if len(preview) >= len(message.Content) {
				continue
			}
			in.Messages[index].Content = preview
			changed[index] = true
		}
		if len(changed) > 0 {
			in.Compaction = &agentsdk.ConversationContextCompaction{Version: 1, BeforeBytes: len(original), Results: len(changed)}
			// The byte count includes its own serialized digits and converges
			// after at most a few passes; it is not an estimated token count.
			for n := 0; n < 8; n++ {
				raw, err := json.Marshal(in)
				if err != nil {
					return in, err
				}
				if in.Compaction.AfterBytes == len(raw) {
					break
				}
				in.Compaction.AfterBytes = len(raw)
			}
		}
		raw, err := json.Marshal(in)
		if err != nil {
			return in, err
		}
		if len(raw) <= s.options.ContextBytes {
			return in, nil
		}
	}
	return in, conversationFailure("rate_limited", "execution_context_exceeded")
}

func previewConversationResult(result agentsdk.ConversationToolResult, ref agentsdk.ConversationResultReference, limit int) (string, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	preview := conversationResultPreview{Representation: "stored_result_preview", Status: result.Status, ResourceID: result.ResourceID, ErrorCode: result.ErrorCode, Reference: ref, TotalBytes: len(raw)}
	best, err := json.Marshal(preview)
	if err != nil {
		return "", err
	}
	// Count JSON escaping and framing, not just the unescaped excerpt. A long
	// resource identifier may itself exceed the target; it is retained intact.
	low, high := 0, min(len(raw), limit)
	for low <= high {
		mid := low + (high-low)/2
		end := mid
		for end < len(raw) && end > 0 && !utf8.RuneStart(raw[end]) {
			end--
		}
		preview.JSONPreview, preview.PreviewBytes = string(raw[:end]), end
		encoded, err := json.Marshal(preview)
		if err != nil {
			return "", err
		}
		if len(encoded) <= limit {
			best = encoded
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return string(best), nil
}
