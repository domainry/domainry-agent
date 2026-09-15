package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type executionInputSizingModel struct {
	agentsdk.ConversationModel
	inputBytes int
	err        error
}

type executionMessageSizingModel struct{ agentsdk.ConversationModel }

func (*executionMessageSizingModel) ConversationStepInputBytes(in agentsdk.ConversationStepRequest) (int, error) {
	raw, err := json.Marshal(in.Messages)
	return len(raw), err
}

type executionIntervalRepository struct {
	persistence.ConversationRepository
	records map[string]persistence.ConversationToolExecution
}

func (r *executionIntervalRepository) ConversationResult(_ context.Context, ref agentsdk.ConversationResultReference, _ agentsdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	return r.records[ref.CallID], nil
}

type executionIntervalHost struct {
	definitions    []agentsdk.ConversationToolDefinition
	granted        bool
	authorizations int
}

func (h *executionIntervalHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return h.definitions, nil
}

func (h *executionIntervalHost) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	h.authorizations++
	return agentsdk.ConversationToolAuthorization{Granted: h.granted, Revision: "current-policy"}, nil
}

func (*executionIntervalHost) InvokeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}

func (*executionIntervalHost) ReconcileConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return agentsdk.ConversationToolResult{}, nil
}

func (m *executionInputSizingModel) ConversationStepInputBytes(agentsdk.ConversationStepRequest) (int, error) {
	return m.inputBytes, m.err
}

func TestExecutionInputSizerPreservesFrozenMetadataAndRetainsConservativeFallback(t *testing.T) {
	meter := &executionInputSizingModel{inputBytes: 512}
	s := &ConversationService{model: meter, options: ConversationOptions{ContextBytes: 4096}}
	in := agentsdk.ConversationStepRequest{Messages: []agentsdk.ConversationStepMessage{{Role: "user", Content: "Original request"}}, Tools: []agentsdk.ConversationToolDefinition{{Key: "clock", OutputSchema: json.RawMessage(`{"description":"` + strings.Repeat("unsent-policy", 6000) + `"}`)}}, ContextSources: []agentsdk.ConversationRunReference{{ConversationID: "original", RunID: "original-proof"}}}
	out, err := s.compactConversationExecution(t.Context(), persistence.ConversationClaim{}, in)
	if err != nil || !reflect.DeepEqual(in.Messages, out.Messages) || !reflect.DeepEqual(in.Tools, out.Tools) || !reflect.DeepEqual(in.ContextSources, out.ContextSources) || out.ContextWindow == nil || out.ContextWindow.InputBytes != 512 || !out.ContextWindow.ProviderSerialized {
		t.Fatal("provider budget stripped or rewrote frozen source/policy metadata", out.ContextWindow, err)
	}
	meter.inputBytes = s.options.ContextBytes + 1
	if _, err = s.compactConversationExecution(t.Context(), persistence.ConversationClaim{}, in); err == nil {
		t.Fatal("actual provider input exceeded unchanged context budget")
	}
	meter.inputBytes = 0
	if _, err = s.compactConversationExecution(t.Context(), persistence.ConversationClaim{}, in); err == nil {
		t.Fatal("invalid provider measurement disabled context checks")
	}
	meter.err = conversationFailure("conflict", "model_changed")
	if _, err = s.compactConversationExecution(t.Context(), persistence.ConversationClaim{}, in); err != meter.err {
		t.Fatal("input measurement error was lost", err)
	}
	s.model = nil
	if _, err = s.compactConversationExecution(t.Context(), persistence.ConversationClaim{}, in); err == nil {
		t.Fatal("model without measurement port bypassed conservative snapshot bound")
	}
}

