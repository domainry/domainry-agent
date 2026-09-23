package agent

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func transferAdmission(t *testing.T, repo *ConversationStore, a sdk.ConversationAuthority, d sdk.ConversationDelegation, clientID string) persistence.ConversationDelegationTransferAdmission {
	t.Helper()
	peer, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: clientID, Name: clientID, Instructions: "Continue remaining work", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	in := sdk.ConversationDelegationUpdate{ClientID: clientID, ExpectedRevision: d.Revision, Action: "transfer", Reason: "Reassign remaining review", Transfer: &sdk.ConversationDelegationTransfer{AgentID: peer.ID, RemainingWork: "Check the original receipt and complete the remaining review"}}
	handoff, err := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, in.Transfer.RemainingWork, a)
	if err != nil {
		t.Fatal(err)
	}
	return persistence.ConversationDelegationTransferAdmission{Request: in, Agent: sdk.ConversationAgentSnapshot{ID: peer.ID, Revision: peer.Revision}, Task: sdk.ConversationTask{SourceConversationID: d.SourceConversationID, SourceRunID: d.SourceRunID, Budget: d.Budget}, Handoff: handoff}
}

func TestPeerTransferPreservesHistoryBudgetAndReusesCompletedEffects(t *testing.T) {
	repo, a, d, old := peerUnknownWrite(t)
	query := sdk.ConversationOutcomeInspectionRequest{RunID: old.Run.ID, Step: 0, CallID: "write"}
	inspection, err := repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, query, "settle-before-transfer", a)
	if err != nil {
		t.Fatal(err)
	}
	result := sdk.ConversationToolResult{Status: "completed", ResourceID: "original-record", Content: []byte(`{"id":"original-record"}`)}
	if err = repo.FinishConversationOutcomeInspection(t.Context(), inspection, result, a); err != nil {
		t.Fatal(err)
	}
	in := transferAdmission(t, repo, a, d, "replacement")
	if len(in.Handoff.Effects) != 1 || in.Handoff.Effects[0].Reference.RunID != old.Run.ID {
		t.Fatalf("missing original receipt: %+v", in.Handoff)
	}
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, a)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != d.ID || next.ConversationID == d.ConversationID || next.TaskID == d.TaskID || next.ToAgentID == d.ToAgentID || next.AssignmentNumber != 2 || next.AgreementRevision != d.AgreementRevision+1 || next.Budget != d.Budget {
		t.Fatalf("identities or total budget lost: %+v", next)
	}
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, a)
	if err != nil || len(assignments) != 2 || assignments[0].ConversationID != d.ConversationID || assignments[1].ConversationID != next.ConversationID {
		t.Fatalf("history %+v %v", assignments, err)
	}
	if again, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, a); err != nil || again.ConversationID != next.ConversationID {
		t.Fatal("duplicate transfer", err)
	}
	if _, found, err := repo.ConversationDelegationTransferReceipt(t.Context(), d.ID, in.Request, a); err != nil || !found {
		t.Fatal("transfer receipt missing", err)
	}
	if _, err = repo.Resume(t.Context(), d.ConversationID, old.Run.ID, a); err == nil {
		t.Fatal("old assignment resumed")
	}
	if _, _, err = repo.ExecutionStep(t.Context(), old, 1, nil); err == nil {
		t.Fatal("old worker retained lease")
	}
	listed, err := repo.ConversationDelegations(t.Context(), d.ConversationID, a)
	if err != nil || len(listed) != 1 || listed[0].ID != d.ID {
		t.Fatal("old conversation lost relationship", err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !found || launch.Task.Budget.MaxSteps != d.Budget.MaxSteps-1 || launch.Task.Budget.MaxToolCalls != d.Budget.MaxToolCalls-1 || launch.Run.BackgroundTask.Handoff == nil {
		t.Fatalf("budget or handoff reset %+v %v", launch, err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "replacement-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	input := executionStoreInput()
	call := sdk.ConversationToolCall{ID: "repeat-old-write", Name: "create_thing", Arguments: `{ "name" : "approved" }`}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	reused, found, err := repo.ReuseConversationDelegationEffect(t.Context(), claim, 0, call, input.Tools[0])
	if err != nil || !found || conversationHash(reused) != conversationHash(result) {
		t.Fatalf("known effect not reused %+v %v", reused, err)
	}
	changedDefinition := input.Tools[0]
	changedDefinition.Description += " changed behavior"
	if _, _, err = repo.ReuseConversationDelegationEffect(t.Context(), claim, 0, call, changedDefinition); err == nil || !strings.Contains(err.Error(), "delegation_effect_changed") {
		t.Fatal("changed tool definition permitted receipt reuse or resubmission", err)
	}
	run, err := repo.Run(t.Context(), next.ConversationID, claim.Run.ID, a)
	if err != nil || run.Steps[0].Calls[0].ReusedFrom == nil || run.Metrics.ToolAttempts != 0 {
		t.Fatalf("reuse reported as new invocation %+v %v", run, err)
	}
	changed := call
	changed.Arguments = `{"name":"new-target"}`
	if _, found, err = repo.ReuseConversationDelegationEffect(t.Context(), claim, 0, changed, input.Tools[0]); err != nil || found {
		t.Fatal("different business operation reused", err)
	}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 1, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 1, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	next = currentPeer(t, repo, a, d.ID)
	thirdAdmission := transferAdmission(t, repo, a, next, "third-assignment")
	third, err := repo.TransferConversationDelegation(t.Context(), d.ID, thirdAdmission, a)
	if err != nil {
		t.Fatal(err)
	}
	launch, found, err = repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !found || launch.Task.Budget.MaxSteps != 3 || launch.Task.Budget.MaxToolCalls != 4 || len(launch.Task.Handoff.Effects) != 1 {
		t.Fatalf("cumulative work lost on repeated transfer %+v %v", launch, err)
	}
	if third.AssignmentNumber != 3 {
		t.Fatal("assignment chain lost")
	}
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		corrupt := assignments[0]
		corrupt.AgentID = "forged"
		return repo.insertConversationAssignment(t.Context(), tx, d.ID, corrupt, a)
	})
	if err == nil {
		t.Fatal("immutable assignment was overwritten")
	}
}

func TestPeerTransferRejectsUnknownEffectsAndConcurrentRequirementChange(t *testing.T) {
	repo, a, d, old := peerUnknownWrite(t)
	if _, err := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, "continue", a); err == nil || !strings.Contains(err.Error(), "delegation_reconciliation_required") {
		t.Fatal("unknown effects transferred", err)
	}
	inspection, err := repo.BeginConversationOutcomeInspection(t.Context(), d.ID, d.Revision, sdk.ConversationOutcomeInspectionRequest{RunID: old.Run.ID, Step: 0, CallID: "write"}, "settle", a)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishConversationOutcomeInspection(t.Context(), inspection, sdk.ConversationToolResult{Status: "failed", ErrorCode: "definitive-rejection"}, a); err != nil {
		t.Fatal(err)
	}
	in := transferAdmission(t, repo, a, d, "replacement")
	brief := d.Brief
	brief.Version++
	brief.Goal = "New scope"
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "new-scope", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Changed", Brief: &brief}, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.TransferConversationDelegation(t.Context(), d.ID, in, a); err == nil {
		t.Fatal("concurrent change lost")
	}
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, a)
	if err != nil || len(assignments) != 1 {
		t.Fatal("failed transfer left assignment", err)
	}
	other := a
	other.UserID = "other"
	if _, err = repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, "private", other); err == nil {
		t.Fatal("cross-owner handoff exposed")
	}
}

