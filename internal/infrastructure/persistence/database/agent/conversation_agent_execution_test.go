package agent

import (
	sdk "github.com/domainry/domainry-agent-sdk"
	"testing"
)

func TestAgentExecutionBindingPersistsRoleAndRejectsIdentityChangingRetry(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	a.RoleKey = "first-role"
	mode := "owner"
	in := sdk.ConversationAgentWrite{ClientID: "bind-role", Name: "Review", Instructions: "Review", Enabled: true, MaxConcurrent: 1, DelegationExecution: &mode}
	agent, err := repo.WriteConversationAgent(t.Context(), "", in, a)
	if err != nil || agent.DelegationRoleKey != a.RoleKey {
		t.Fatal(agent, err)
	}
	changed := a
	changed.RoleKey = "second-role"
	if _, err := repo.WriteConversationAgent(t.Context(), "", in, changed); err == nil {
		t.Fatal("retry changed binding role without conflict")
	}
	in.ClientID = "rename"
	in.ExpectedRevision = agent.Revision
	in.Name = "Renamed"
	in.DelegationExecution = nil
	agent, err = NewConversationStore(store).WriteConversationAgent(t.Context(), agent.ID, in, changed)
	if err != nil || agent.DelegationRoleKey != a.RoleKey {
		t.Fatal("ordinary edit rebound role", agent, err)
	}
	mode = "owner"
	in.ClientID = "rebind"
	in.ExpectedRevision = agent.Revision
	in.DelegationExecution = &mode
	agent, err = repo.WriteConversationAgent(t.Context(), agent.ID, in, changed)
	if err != nil || agent.DelegationRoleKey != changed.RoleKey {
		t.Fatal("explicit rebind did not capture authenticated role", agent, err)
	}
	mode = "caller"
	in.ClientID = "disable"
	in.ExpectedRevision = agent.Revision
	agent, err = repo.WriteConversationAgent(t.Context(), agent.ID, in, changed)
	if err != nil || agent.DelegationRoleKey != "" || agent.DelegationExecution != "" {
		t.Fatal("disable kept role binding", agent, err)
	}
}