func TestResultPreviewPreservesOutcomeAndBoundsEscapedUnicode(t *testing.T) {
	for _, status := range []string{"completed", "failed", "pending"} {
		content, _ := json.Marshal(map[string]string{"details": strings.Repeat("中文\n\"\\", 4000)})
		result := agentsdk.ConversationToolResult{Status: status, ResourceID: "resource-waiting-review", ErrorCode: "known-error", Content: content}
		ref := agentsdk.ConversationResultReference{ConversationID: "c", RunID: "r", CallID: "call", SHA256: conversationDigest(result)}
		for _, budget := range []int{1024, 2048, 8192} {
			text, err := previewConversationResult(result, ref, budget)
			if err != nil || len(text) > budget || !json.Valid([]byte(text)) {
				t.Fatalf("preview budget=%d bytes=%d err=%v", budget, len(text), err)
			}
			var preview conversationResultPreview
			if json.Unmarshal([]byte(text), &preview) != nil || preview.ContentComplete || preview.Status != status || preview.ErrorCode != result.ErrorCode || preview.ResourceID != result.ResourceID || preview.Reference != ref || !utf8.ValidString(preview.JSONPreview) || preview.PreviewBytes != len(preview.JSONPreview) {
				t.Fatal("preview changed an outcome or lost a resource")
			}
			full, _ := json.Marshal(result)
			if preview.TotalBytes != len(full) || !strings.HasPrefix(string(full), preview.JSONPreview) || preview.PreviewBytes == 0 {
				t.Fatal("excerpt is not a real prefix of the full stored result")
			}
		}
	}
}

func TestResultLocatorWithoutExcerptRetainsExactOutcomeAndReadReference(t *testing.T) {
	result := agentsdk.ConversationToolResult{Status: "failed", ResourceID: "pending-review", ErrorCode: "operation-failed", Content: json.RawMessage(`{"observed":"original result"}`)}
	ref := agentsdk.ConversationResultReference{ConversationID: "conversation", RunID: "run", Step: 7, CallID: "original-call", SHA256: conversationDigest(result)}
	text, err := previewConversationResult(result, ref, 0)
	var preview conversationResultPreview
	if err != nil || json.Unmarshal([]byte(text), &preview) != nil {
		t.Fatal("minimal locator is not valid result data", text, err)
	}
	full, _ := json.Marshal(result)
	if preview.Reference != ref || preview.Status != result.Status || preview.ResourceID != result.ResourceID || preview.ErrorCode != result.ErrorCode || preview.TotalBytes != len(full) || preview.JSONPreview != "" || preview.PreviewBytes != 0 || preview.ContentComplete || preview.Representation != "stored_result_preview" {
		t.Fatal("minimal locator changed the observed outcome or implied complete content", preview)
	}
}

