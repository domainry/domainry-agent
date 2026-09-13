package agent

import (
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestDelegationParticipantScopeMessagesAndRevocation(t *testing.T) {
	repo, owner, d := peerFixture(t)
	viewer := owner
	viewer.UserID = "participant"
	viewer.RoleKey = "participant-reviewer"
	if _, err := repo.ConversationDelegation(t.Context(), d.ID, viewer); err == nil {
		t.Fatal("unassigned user read private delegation")
	}
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: viewer.UserID, Operations: []string{"view", "communicate"}}}
	input := sdk.ConversationDelegationUpdate{ClientID: "members", ExpectedRevision: d.Revision, Action: "set_participants", Reason: "Join this review", Participants: &grants}
	changed, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, input, owner)
	if err != nil || changed.ParticipantsRevision != 1 || len(changed.Participants) != 1 || changed.OwnerUserID != owner.UserID {
		t.Fatal("participant admission", changed, err)
	}
	again, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, input, owner)
	if err != nil || again.Revision != changed.Revision {
		t.Fatal("participant grant replay", err)
	}
	if _, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, input, viewer); err == nil {
		t.Fatal("participant replaced owner's grants")
	}
	list, err := NewConversationStore(repo.store).ConversationDelegations(t.Context(), "", viewer)
	if err != nil || len(list) != 1 || list[0].ID != d.ID {
		t.Fatal("persisted participant directory", list, err)
	}
	if _, err := repo.Get(t.Context(), d.ConversationID, viewer); err == nil {
		t.Fatal("participant grant opened private conversation")
	}
	foreign := viewer
	foreign.WorkspaceID = "foreign"
	if _, err := repo.ConversationDelegation(t.Context(), d.ID, foreign); err == nil {
		t.Fatal("participant escaped workspace")
	}
	foreign = viewer
	foreign.RuntimeID = "foreign"
	if _, err := repo.ConversationDelegation(t.Context(), d.ID, foreign); err == nil {
		t.Fatal("participant escaped runtime")
	}
	messageInput := sdk.ConversationAgentMessageSend{ClientID: "participant-message", ToAgentID: d.ToAgentID, BriefVersion: 1, AgreementRevision: 1, Content: "Check original totals", ExecutionAgent: &sdk.ConversationAgentSnapshot{ID: "forged-executor"}}
	message, err := repo.SendConversationAgentMessage(t.Context(), d.ID, messageInput, "", viewer)
	if err != nil || message.FromUserID != viewer.UserID || message.ParticipantUserID != viewer.UserID || message.ParticipantRoleKey != viewer.RoleKey || message.ParticipantRevision != 1 {
		t.Fatal("participant lost real identity", message, err)
	}
	replayed, err := repo.SendConversationAgentMessage(t.Context(), d.ID, messageInput, "", viewer)
	if err != nil || replayed.ID != message.ID || replayed.ParticipantRevision != message.ParticipantRevision {
		t.Fatal("recipient snapshot changed sender idempotency", replayed, err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), owner.RuntimeID)
	if err != nil || !ok || launch.Authority.UserID != owner.UserID {
		t.Fatal("participant changed executor", launch, err)
	}
	claim, ok, err := repo.Claim(t.Context(), owner.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	inbox, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, owner)
	if err != nil || len(inbox) != 1 || inbox[0].ID != message.ID {
		t.Fatal("cross-user message did not reach recipient", inbox, err)
	}
	// Withdraw then re-add: old, unconsumed input must not regain authority.
	current, _ := repo.ConversationDelegation(t.Context(), d.ID, owner)
	empty := []sdk.ConversationDelegationParticipantInput{}
	current, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "remove", ExpectedRevision: current.Revision, Action: "set_participants", Reason: "Remove participant", Participants: &empty}, owner)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := repo.ConversationAgentMessages(t.Context(), d.ID, owner)
	if err != nil || len(messages) != 1 || !messages[0].Superseded || messages[0].ConsumedByRunID != "" || messages[0].Content != message.Content {
		t.Fatal("withdrawal did not preserve and invalidate pending message", messages, err)
	}
	frozen := executionStoreInput()
	frozen.InboxMessageIDs = []string{message.ID}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &frozen); err == nil {
		t.Fatal("grant revoked after inbox read still consumed")
	}
	if _, err := repo.ConversationDelegation(t.Context(), d.ID, viewer); err == nil {
		t.Fatal("removed participant retained view")
	}
	current, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "rejoin", ExpectedRevision: current.Revision, Action: "set_participants", Reason: "New participation", Participants: &grants}, owner)
	if err != nil || current.Participants[0].Revision == message.ParticipantRevision {
		t.Fatal("new membership reused old authority", current, err)
	}
	if inbox, err = NewConversationStore(repo.store).ConversationPeerInbox(t.Context(), d.ConversationID, owner); err != nil || len(inbox) != 0 {
		t.Fatal("rejoining revived old pending input", inbox, err)
	}
	grants[0].Operations = []string{"view"}
	current, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "observer", ExpectedRevision: current.Revision, Action: "set_participants", Reason: "Observe only", Participants: &grants}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ConversationAgentMessages(t.Context(), d.ID, viewer); err == nil {
		t.Fatal("view-only participant read messages")
	}
	if _, err := repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "denied", ToAgentID: d.ToAgentID, BriefVersion: 1, Content: "No communication grant"}, "", viewer); err == nil {
		t.Fatal("view-only participant sent message")
	}
}
