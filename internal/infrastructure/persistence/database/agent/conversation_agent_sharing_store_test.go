package agent

import (
	"fmt"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestSharedAgentKeepsExecutionOwnersAndGlobalCapacitySeparate(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	owner := conversationTestAuthority()
	caller := owner
	caller.UserID = "shared-caller"
	users := []string{caller.UserID}
	in := sdk.ConversationAgentWrite{ClientID: "shared", Name: "Shared reviewer", Instructions: "Review", Tools: []string{"time_now"}, ModelKey: "default", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users}
	agent, err := repo.WriteConversationAgent(t.Context(), "", in, owner)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := NewConversationStore(store).ConversationAgent(t.Context(), agent.ID, caller)
	if err != nil || !shared.Shared || shared.OwnerUserID != owner.UserID || shared.SharedWithUserIDs != nil {
		t.Fatal("shared configuration scope", shared, err)
	}
	if _, err := repo.WriteConversationAgent(t.Context(), agent.ID, sdk.ConversationAgentWrite{ClientID: "hijack", ExpectedRevision: 1, Name: "changed"}, caller); err == nil {
		t.Fatal("recipient changed source configuration")
	}
	for _, foreign := range []sdk.ConversationAuthority{{Known: true, RuntimeID: owner.RuntimeID, WorkspaceID: "foreign", UserID: caller.UserID}, {Known: true, RuntimeID: "foreign", WorkspaceID: owner.WorkspaceID, UserID: caller.UserID}, {Known: true, RuntimeID: owner.RuntimeID, WorkspaceID: owner.WorkspaceID, UserID: "stranger"}} {
		if _, err := repo.ConversationAgent(t.Context(), agent.ID, foreign); err == nil {
			t.Fatal("shared configuration escaped scope", foreign)
		}
	}
	admit := func(a sdk.ConversationAuthority) sdk.ConversationDelegation {
		t.Helper()
		source, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "source"}, a)
		if err != nil {
			t.Fatal(err)
		}
		brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Review", Deliverable: "Result", CompletionConditions: []string{"done"}}
		budget := sdk.ConversationTaskBudget{MaxSteps: 6, MaxToolCalls: 6, MaxOutputBytes: 2048, TimeoutSeconds: 60}
		subject := &sdk.ConversationExecutionSubject{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: a.UserID}
		admission := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Review", Brief: brief, Budget: budget}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: 1, OwnerUserID: owner.UserID, ExecutionSubject: subject}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: source.ID, Budget: budget}}
		d, err := repo.CreateConversationDelegation(t.Context(), admission, a)
		if err != nil {
			t.Fatal(err)
		}
		if d.ExecutionSubject == nil || *d.ExecutionSubject != *subject {
			t.Fatal("wrong persisted execution principal", d.ExecutionSubject)
		}
		return d
	}
	first, second := admit(owner), admit(caller)
	for _, pair := range []struct {
		id string
		a  sdk.ConversationAuthority
	}{{first.ID, caller}, {second.ID, owner}} {
		if _, err := repo.ConversationDelegation(t.Context(), pair.id, pair.a); err == nil {
			t.Fatal("configuration sharing granted private task access")
		}
	}
	for range 2 {
		if _, ok, err := repo.LaunchConversationTask(t.Context(), owner.RuntimeID); err != nil || !ok {
			t.Fatal("launch", ok, err)
		}
	}
	claim, ok, err := repo.Claim(t.Context(), owner.RuntimeID, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatal("first claim", ok, err)
	}
	if claim.Run.Agent.ExecutionSubject == nil || claim.Run.Agent.ExecutionSubject.UserID != claim.Authority.UserID {
		t.Fatal("worker changed principal", claim.Authority, claim.Run.Agent)
	}
	if _, ok, err := repo.Claim(t.Context(), owner.RuntimeID, "worker-2", time.Minute); err != nil || ok {
		t.Fatal("shared Agent exceeded one global slot", ok, err)
	}
	for _, a := range []sdk.ConversationAuthority{owner, caller} {
		observations, err := repo.ConversationAgentObservations(t.Context(), a)
		if err != nil || observations.Load[agent.ID].Running != 1 || observations.Load[agent.ID].Queued != 1 || len(observations.History) != 0 {
			t.Fatal("shared load aggregate", observations, err)
		}
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, a := range []sdk.ConversationAuthority{owner, caller} {
		observations, err := repo.ConversationAgentObservations(t.Context(), a)
		want := 0
		if a.UserID == claim.Authority.UserID {
			want = 1
		}
		if err != nil || len(observations.History) != want {
			t.Fatal("shared configuration exposed another caller's execution history", a.UserID, observations, err)
		}
	}
	in.ClientID = "revoke"
	in.ExpectedRevision = 1
	empty := []string{}
	in.SharedWithUserIDs = &empty
	if _, err := repo.WriteConversationAgent(t.Context(), agent.ID, in, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConversationStore(store).ConversationAgent(t.Context(), agent.ID, caller); err == nil {
		t.Fatal("revoked grant survived reload")
	}
	// Revocation must not leave an unclaimable row poisoning other queued work.
	if _, ok, err := repo.Claim(t.Context(), owner.RuntimeID, "worker-after-revoke", time.Minute); err != nil || !ok {
		t.Fatal("revoked configuration prevented terminal processing", ok, err)
	}
}

func TestAgentSharingAdmissionIsAtomicAtRecipientDirectoryLimit(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	owner := conversationTestAuthority()
	viewer := owner
	viewer.UserID = "full-directory"
	for i := range 64 {
		if _, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: fmt.Sprintf("own-%d", i), Name: "Agent", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, viewer); err != nil {
			t.Fatal(err)
		}
	}
	users := []string{viewer.UserID}
	if _, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "over-limit", Name: "must roll back", SharedWithUserIDs: &users}, owner); err == nil {
		t.Fatal("incomplete directory admitted")
	}
	list, err := repo.ConversationAgents(t.Context(), owner)
	if err != nil || len(list) != 0 {
		t.Fatal("rejected share left partial Agent", list, err)
	}
}
