package agent

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func subjectTransferAdmission(t *testing.T, repo *ConversationStore, issuer, executor sdk.ConversationAuthority, d sdk.ConversationDelegation, sharedUsers ...string) persistence.ConversationDelegationTransferAdmission {
	t.Helper()
	mode, users := "owner", []string{issuer.UserID}
	users = append(users, sharedUsers...)
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "replacement", Name: "Replacement", Instructions: "Complete remaining work", ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}, executor)
	if err != nil {
		t.Fatal(err)
	}
	request := sdk.ConversationDelegationUpdate{ClientID: "transfer", ExpectedRevision: d.Revision, Action: "transfer", Reason: "Independent peer takes remaining work", Transfer: &sdk.ConversationDelegationTransfer{AgentID: agent.ID, RemainingWork: "Verify the original effect and finish remaining work"}}
	handoff, err := repo.PrepareConversationDelegationHandoff(t.Context(), d.ID, d.Revision, request.Transfer.RemainingWork, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return persistence.ConversationDelegationTransferAdmission{ExecutionAuthority: &executor, Request: request, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision, OwnerUserID: executor.UserID, DelegationRoleKey: executor.RoleKey, ExecutionSubject: &sdk.ConversationExecutionSubject{RuntimeID: executor.RuntimeID, WorkspaceID: executor.WorkspaceID, UserID: executor.UserID}}, Task: sdk.ConversationTask{SourceConversationID: d.SourceConversationID, SourceRunID: d.SourceRunID, Budget: d.Budget}, Handoff: handoff}
}

func TestCrossSubjectTransferRequiresExactPublicationAndKeepsOriginalEffects(t *testing.T) {
	repo, issuer, original, d, oldClaim := completedSubjectAssignment(t)
	replacement := issuer
	replacement.UserID, replacement.RoleKey = "replacement", "replacement-role"
	participants := []sdk.ConversationDelegationParticipantInput{{UserID: original.UserID, Operations: []string{"view", "execution_read"}}, {UserID: replacement.UserID, Operations: []string{"view", "execution_read"}}}
	var err error
	d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "readers", ExpectedRevision: d.Revision, Action: "set_participants", Reason: "Explicitly admit original and replacement readers", Participants: &participants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	in := subjectTransferAdmission(t, repo, issuer, replacement, d)
	if _, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, issuer); err == nil || !strings.Contains(err.Error(), "delegation_handoff_source_unavailable") {
		t.Fatal("private original execution was transferred", err)
	}
	if current := currentPeer(t, repo, issuer, d.ID); current.Revision != d.Revision || current.TaskID != d.TaskID {
		t.Fatal("denied transfer changed original work", current)
	}
	ref := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: oldClaim.Run.ID}
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, sdk.ConversationExecutionShare{ClientID: "publish-original", ExpectedRevision: d.Revision, Reference: ref, Reason: "Original executor explicitly shares its admitted run"}, original); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	in.Request.ExpectedRevision = d.Revision
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, issuer)
	if err != nil || next.ID != d.ID || next.ExecutionSubject == nil || next.ExecutionSubject.UserID != replacement.UserID || next.ConversationID == d.ConversationID || next.AssignmentNumber != 2 {
		t.Fatal("cross-subject admission failed", next, err)
	}
	repo = NewConversationStore(repo.store)
	authorities, err := repo.ConversationDelegationAuthorities(t.Context(), d.ID, issuer)
	if err != nil || authorities.Issuer != issuer || authorities.Executor != replacement {
		t.Fatal("binding did not persist exact issuer and new executor", authorities, err)
	}
	if _, err := repo.Get(t.Context(), next.ConversationID, issuer); err == nil {
		t.Fatal("issuer acquired replacement private conversation")
	}
	if _, err := repo.Run(t.Context(), d.ConversationID, oldClaim.Run.ID, replacement); err == nil {
		t.Fatal("replacement acquired original private endpoint")
	}
	if old, err := repo.Run(t.Context(), d.ConversationID, oldClaim.Run.ID, original); err != nil || old.Status != "completed" {
		t.Fatal("original execution was moved or erased", old, err)
	}
	if again, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, issuer); err != nil || again.TaskID != next.TaskID {
		t.Fatal("transfer replay created another task", again, err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
	if err != nil || !ok || launch.Authority != replacement || launch.Task.Budget.MaxSteps != d.Budget.MaxSteps-2 || launch.Task.Budget.MaxToolCalls != d.Budget.MaxToolCalls-1 {
		t.Fatal("original cumulative budget or receiving role lost", launch, err)
	}
	claim, ok, err := repo.Claim(t.Context(), issuer.RuntimeID, "replacement-worker", time.Minute)
	if err != nil || !ok || claim.Authority != replacement {
		t.Fatal("replacement claim", claim, err)
	}
	input := executionStoreInput()
	call := sdk.ConversationToolCall{ID: "reuse-original", Name: "create_thing", Arguments: `{"name":"approved"}`}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	reused, found, err := repo.ReuseConversationDelegationEffect(t.Context(), claim, 0, call, input.Tools[0])
	if err != nil || !found || reused.ResourceID != "original-record" {
		t.Fatal("original cross-subject effect was repeated or lost", reused, found, err)
	}
	assignments, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, issuer)
	if err != nil || len(assignments) != 2 {
		t.Fatal(assignments, err)
	}
	for i, want := range []sdk.ConversationAuthority{original, replacement} {
		got, err := repo.assignmentExecutionAuthority(t.Context(), repo.store.Database(), next, assignments[i], issuer)
		if err != nil || got != want {
			t.Fatal("assignment proof changed", i, got, want, err)
		}
	}
}

