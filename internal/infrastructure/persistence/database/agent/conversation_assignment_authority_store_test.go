package agent

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func completedSubjectAssignment(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, persistence.ConversationClaim) {
	t.Helper()
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID); err != nil || !ok {
		t.Fatal("launch", ok, err)
	}
	claim, ok, err := repo.Claim(t.Context(), issuer.RuntimeID, "assignment-worker", time.Minute)
	if err != nil || !ok || claim.Authority != executor {
		t.Fatal("claim", ok, err)
	}
	input := executionStoreInput()
	call := sdk.ConversationToolCall{ID: "original-write", Name: "create_thing", Arguments: `{"name":"approved"}`}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	result := sdk.ConversationToolResult{Status: "completed", ResourceID: "original-record", Content: []byte(`{"id":"original-record"}`)}
	if err := repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, result); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 1, &input); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 1, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	return repo, issuer, executor, d, claim
}

func TestAssignmentAuthorityKeepsCrossSubjectOriginalLedgerAndPrivateDTO(t *testing.T) {
	repo, issuer, executor, d, claim := completedSubjectAssignment(t)
	repo = NewConversationStore(repo.store)
	reader := issuer
	reader.RoleKey = "later-reading-role"
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, reader)
	if err != nil || len(assignments) != 1 {
		t.Fatal(assignments, err)
	}
	original, err := repo.assignmentExecutionAuthority(t.Context(), repo.store.Database(), d, assignments[0], reader)
	if err != nil || original != executor {
		t.Fatal("reading role replaced original task proof", original, err)
	}
	raw, _ := json.Marshal(assignments)
	if strings.Contains(string(raw), "execution_authority") || strings.Contains(string(raw), executor.RoleKey) {
		t.Fatal("internal routing leaked through public history", string(raw))
	}
	handoff, err := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, "Complete remaining work", reader)
	if err != nil || len(handoff.Runs) != 1 || handoff.Runs[0].RunID != claim.Run.ID || len(handoff.Effects) != 1 || handoff.Effects[0].ResourceID != "original-record" {
		t.Fatal("lost original executor's actual effects", handoff, err)
	}
	if _, err := repo.Run(t.Context(), d.ConversationID, claim.Run.ID, reader); err == nil {
		t.Fatal("routing metadata granted private execution access")
	}
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		remaining, err := repo.conversationDelegationRemainingBudget(t.Context(), tx, d, reader)
		if err == nil && (remaining.MaxSteps != d.Budget.MaxSteps-2 || remaining.MaxToolCalls != d.Budget.MaxToolCalls-1) {
			t.Errorf("actual prior work missing from budget: %+v", remaining)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentAuthorityEnrichesLegacyProofWithoutChangingHistory(t *testing.T) {
	repo, a, d, old := peerUnknownWrite(t)
	inspection, err := repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, sdk.ConversationOutcomeInspectionRequest{RunID: old.Run.ID, Step: 0, CallID: "write"}, "settle", a)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishConversationOutcomeInspection(t.Context(), inspection, sdk.ConversationToolResult{Status: "completed", ResourceID: "original-record", Content: []byte(`{"id":"original-record"}`)}, a); err != nil {
		t.Fatal(err)
	}
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, a)
	if err != nil || len(assignments) != 1 {
		t.Fatal(assignments, err)
	}
	before := conversationHash(assignments[0])
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAssignmentTable).Set("payload_json", conversationJSON(assignments[0])).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("delegation_id", d.ID), query.Equal("number", 1))).Build()
		return conversationCAS(t.Context(), tx, q, args, err)
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := a
	reader.RoleKey = "replacement-role"
	admission := transferAdmission(t, repo, reader, d, "replacement-with-role")
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, admission, reader)
	if err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(repo.store)
	assignments, err = repo.ConversationDelegationAssignments(t.Context(), d.ID, reader)
	if err != nil || len(assignments) != 2 || conversationHash(assignments[0]) != before {
		t.Fatal("enrichment changed original public facts", assignments, err)
	}
	for i, want := range []sdk.ConversationAuthority{a, reader} {
		got, err := repo.assignmentExecutionAuthority(t.Context(), repo.store.Database(), next, assignments[i], reader)
		if err != nil || got != want {
			t.Fatal("assignment's actual original role lost", i, got, want, err)
		}
	}
	if err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.insertConversationAssignment(t.Context(), tx, d.ID, assignments[0], reader)
	}); err != nil {
		t.Fatal("later reader could not replay the same immutable facts", err)
	}
}

func TestAssignmentAuthorityRejectsForgedRoutingBeforeHandoff(t *testing.T) {
	repo, issuer, executor, d, _ := completedSubjectAssignment(t)
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "workspace", "task"} {
		t.Run(field, func(t *testing.T) {
			forged := executor
			assignment := assignments[0]
			switch field {
			case "role":
				forged.RoleKey = "unrecorded-role"
			case "workspace":
				forged.WorkspaceID = "other-workspace"
			case "task":
				assignment.TaskID = "unrelated-task"
			}
			record := conversationAssignmentRecord{ConversationDelegationAssignment: assignment, ExecutionAuthority: &forged}
			err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
				q, args, err := query.NewUpdateBuilder(repo.store.Renderer(), conversationAssignmentTable).Set("payload_json", conversationJSON(record)).Where(query.And(query.Equal("owner_key", conversationOwner(issuer)), query.Equal("delegation_id", d.ID), query.Equal("number", 1))).Build()
				return conversationCAS(t.Context(), tx, q, args, err)
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, "continue", issuer); err == nil {
				t.Fatal("forged assignment proof accepted")
			}
		})
	}
}
