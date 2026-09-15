package application

import (
	"context"
	"fmt"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type compactingReceiptRepository struct {
	privatePeerSources
	persistence.ConversationExecutionReadRepository
	a         sdk.ConversationAuthority
	snapshots []sdk.ConversationRunReference
	records   map[string]persistence.ConversationToolExecution
}

func (r *compactingReceiptRepository) ConversationSourceSnapshot(_ context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	if a != r.a {
		return persistence.ConversationSourceSnapshot{}, conversationFailure("forbidden", "source_read_forbidden")
	}
	r.snapshots = append(r.snapshots, ref)
	return persistence.ConversationSourceSnapshot{Authority: a, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}, nil
}

func (r *compactingReceiptRepository) ReadExecutionCall(_ context.Context, conversationID, runID string, step int, callID string, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	if a != r.a {
		return persistence.ConversationToolExecution{}, conversationFailure("forbidden", "execution_read_forbidden")
	}
	record, ok := r.records[fmt.Sprintf("%s\x00%s\x00%d\x00%s", conversationID, runID, step, callID)]
	if !ok {
		return persistence.ConversationToolExecution{}, conversationFailure("not_found", "execution_call_not_found")
	}
	return record, nil
}

func TestCompletionReceiptsAuditOnlyLargestPrefixPerRun(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
	repo := &compactingReceiptRepository{a: a, records: map[string]persistence.ConversationToolExecution{}}
	refs := []sdk.ConversationResultReference{}
	for _, item := range []struct {
		conversationID string
		runID          string
		step           int
		callID         string
	}{
		{"conversation-a", "run-a", 0, "first"},
		{"conversation-a", "run-a", 1, "second"},
		{"conversation-b", "run-b", 2, "foreign"},
		{"conversation-a", "run-a", 4, "last"},
	} {
		result := &sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"ok":true}`)}
		record := persistence.ConversationToolExecution{State: "completed", Step: item.step, Call: sdk.ConversationToolCall{ID: item.callID}, Result: result}
		repo.records[fmt.Sprintf("%s\x00%s\x00%d\x00%s", item.conversationID, item.runID, item.step, item.callID)] = record
		refs = append(refs, sdk.ConversationResultReference{ConversationID: item.conversationID, RunID: item.runID, Step: item.step, CallID: item.callID, SHA256: conversationDigest(result)})
	}
	s := &ConversationService{runtimeID: a.RuntimeID, repo: repo}
	if err := s.checkCompletionReceipts(t.Context(), refs, a, ""); err != nil {
		t.Fatal(err)
	}
	want := []sdk.ConversationRunReference{
		{ConversationID: "conversation-a", RunID: "run-a", BeforeStep: 6},
		{ConversationID: "conversation-b", RunID: "run-b", BeforeStep: 4},
	}
	if len(repo.snapshots) != len(want) {
		t.Fatalf("audited %d prefixes, want %d: %+v", len(repo.snapshots), len(want), repo.snapshots)
	}
	for index := range want {
		if repo.snapshots[index] != want[index] {
			t.Fatalf("prefix %d = %+v, want %+v", index, repo.snapshots[index], want[index])
		}
	}
	repo.snapshots = nil
	brief := sdk.ConversationTaskBrief{CompletionConditions: []string{"first", "second", "foreign", "last"}}
	entries := make([]sdk.ConversationConditionAssessment, len(refs))
	for index, ref := range refs {
		entries[index] = sdk.ConversationConditionAssessment{Condition: index, Verdict: "met", Basis: "verified", Receipts: []sdk.ConversationResultReference{ref}}
	}
	if err := s.checkCompletionAssessments(t.Context(), brief, entries, a, ""); err != nil {
		t.Fatal(err)
	}
	if len(repo.snapshots) != len(want) {
		t.Fatalf("assessment groups audited %d prefixes, want %d: %+v", len(repo.snapshots), len(want), repo.snapshots)
	}
}