func TestCrossSubjectTransferRejectsChangedOrCallerChosenExecutionIdentity(t *testing.T) {
	repo, issuer, _, d, _ := completedSubjectAssignment(t)
	replacement := issuer
	replacement.UserID, replacement.RoleKey = "replacement", "replacement-role"
	in := subjectTransferAdmission(t, repo, issuer, replacement, d)
	for _, field := range []string{"user", "role", "workspace", "unbound"} {
		t.Run(field, func(t *testing.T) {
			bad := in
			a := replacement
			switch field {
			case "user":
				a.UserID = "arbitrary-user"
			case "role":
				a.RoleKey = "arbitrary-role"
			case "workspace":
				a.WorkspaceID = "other-workspace"
			case "unbound":
				bad.ExecutionAuthority = nil
			}
			if field != "unbound" {
				bad.ExecutionAuthority = &a
			}
			if _, err := repo.TransferConversationDelegation(t.Context(), d.ID, bad, issuer); err == nil || !strings.Contains(err.Error(), "execution_subject_mismatch") {
				t.Fatal("caller chose receiving identity", err)
			}
		})
	}
}

func TestParticipantTransferKeepsIssuerLedgerAndRechecksGrantBeforeReplay(t *testing.T) {
	exerciseParticipantTransferStore(t, false)
}

func TestParticipantTransferSeparatesManagerAndReplacementIdentity(t *testing.T) {
	exerciseParticipantTransferStore(t, true)
}

func TestParticipantDependencyTransferStoreValidatesManagerAndReceivingAudience(t *testing.T) {
	exerciseParticipantTransferStore(t, true, true)
}

