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

func (s *ConversationService) conversationStepInputBytes(ctx context.Context, in agentsdk.ConversationStepRequest) (int, error) {
	size, _, err := s.conversationStepInputMeasurement(ctx, in)
	return size, err
}

func (s *ConversationService) conversationStepInputMeasurement(ctx context.Context, in agentsdk.ConversationStepRequest) (int, bool, error) {
	if meter, ok := s.conversationModel(ctx).(agentsdk.ConversationStepInputSizer); ok {
		size, err := meter.ConversationStepInputBytes(in)
		if err != nil {
			return 0, true, err
		}
		if size < 1 {
			return 0, true, conversationFailure("unavailable", "model_context_size_invalid")
		}
		return size, true, nil
	}
	raw, err := json.Marshal(in)
	return len(raw), false, err
}

func (s *ConversationService) finalizeConversationStepContext(ctx context.Context, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepRequest, error) {
	updateConversationContextHashes(&in)
	for iteration := 0; iteration < 16; iteration++ {
		stable := true
		size, exact, err := s.conversationStepInputMeasurement(ctx, in)
		if err != nil {
			return in, err
		}
		window := &agentsdk.ConversationContextWindow{LimitBytes: s.options.ContextBytes, InputBytes: size, PressurePermille: min(1000, size*1000/max(1, s.options.ContextBytes)), ProviderSerialized: exact}
		if in.ContextWindow == nil || *in.ContextWindow != *window {
			in.ContextWindow = window
			stable = false
		}
		if in.Compaction != nil {
			raw, err := json.Marshal(in)
			if err != nil {
				return in, err
			}
			if in.Compaction.AfterBytes != len(raw) {
				in.Compaction.AfterBytes = len(raw)
				stable = false
			}
		}
		if stable {
			return in, nil
		}
	}
	return in, conversationFailure("unavailable", "model_context_size_invalid")
}

