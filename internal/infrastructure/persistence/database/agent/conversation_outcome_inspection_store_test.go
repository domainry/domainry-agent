package agent

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"testing"
	"time"
)

func peerUnknownWrite(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationDelegation, persistence.ConversationClaim) {
	t.Helper()
	repo, a, d := peerFixture(t)
	if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "writer", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	input := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "write", Name: "create_thing", Arguments: `{"name":"approved"}`}}}}); err != nil {
		t.Fatal(err)
	}
	approval, err := repo.WaitExecution(t.Context(), claim, persistence.ConversationWait{Step: 0, CallID: "write", Kind: "confirmation", Question: "Create one?"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RespondExecution(t.Context(), d.ConversationID, claim.Run.ID, sdk.ConversationInteractionResponse{InteractionID: approval.ID, ClientID: "approve", ExpectedRevision: approval.Revision, Decision: "approve"}, a); err != nil {
		t.Fatal(err)
	}
	claim, ok, err = repo.Claim(t.Context(), a.RuntimeID, "approved-writer", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "write", sdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "write", sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	brief := d.Brief
	brief.Version++
	brief.Goal = "Corrected task"
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "correct", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Correction", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	return repo, a, d, claim
}
func TestPeerOutcomeInspectionSettlesStaleRunWithoutExecutingIt(t *testing.T) {
	repo, a, d, old := peerUnknownWrite(t)
	if _, err := repo.Resume(t.Context(), d.ConversationID, old.Run.ID, a); err == nil {
		t.Fatal("stale agreement executed")
	}
	resume := sdk.ConversationDelegationUpdate{ClientID: "resume", ExpectedRevision: d.Revision, Action: "resume", Reason: "Continue"}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, resume, a); err == nil {
		t.Fatal("unknown effects resumed")
	}
	query := sdk.ConversationOutcomeInspectionRequest{RunID: old.Run.ID, Step: 0, CallID: "write"}
	inspection, err := repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "inspect", a)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Request.IdempotencyKey == "" || inspection.Request.Confirmation == nil || inspection.Run.Status != "cancelled" {
		t.Fatalf("missing frozen query %+v", inspection)
	}
	valid, err := repo.VerifyConversationOutcomeInspection(t.Context(), inspection.Request)
	if err != nil || !valid {
		t.Fatalf("original approval unavailable %v %v", valid, err)
	}
	tampered := inspection.Request
	tampered.Call.Arguments = `{"name":"different"}`
	if valid, _ = repo.VerifyConversationOutcomeInspection(t.Context(), tampered); valid {
		t.Fatal("inspection approved different operation")
	}
	if _, err = repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "competing", a); err == nil {
		t.Fatal("overlapping inspection")
	}
	if _, found, err := repo.Claim(t.Context(), a.RuntimeID, "forbidden-worker", time.Minute); err != nil || found {
		t.Fatal("inspection queued execution", err)
	}
	receipt := sdk.ConversationToolResult{Status: "completed", ResourceID: "existing-record", Content: []byte(`{"id":"existing-record"}`)}
	if err = repo.FinishConversationOutcomeInspection(t.Context(), inspection, receipt, a); err != nil {
		t.Fatal(err)
	}
	var persisted persistence.ConversationToolExecution
	if found, err := repo.readExecutionTool(t.Context(), repo.store.Database(), old, 0, "write", &persisted); err != nil || !found || conversationHash(persisted.Result) != conversationHash(receipt) {
		t.Fatalf("inspection restored fields from an earlier unknown receipt: %+v %v", persisted.Result, err)
	}
	run, err := repo.Run(t.Context(), d.ConversationID, old.Run.ID, a)
	if err != nil || run.Status != "cancelled" || run.Steps[0].Calls[0].Status != "completed" || run.Steps[0].Calls[0].OutcomeInspection == nil || run.Steps[0].Calls[0].OutcomeInspection.Status != "completed" {
		t.Fatalf("receipt projection %+v %v", run, err)
	}
	if currentPeer(t, repo, a, d.ID).Status != "needs_update" {
		t.Fatal("inspection resumed delegation")
	}
	if err = repo.FinishExecutionTool(t.Context(), old, 0, "write", sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "late"}); err == nil {
		t.Fatal("late uncertainty overwrote known effect")
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, resume, a); err != nil {
		t.Fatal("known effect did not unblock explicit new-version resume", err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !found || launch.Run.ID == old.Run.ID || launch.Run.BackgroundTask.AgreementRevision != 2 {
		t.Fatalf("wrong resumed work %+v %v", launch, err)
	}
}
func TestPeerOutcomeInspectionUnknownAndLateReceiptRace(t *testing.T) {
	repo, a, d, old := peerUnknownWrite(t)
	query := sdk.ConversationOutcomeInspectionRequest{RunID: old.Run.ID, Step: 0, CallID: "write"}
	inspection, err := repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "inspect", a)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishConversationOutcomeInspection(t.Context(), inspection, sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "unknown"}, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "not-yet", ExpectedRevision: d.Revision, Action: "resume", Reason: "Try"}, a); err == nil {
		t.Fatal("unknown receipt enabled work")
	}
	inspection, err = repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "inspect-again", a)
	if err != nil {
		t.Fatal(err)
	}
	receipt := sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"id":"late-original"}`), ResourceID: "late-original"}
	if err = repo.FinishExecutionTool(t.Context(), old, 0, "write", receipt); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishConversationOutcomeInspection(t.Context(), inspection, sdk.ConversationToolResult{Status: "uncertain", ErrorCode: "query-raced"}, a); err != nil {
		t.Fatal(err)
	}
	run, err := repo.Run(t.Context(), d.ConversationID, old.Run.ID, a)
	if err != nil || run.Steps[0].Calls[0].ResourceID != "late-original" || run.Steps[0].Calls[0].Status != "completed" {
		t.Fatalf("original receipt lost %+v %v", run, err)
	}
	other := a
	other.UserID = "other"
	if _, err = repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "cross-owner", other); err == nil {
		t.Fatal("cross-owner outcome read")
	}
}