func exerciseParticipantTransferStore(t *testing.T, independent bool, dependencyMode ...bool) {
	dependent := len(dependencyMode) > 0 && dependencyMode[0]
	repo, issuer, original, d, claim := completedSubjectAssignment(t)
	manager := issuer
	manager.UserID, manager.RoleKey = "replacement", "actual-manager-role"
	replacement := manager
	sharedUsers := []string{}
	if independent {
		replacement.UserID, replacement.RoleKey = "independent-replacement", "actual-execution-role"
		sharedUsers = append(sharedUsers, manager.UserID)
	}
	in := subjectTransferAdmission(t, repo, issuer, replacement, d, sharedUsers...)
	participants := []sdk.ConversationDelegationParticipantInput{{UserID: original.UserID, Operations: []string{"view", "execution_read"}}, {UserID: manager.UserID, Operations: []string{"view", "manage", "execution_read"}}}
	if independent {
		participants = append(participants, sdk.ConversationDelegationParticipantInput{UserID: replacement.UserID, Operations: []string{"view", "execution_read"}})
	}
	d, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "grant-transfer-manager", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &participants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var upstream sdk.ConversationDelegation
	if dependent {
		task, err := repo.ConversationTask(t.Context(), d.TaskID, original)
		if err != nil || task.Agent == nil {
			t.Fatal("original task snapshot", task, err)
		}
		upstream, err = repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{ExecutionAuthority: &original, Request: sdk.ConversationDelegationCreate{ClientID: "dependency-upstream", ConversationID: d.SourceConversationID, AgentID: d.ToAgentID, Purpose: "Original scope", Brief: d.Brief}, FromAgentID: d.FromAgentID, SourceAgent: sdk.ConversationAgentSnapshot{ID: d.FromAgentID}, Agent: *task.Agent, Task: sdk.ConversationTask{Goal: d.Brief.Goal, SourceConversationID: d.SourceConversationID, Budget: d.Budget}}, issuer)
		if err != nil {
			t.Fatal(err)
		}
		upstream, err = repo.UpdateConversationDelegation(t.Context(), upstream.ID, sdk.ConversationDelegationUpdate{ClientID: "pause-upstream", ExpectedRevision: upstream.Revision, Action: "pause"}, issuer)
		if err != nil {
			t.Fatal(err)
		}
		refs := []sdk.ConversationDependencyInput{{DelegationID: upstream.ID, BriefVersion: 1, AgreementRevision: 1, Fields: []string{"goal"}}}
		update := sdk.ConversationDelegationUpdate{ClientID: "manager-dependencies", ExpectedRevision: d.Revision, Action: "set_dependencies", Dependencies: &refs}
		if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, update, manager); err == nil {
			t.Fatal("downstream management granted upstream view")
		}
		if unchanged := currentPeer(t, repo, issuer, d.ID); unchanged.Revision != d.Revision || len(unchanged.Dependencies) != 0 {
			t.Fatal("denied dependency mutation changed work", unchanged)
		}
		upstream = dependencyViewers(t, repo, issuer, upstream, manager.UserID)
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, update, manager)
		if err != nil || len(d.Dependencies) != 1 || d.OwnerUserID != issuer.UserID {
			t.Fatal("authorized manager could not freeze exact upstream agreement", d, err)
		}
	}
	ref := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: claim.Run.ID}
	_, err = repo.PublishConversationDelegationExecution(t.Context(), d.ID, sdk.ConversationExecutionShare{ClientID: "publish-managed-original", ExpectedRevision: d.Revision, Reference: ref, Reason: "Explicit original executor publication"}, original)
	if err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	in.Request.ExpectedRevision = d.Revision
	if dependent {
		if _, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, manager); err == nil {
			t.Fatal("manager view lent upstream audience to receiving identity")
		}
		if unchanged := currentPeer(t, repo, issuer, d.ID); unchanged.Revision != d.Revision || unchanged.TaskID != d.TaskID {
			t.Fatal("missing recipient upstream audience admitted a task", unchanged)
		}
		dependencyViewers(t, repo, issuer, upstream, manager.UserID, replacement.UserID)
	}
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, manager)
	if err != nil || next.OwnerUserID != issuer.UserID || next.ExecutionSubject == nil || next.ExecutionSubject.UserID != replacement.UserID {
		t.Fatal("management changed owner or execution authority", next, err)
	}
	repo = NewConversationStore(repo.store)
	history, err := repo.ConversationDelegationAssignments(t.Context(), d.ID, issuer)
	if err != nil || len(history) != 2 || history[1].ActorID != manager.UserID {
		t.Fatal("actual manager or original issuer history lost", history, err)
	}
	if _, err := repo.Get(t.Context(), d.SourceConversationID, manager); err == nil {
		t.Fatal("transfer lent issuer private source ownership")
	}
	again, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, manager)
	if err != nil || again.TaskID != next.TaskID {
		t.Fatal("managed retry admitted another task", again, err)
	}
	if _, found, err := repo.ConversationDelegationTransferReceipt(t.Context(), d.ID, in.Request, manager); err != nil || !found {
		t.Fatal("managed receipt missing", found, err)
	}
	participants[1].Operations = []string{"view", "execution_read"}
	_, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "withdraw-transfer-manager", ExpectedRevision: next.Revision, Action: "set_participants", Participants: &participants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, manager); err == nil {
		t.Fatal("withdrawn managed grant still replays mutation")
	}
	if _, _, err := repo.ConversationDelegationTransferReceipt(t.Context(), d.ID, in.Request, manager); err == nil {
		t.Fatal("withdrawn managed grant still reads transfer receipt")
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
	if err != nil || !found || launch.Authority != replacement {
		t.Fatal("extra grant withdrawal removed independent execution responsibility", launch, found, err)
	}
}