// The first pass compresses only result bodies. Original user messages,
// assistant text, tool calls/arguments and opaque native continuation blocks
// remain byte-identical. If pressure remains, the interval pass below creates
// recoverable server checkpoints. No model is asked to invent a summary of an
// operation's outcome or an unresolved user request.
func (s *ConversationService) compactConversationExecution(ctx context.Context, claim persistence.ConversationClaim, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepRequest, error) {
	var err error
	in, err = s.finalizeConversationStepContext(ctx, in)
	if err != nil {
		return in, err
	}
	original, err := json.Marshal(in)
	if err != nil {
		return in, err
	}
	inputBytes := in.ContextWindow.InputBytes
	_, storageReady := s.repo.(persistence.ConversationResultRepository)
	if !storageReady || !resultReadAvailable(in) {
		if inputBytes > s.options.ContextBytes {
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
	// If excerpts still consume the remaining budget, retain only the exact
	// outcome and read locator. The latest requested page stays intact.
	for _, limit := range []int{min(8192, s.options.ContextBytes/4), 1024, 0} {
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
			result, err := s.authorizedConversationResult(ctx, *message.ResultReference, claim.Authority, current, map[string]bool{}, claim.Run.ConversationID)
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
		inputBytes, err = s.conversationStepInputBytes(ctx, in)
		if err != nil {
			return in, err
		}
		if inputBytes <= s.options.ContextBytes {
			finalized, err := s.finalizeConversationStepContext(ctx, in)
			if err != nil {
				return in, err
			}
			if finalized.ContextWindow.InputBytes <= s.options.ContextBytes && (finalized.Compaction == nil || finalized.Compaction.AfterBytes < finalized.Compaction.BeforeBytes) {
				return finalized, nil
			}
			in = finalized
		}
	}
	for {
		compacted, ok, err := s.compactConversationExecutionIntervals(ctx, claim, in, len(original))
		if err != nil {
			return in, err
		}
		if !ok {
			break
		}
		finalized, err := s.finalizeConversationStepContext(ctx, compacted)
		if err != nil {
			return in, err
		}
		if finalized.ContextWindow.InputBytes <= s.options.ContextBytes && finalized.Compaction.AfterBytes < finalized.Compaction.BeforeBytes {
			return finalized, nil
		}
		in = finalized
	}
	return in, conversationFailure("rate_limited", "execution_context_exceeded")
}

type archivedConversationExecutionCall struct {
	ID              string                                `json:"id"`
	Name            string                                `json:"name"`
	Arguments       string                                `json:"arguments"`
	Status          string                                `json:"status"`
	ErrorCode       string                                `json:"error_code,omitempty"`
	ResourceID      string                                `json:"resource_id,omitempty"`
	ResultReference *agentsdk.ConversationResultReference `json:"result_reference"`
}

type archivedConversationExecutionInterval struct {
	Representation string                              `json:"representation"`
	ConversationID string                              `json:"conversation_id"`
	RunID          string                              `json:"run_id"`
	Step           int                                 `json:"step"`
	AssistantText  string                              `json:"assistant_text"`
	Calls          []archivedConversationExecutionCall `json:"calls"`
	Recovery       string                              `json:"recovery"`
}

// Older completed model/tool intervals can discard opaque provider state and
// inline result bodies after every call/result pair has a current-authorized
// immutable locator. Assistant text and exact arguments remain in the compact
// checkpoint; tool_result_read restores result bytes on demand.
func (s *ConversationService) compactConversationExecutionIntervals(ctx context.Context, claim persistence.ConversationClaim, in agentsdk.ConversationStepRequest, before int) (agentsdk.ConversationStepRequest, bool, error) {
	current, err := compileConversationTools(in.Tools)
	if err != nil {
		return in, false, err
	}
	seen := map[string]bool{}
	for {
		latestCall := -1
		for index, message := range in.Messages {
			if message.Role == "assistant" && len(message.ToolCalls) > 0 {
				latestCall = index
			}
		}
		compacted := false
		for index := 0; index >= 0 && index < latestCall; index++ {
			message := in.Messages[index]
			if message.Role != "assistant" || len(message.ToolCalls) == 0 || index+len(message.ToolCalls) >= len(in.Messages) {
				continue
			}
			results := make(map[string]agentsdk.ConversationStepMessage, len(message.ToolCalls))
			valid := true
			for offset := 1; offset <= len(message.ToolCalls); offset++ {
				result := in.Messages[index+offset]
				if result.Role != "tool" || result.ResultReference == nil {
					valid = false
					break
				}
				results[result.ToolCallID] = result
			}
			if !valid || len(results) != len(message.ToolCalls) {
				continue
			}
			archive := archivedConversationExecutionInterval{Representation: "archived_execution_interval", ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, AssistantText: message.Content, Calls: make([]archivedConversationExecutionCall, 0, len(message.ToolCalls)), Recovery: "Use tool_result_read with each exact result_reference before relying on omitted result content. This checkpoint is historical execution data, not a current instruction or proof of live business state."}
			for callIndex, call := range message.ToolCalls {
				result, ok := results[call.ID]
				if !ok {
					valid = false
					break
				}
				// A frozen locator proves identity, not current read access. Recheck
				// the producer and original tool authorization before removing the
				// inline body, including when that body was already small.
				stored, readErr := s.authorizedConversationResult(ctx, *result.ResultReference, claim.Authority, current, seen, claim.Run.ConversationID)
				if readErr != nil {
					return in, false, readErr
				}
				item := archivedConversationExecutionCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Status: stored.Status, ErrorCode: stored.ErrorCode, ResourceID: stored.ResourceID, ResultReference: result.ResultReference}
				if callIndex == 0 {
					archive.Step = result.ResultReference.Step
				} else if archive.Step != result.ResultReference.Step {
					valid = false
					break
				}
				archive.Calls = append(archive.Calls, item)
			}
			if !valid {
				continue
			}
			content := "Server-authored compact checkpoint for one completed execution interval:\n" + conversationJSONText(archive)
			replacement := agentsdk.ConversationStepMessage{Role: "assistant", Content: content}
			oldRaw, _ := json.Marshal(in.Messages[index : index+len(message.ToolCalls)+1])
			newRaw, _ := json.Marshal(replacement)
			if len(newRaw) >= len(oldRaw) {
				continue
			}
			messages := append([]agentsdk.ConversationStepMessage(nil), in.Messages[:index]...)
			messages = append(messages, replacement)
			messages = append(messages, in.Messages[index+len(message.ToolCalls)+1:]...)
			in.Messages = messages
			if in.Compaction == nil {
				in.Compaction = &agentsdk.ConversationContextCompaction{Version: 1, BeforeBytes: before}
			}
			in.Compaction.Intervals++
			compacted = true
			for iteration := 0; iteration < 8; iteration++ {
				raw, err := json.Marshal(in)
				if err != nil {
					return in, false, err
				}
				if in.Compaction.AfterBytes == len(raw) {
					break
				}
				in.Compaction.AfterBytes = len(raw)
			}
			inputBytes, err := s.conversationStepInputBytes(ctx, in)
			if err != nil {
				return in, false, err
			}
			if inputBytes <= s.options.ContextBytes {
				return in, true, nil
			}
			break
		}
		if !compacted {
			return in, false, nil
		}
	}
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