func TestPeerSameAgentRecoveryRequiresANewerImmutableSnapshot(t *testing.T) {
	repo, a, d := peerFixture(t)
	d, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "pause-for-recovery", ExpectedRevision: d.Revision, Action: "pause", Reason: "Configuration must be repaired"}, a)
	if err != nil {
		t.Fatal(err)
	}
	makeAdmission := func(clientID string, snapshot sdk.ConversationAgentSnapshot) persistence.ConversationDelegationTransferAdmission {
		request := sdk.ConversationDelegationUpdate{ClientID: clientID, ExpectedRevision: d.Revision, Action: "transfer", Reason: "Restart with the current configuration", Transfer: &sdk.ConversationDelegationTransfer{AgentID: d.ToAgentID, RemainingWork: "Continue the same review from the preserved handoff"}}
		handoff, handoffErr := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, request.Transfer.RemainingWork, a)
		if handoffErr != nil {
			t.Fatal(handoffErr)
		}
		return persistence.ConversationDelegationTransferAdmission{Request: request, Agent: snapshot, Task: sdk.ConversationTask{SourceConversationID: d.SourceConversationID, SourceRunID: d.SourceRunID, Budget: d.Budget}, Handoff: handoff}
	}
	_, err = repo.TransferConversationDelegation(t.Context(), d.ID, makeAdmission("same-snapshot", sdk.ConversationAgentSnapshot{ID: d.ToAgentID, Revision: 1}), a)
	requireConversationCode(t, err, "delegation_recovery_not_needed")

	agent, err := repo.ConversationAgent(t.Context(), d.ToAgentID, a)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = repo.WriteConversationAgent(t.Context(), agent.ID, sdk.ConversationAgentWrite{ClientID: "repair-agent", ExpectedRevision: agent.Revision, Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions + " with repaired configuration", Tools: agent.Tools, SkillKeys: agent.SkillKeys, ModelKey: agent.ModelKey, Enabled: true, MaxConcurrent: agent.MaxConcurrent}, a)
	if err != nil {
		t.Fatal(err)
	}
	admission := makeAdmission("recover-new-snapshot", sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision})
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, admission, a)
	if err != nil {
		t.Fatal(err)
	}
	if next.ToAgentID != d.ToAgentID || next.AssignmentNumber != 2 || next.ConversationID == d.ConversationID || next.TaskID == d.TaskID || next.WorkBudget == nil || conversationHash(*next.WorkBudget) != conversationHash(*d.WorkBudget) {
		t.Fatalf("same-agent recovery lost immutable history or shared budget: %+v", next)
	}
	again, err := repo.TransferConversationDelegation(t.Context(), d.ID, admission, a)
	if err != nil || again.ConversationID != next.ConversationID {
		t.Fatalf("same-agent recovery was not idempotent: %+v %v", again, err)
	}
}

func TestPeerTransferAgreementDecisionRetainsItsOwnSource(t *testing.T) {
	repo, a, d := peerFixture(t)
	d.AgreementRevision++
	d.BriefSource = &sdk.ConversationRunReference{ConversationID: "brief", RunID: "brief-run"}
	actor := &sdk.ConversationToolRequest{ConversationID: d.SourceConversationID, RunID: "decision-run", Step: 2}
	err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveAgreementRevision(t.Context(), tx, d, []string{"assignment"}, "Decision based on new evidence", actor, a)
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := repo.ConversationAgreementHistory(t.Context(), d.ID, 0, a)
	if err != nil || len(history.Items) != 2 {
		t.Fatal("history missing", err)
	}
	entry := history.Items[0]
	if entry.Source == nil || entry.Source.RunID != "brief-run" || entry.ChangeSource == nil || entry.ChangeSource.RunID != "decision-run" || entry.ChangeSource.BeforeStep != 3 || entry.FromAgentID != "default" {
		t.Fatalf("brief and decision provenance conflated: %+v", entry)
	}
}