func TestContextCompactionPersistsOnlyANetSmallerFrozenSnapshot(t *testing.T) {
	definition := agentsdk.ConversationToolDefinition{
		Key: "record_read", Version: "1", Description: "Read one record", ActionKey: "records.read", Effect: "read", Idempotency: "natural",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), TimeoutMillis: 1000, MaxOutputBytes: 65536,
	}
	call := agentsdk.ConversationToolCall{ID: "record", Name: definition.Key, Arguments: `{}`}
	result := agentsdk.ConversationToolResult{Status: "completed", ResourceID: "record", Content: json.RawMessage(`{"body":"` + strings.Repeat("x", 8000) + `"}`)}
	ref := &agentsdk.ConversationResultReference{ConversationID: "conversation", RunID: "run", Step: 0, CallID: call.ID, SHA256: conversationDigest(result)}
	raw, err := json.Marshal(struct {
		agentsdk.ConversationToolResult
		Reference *agentsdk.ConversationResultReference `json:"reference"`
	}{result, ref})
	if err != nil {
		t.Fatal(err)
	}
	input := agentsdk.ConversationStepRequest{
		Messages: []agentsdk.ConversationStepMessage{
			{Role: "user", Content: "read the current record"},
			{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call}},
			{Role: "tool", ToolCallID: call.ID, Content: string(raw), ResultReference: ref},
		},
		Tools:          []agentsdk.ConversationToolDefinition{definition, agentsdk.ConversationToolResultReadDefinition()},
		ModelIdentity:  agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "test", Model: "test", Fingerprint: "model-v1"},
		IdempotencyKey: "net-smaller", MaxOutputBytes: 4096, MaxArgumentBytes: 4096, MaxToolCalls: 8,
	}
	repo := &executionIntervalRepository{records: map[string]persistence.ConversationToolExecution{
		call.ID: {Step: ref.Step, State: "completed", Definition: definition, Call: call, Result: &result},
	}}
	host := &executionIntervalHost{definitions: input.Tools, granted: true}
	s := &ConversationService{repo: repo, model: &executionMessageSizingModel{}, options: ConversationOptions{ContextBytes: 64 * 1024, ToolHost: host}}
	claim := persistence.ConversationClaim{Authority: agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, Run: agentsdk.ConversationRun{ID: "run", ConversationID: "conversation"}}
	compacted, err := s.compactConversationExecution(t.Context(), claim, input)
	if err != nil || compacted.Compaction == nil || compacted.Compaction.Results != 1 || compacted.Compaction.AfterBytes >= compacted.Compaction.BeforeBytes || compacted.ContextWindow == nil || compacted.ContextWindow.InputBytes > compacted.ContextWindow.LimitBytes {
		t.Fatal("compaction persisted a non-shrinking or invalid frozen snapshot", compacted.Compaction, compacted.ContextWindow, err)
	}
	var preview conversationResultPreview
	if json.Unmarshal([]byte(compacted.Messages[2].Content), &preview) != nil || preview.Reference != *ref || preview.Status != result.Status || preview.PreviewBytes >= 2048 {
		t.Fatal("net-shrinking retry lost the result locator", compacted.Messages[2].Content)
	}
}

