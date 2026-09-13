package application

import (
	"context"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

type outcomeProbe struct {
	sdk.ConversationToolHost
	writes int
}

func (h *outcomeProbe) InvokeConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	h.writes++
	return sdk.ConversationToolResult{}, nil
}
func (h *outcomeProbe) ReconcileConversationTool(context.Context, sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	h.writes++
	return sdk.ConversationToolResult{}, nil
}

type outcomeReaderProbe struct {
	*outcomeProbe
	reads int
}

func (h *outcomeReaderProbe) InspectConversationToolOutcome(_ context.Context, in sdk.ConversationToolRequest) (sdk.ConversationToolResult, error) {
	if in.OutcomeInspectionToken != "fenced-original" || in.IdempotencyKey != "original" {
		panic("frozen receipt query changed")
	}
	h.reads++
	return sdk.ConversationToolResult{Status: "completed", ResourceID: "existing"}, nil
}
func TestPeerOutcomeInspectionCompositionNeverFallsBackToInvocation(t *testing.T) {
	probe := &outcomeProbe{}
	in := sdk.ConversationToolRequest{Definition: sdk.ConversationToolDefinition{Key: "write"}, IdempotencyKey: "original", OutcomeInspectionToken: "fenced-original"}
	wrap := func(h sdk.ConversationToolHost) sdk.ConversationToolHost {
		return &profileToolHost{allowed: map[string]bool{"write": true}, base: &extensionInteractionHost{ConversationToolHost: &collaborationToolHost{ConversationToolHost: &assemblyConfirmationHost{ConversationToolHost: &knowledgeConversationHost{base: &attachmentKnowledgeHost{base: &businessConversationHost{base: h}}}}}}}
	}
	if _, err := inspectOutcome(t.Context(), wrap(probe), in); err == nil {
		t.Fatal("unsupported host reported an outcome")
	}
	if probe.writes != 0 {
		t.Fatal("receipt query used mutating reconciliation")
	}
	reader := &outcomeReaderProbe{outcomeProbe: probe}
	out, err := inspectOutcome(t.Context(), wrap(reader), in)
	if err != nil || out.ResourceID != "existing" || reader.reads != 1 || probe.writes != 0 {
		t.Fatalf("optional port lost through composition: %+v %v", out, err)
	}
	denied := &profileToolHost{allowed: map[string]bool{}, base: reader}
	if _, err = inspectOutcome(t.Context(), denied, in); err == nil || reader.reads != 1 {
		t.Fatal("frozen profile scope bypassed")
	}
}
