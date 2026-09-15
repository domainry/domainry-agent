package agent

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestParticipantManagementPreservesOriginalTaskAndRejectsWithdrawnGrantReplay(t *testing.T) {
	repo, owner, d := peerFixture(t)
	manager := owner
	manager.UserID, manager.RoleKey = "participant", "managing-role"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: manager.UserID, Operations: []string{"view", "manage"}}}
	invite := sdk.ConversationDelegationUpdate{ClientID: "grant-management", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}
	shared, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, invite, owner)
	if err != nil {
		t.Fatal(err)
	}
	pause := sdk.ConversationDelegationUpdate{ClientID: "participant-pause", ExpectedRevision: shared.Revision, Action: "pause", Reason: "Pause original worker"}
	paused, err := repo.UpdateConversationDelegation(t.Context(), d.ID, pause, manager)
	if err != nil || paused.Status != "paused" || paused.OwnerUserID != owner.UserID || paused.ConversationID != d.ConversationID || paused.TaskID != d.TaskID {
		t.Fatal("participant management changed task identity or failed", paused, err)
	}
	if task, err := repo.ConversationTask(t.Context(), d.TaskID, owner); err != nil || task.Status != "cancelled" {
		t.Fatal("management failed to stop the original task", task, err)
	}
	repo = NewConversationStore(repo.store)
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, pause, manager)
	if err != nil || replay.Revision != paused.Revision {
		t.Fatal("restart changed the original management receipt", replay, err)
	}
	resume := sdk.ConversationDelegationUpdate{ClientID: "participant-resume", ExpectedRevision: paused.Revision, Action: "resume", Reason: "Continue original worker"}
	resumed, err := repo.UpdateConversationDelegation(t.Context(), d.ID, resume, manager)
	if err != nil || resumed.Status != "accepted" {
		t.Fatal("participant could not resume the original worker", resumed, err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), owner.RuntimeID)
	if err != nil || !found || launch.Authority != owner || launch.Run.ConversationID != d.ConversationID {
		t.Fatal("manager became the execution subject", launch, err)
	}
	current, err := repo.ConversationDelegation(t.Context(), d.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	grants[0].Operations = []string{"view"}
	reduced, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "withdraw-management", ExpectedRevision: current.Revision, Action: "set_participants", Participants: &grants}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, pause, manager); err == nil {
		t.Fatal("withdrawn manager recovered an unrestricted raw old delegation")
	}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "cancel-withdrawn", ExpectedRevision: reduced.Revision, Action: "cancel"}, manager); err == nil {
		t.Fatal("withdrawn manager cancelled the original worker")
	}
	if _, err := repo.Get(t.Context(), d.ConversationID, manager); err == nil {
		t.Fatal("management scope opened a raw execution conversation")
	}
}

func TestParticipantRequirementChangeInvalidatesOwnersDependentWork(t *testing.T) {
	repo, owner, upstream := peerFixture(t)
	brief := upstream.Brief
	brief.Goal = "Dependent findings"
	agent, err := repo.ConversationAgent(t.Context(), upstream.ToAgentID, owner)
	if err != nil {
		t.Fatal(err)
	}
	child, err := repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{
		Request:     sdk.ConversationDelegationCreate{ClientID: "dependent-work", ConversationID: upstream.SourceConversationID, AgentID: agent.ID, Purpose: "Use upstream goal", Brief: brief, Budget: upstream.Budget, Dependencies: []sdk.ConversationDependencyInput{{DelegationID: upstream.ID, BriefVersion: 1, AgreementRevision: 1, Fields: []string{"goal"}}}},
		FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: agent.Revision}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: upstream.SourceConversationID, Budget: upstream.Budget},
	}, owner)
	if err != nil {
		t.Fatal(err)
	}
	manager := owner
	manager.UserID, manager.RoleKey = "participant", "managing-role"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: manager.UserID, Operations: []string{"view", "manage"}}}
	upstream, err = repo.SetConversationDelegationParticipants(t.Context(), upstream.ID, sdk.ConversationDelegationUpdate{ClientID: "grant-management", ExpectedRevision: upstream.Revision, Action: "set_participants", Participants: &grants}, owner)
	if err != nil {
		t.Fatal(err)
	}
	updatedBrief := upstream.Brief
	updatedBrief.Version++
	updatedBrief.Goal = "Changed upstream goal"
	if _, err := repo.UpdateConversationDelegation(t.Context(), upstream.ID, sdk.ConversationDelegationUpdate{ClientID: "change-upstream-goal", ExpectedRevision: upstream.Revision, Action: "update_brief", Brief: &updatedBrief, Reason: "New declared scope"}, manager); err != nil {
		t.Fatal(err)
	}
	child, err = repo.ConversationDelegation(t.Context(), child.ID, owner)
	if err != nil || child.Status != "needs_update" || len(child.PendingChanges) != 1 || child.PendingChanges[0].SourceDelegationID != upstream.ID {
		t.Fatal("participant requirement change silently skipped the owner's dependent work", child, err)
	}
	if task, err := repo.ConversationTask(t.Context(), child.TaskID, owner); err != nil || task.Status != "cancelled" {
		t.Fatal("affected owner's work did not stop", task, err)
	}
	if _, err := repo.ConversationDelegation(t.Context(), child.ID, manager); err == nil {
		t.Fatal("internal dependency invalidation gave the participant private child access")
	}
}

func TestParticipantManagementRoutesMappedAndUnmappedOriginalExecutionRoles(t *testing.T) {
	for _, mode := range []string{"different-user", "different-role", "same-identity"} {
		t.Run(mode, func(t *testing.T) {
			repo, source, executor, in := delegationSubjectFixture(t, mode != "different-user")
			if mode == "same-identity" {
				executor = source
				in.ExecutionAuthority = &executor
			}
			d, err := repo.CreateConversationDelegation(t.Context(), in, source)
			if err != nil {
				t.Fatal(err)
			}
			manager := source
			manager.UserID, manager.RoleKey = "participant", "actual-management-role"
			grants := []sdk.ConversationDelegationParticipantInput{{UserID: manager.UserID, Operations: []string{"view", "manage"}}}
			d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "management", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, source)
			if err != nil {
				t.Fatal(err)
			}
			authorities, err := repo.ConversationDelegationAuthorities(t.Context(), d.ID, manager)
			if err != nil || authorities.Issuer != source || authorities.Executor != executor {
				t.Fatal("participant storage routing replaced immutable admission roles", authorities, err)
			}
			if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "forged-delivery", ExpectedRevision: d.Revision, Action: "deliver", Delivery: &sdk.ConversationDelegationDelivery{}}, manager); err == nil {
				t.Fatal("management grant became delivery authority")
			}
			d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "pause", ExpectedRevision: d.Revision, Action: "pause", Reason: "Stop original executor"}, manager)
			if err != nil {
				t.Fatal(err)
			}
			d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "resume", ExpectedRevision: d.Revision, Action: "resume", Reason: "Resume original executor"}, manager)
			if err != nil {
				t.Fatal(err)
			}
			launch, found, err := repo.LaunchConversationTask(t.Context(), source.RuntimeID)
			if err != nil || !found || launch.Authority != executor || launch.Run.ConversationID != d.ConversationID {
				t.Fatal("participant resume changed execution authority", launch, err)
			}
		})
	}
}