func TestCompletedExecutionIntervalCompactionKeepsCorrectionsAndRequiresCurrentResultAccess(t *testing.T) {
	definition := agentsdk.ConversationToolDefinition{
		Key: "record_read", Version: "1", Description: "Read one record", ActionKey: "records.read", Effect: "read", Idempotency: "natural",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), TimeoutMillis: 1000, MaxOutputBytes: 65536,
	}
	oldCall := agentsdk.ConversationToolCall{ID: "old", Name: definition.Key, Arguments: `{"quarter":"Q3"}`}
	latestCall := agentsdk.ConversationToolCall{ID: "latest", Name: definition.Key, Arguments: `{"quarter":"Q2"}`}
	oldResult := agentsdk.ConversationToolResult{Status: "completed", ResourceID: "old-record", Content: json.RawMessage(`{"rows":"` + strings.Repeat("old-result-body", 900) + `"}`)}
	latestResult := agentsdk.ConversationToolResult{Status: "completed", ResourceID: "current-record", Content: json.RawMessage(`{"rows":"` + strings.Repeat("latest", 100) + `"}`)}
	oldRef := &agentsdk.ConversationResultReference{ConversationID: "conversation", RunID: "run", Step: 0, CallID: oldCall.ID, SHA256: conversationDigest(oldResult)}
	latestRef := &agentsdk.ConversationResultReference{ConversationID: "conversation", RunID: "run", Step: 1, CallID: latestCall.ID, SHA256: conversationDigest(latestResult)}
	resultMessage := func(call agentsdk.ConversationToolCall, result agentsdk.ConversationToolResult, ref *agentsdk.ConversationResultReference) agentsdk.ConversationStepMessage {
		raw, err := json.Marshal(struct {
			agentsdk.ConversationToolResult
			Reference *agentsdk.ConversationResultReference `json:"reference"`
		}{result, ref})
		if err != nil {
			t.Fatal(err)
		}
		return agentsdk.ConversationStepMessage{Role: "tool", ToolCallID: call.ID, Content: string(raw), ResultReference: ref}
	}
	latestMessage := resultMessage(latestCall, latestResult, latestRef)
	input := agentsdk.ConversationStepRequest{
		Messages: []agentsdk.ConversationStepMessage{
			{Role: "system", Content: "stable current task agreement"},
			{Role: "user", Content: "initial request"},
			{Role: "assistant", Content: "old assistant conclusion", ToolCalls: []agentsdk.ConversationToolCall{oldCall}, ProviderState: json.RawMessage(`{"reasoning_content":"` + strings.Repeat("opaque-old", 1200) + `"}`)},
			resultMessage(oldCall, oldResult, oldRef),
			{Role: "user", Content: "correction: use Q2, not Q3"},
			{Role: "assistant", Content: "current tool request", ToolCalls: []agentsdk.ConversationToolCall{latestCall}, ProviderState: json.RawMessage(`{"reasoning_content":"current-state"}`)},
			latestMessage,
		},
		Tools:            []agentsdk.ConversationToolDefinition{definition, agentsdk.ConversationToolResultReadDefinition()},
		ModelIdentity:    agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "test", Model: "test", Fingerprint: "model-v1"},
		IdempotencyKey:   "step-2",
		MaxOutputBytes:   4096,
		MaxArgumentBytes: 4096,
		MaxToolCalls:     8,
	}
	repo := &executionIntervalRepository{records: map[string]persistence.ConversationToolExecution{
		oldCall.ID: {Step: oldRef.Step, State: "completed", Definition: definition, Call: oldCall, Result: &oldResult},
	}}
	host := &executionIntervalHost{definitions: input.Tools, granted: true}
	meter := &executionMessageSizingModel{}
	initialBytes, _ := meter.ConversationStepInputBytes(input)
	s := &ConversationService{repo: repo, model: meter, options: ConversationOptions{ContextBytes: initialBytes - 5000, ToolHost: host}}
	raw, _ := json.Marshal(input)
	claim := persistence.ConversationClaim{Authority: agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, Run: agentsdk.ConversationRun{ID: "run", ConversationID: "conversation"}}
	compacted, ok, err := s.compactConversationExecutionIntervals(t.Context(), claim, input, len(raw))
	if err != nil || !ok || compacted.Compaction == nil || compacted.Compaction.Intervals != 1 || compacted.Compaction.AfterBytes >= compacted.Compaction.BeforeBytes {
		t.Fatal("old completed interval did not compact into a recoverable checkpoint", compacted.Compaction, err)
	}
	encoded, _ := json.Marshal(compacted.Messages)
	if strings.Contains(string(encoded), "opaque-old") || !strings.Contains(string(encoded), "old assistant conclusion") || !strings.Contains(string(encoded), "correction: use Q2, not Q3") {
		t.Fatal("interval compaction lost required history or retained opaque state")
	}
	var archive archivedConversationExecutionInterval
	for _, message := range compacted.Messages {
		if strings.HasPrefix(message.Content, "Server-authored compact checkpoint for one completed execution interval:\n") {
			_ = json.Unmarshal([]byte(strings.TrimPrefix(message.Content, "Server-authored compact checkpoint for one completed execution interval:\n")), &archive)
		}
	}
	if archive.Representation != "archived_execution_interval" || archive.AssistantText != "old assistant conclusion" || len(archive.Calls) != 1 || archive.Calls[0].Arguments != oldCall.Arguments || archive.Calls[0].ResultReference == nil || *archive.Calls[0].ResultReference != *oldRef {
		t.Fatal("recoverable archive changed the original call", archive)
	}
	var savedLatest *agentsdk.ConversationStepMessage
	for index := range compacted.Messages {
		if compacted.Messages[index].ToolCallID == latestCall.ID {
			savedLatest = &compacted.Messages[index]
		}
	}
	if savedLatest == nil || !reflect.DeepEqual(*savedLatest, latestMessage) || host.authorizations != 1 {
		t.Fatal("latest result changed or archived result was not reauthorized", savedLatest, host.authorizations)
	}

	host.granted = false
	if _, _, err = s.compactConversationExecutionIntervals(t.Context(), claim, input, len(raw)); err == nil || !strings.Contains(err.Error(), "tool_access_denied") {
		t.Fatal("revoked result access did not block interval compaction", err)
	}
}
