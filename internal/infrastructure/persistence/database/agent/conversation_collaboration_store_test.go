package agent

import (
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"testing"
	"time"
)

func TestConversationAgentMessageRateIsPerSenderAndReplayIsFree(t *testing.T) {
	repo, a, d := peerFixture(t)
	first := sdk.ConversationAgentMessageSend{ClientID: "rate-0", ToAgentID: d.ToAgentID, Content: "message 0", BriefVersion: 1}
	written, err := repo.SendConversationAgentMessage(t.Context(), d.ID, first, "", a)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 16; i++ {
		_, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: fmt.Sprintf("rate-%d", i), ToAgentID: d.ToAgentID, Content: fmt.Sprintf("message %d", i), BriefVersion: 1}, "", a)
		if err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := repo.SendConversationAgentMessage(t.Context(), d.ID, first, "", a)
	if err != nil || replayed.ID != written.ID {
		t.Fatalf("idempotent replay consumed rate allowance: %+v %v", replayed, err)
	}
	_, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "rate-blocked", ToAgentID: d.ToAgentID, Content: "message 17", BriefVersion: 1}, "", a)
	requireConversationCode(t, err, "agent_message_rate")
}

func peerFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationDelegation) {
	t.Helper()
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	agent, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "receiver", Name: "Reviewer", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Review the report", Deliverable: "Findings", CompletionConditions: []string{"Check totals"}}
	budget := sdk.ConversationTaskBudget{MaxSteps: 6, MaxToolCalls: 6, MaxOutputBytes: 2048, TimeoutSeconds: 60}
	in := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "delegation", ConversationID: c.ID, AgentID: agent.ID, Purpose: "Independent review", Brief: brief, Budget: budget}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: agent.ID, Revision: 1}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: c.ID, Budget: budget}}
	d, err := repo.CreateConversationDelegation(t.Context(), in, a)
	if err != nil {
		t.Fatal(err)
	}
	again, err := newTestConversationStore(t, store).CreateConversationDelegation(t.Context(), in, a)
	if err != nil || again.ID != d.ID {
		t.Fatalf("replay %+v %v", again, err)
	}
	return repo, a, d
}
func TestPeerDelegationIndependentContextAndDurableInbox(t *testing.T) {
	repo, a, d := peerFixture(t)
	if d.ConversationID == d.SourceConversationID || d.FromAgentID == d.ToAgentID {
		t.Fatal("identities or contexts collapsed")
	}
	for _, other := range []sdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}} {
		if _, err := repo.ConversationDelegation(t.Context(), d.ID, other); err == nil {
			t.Fatal("cross-owner read")
		}
	}
	send := sdk.ConversationAgentMessageSend{ClientID: "steering", ToAgentID: d.ToAgentID, Content: "Also verify currency", BriefVersion: 1, ExecutionAgent: &sdk.ConversationAgentSnapshot{ID: d.ToAgentID}}
	msg, err := repo.SendConversationAgentMessage(t.Context(), d.ID, send, "", a)
	if err != nil || msg.FromUserID != a.UserID {
		t.Fatalf("user message %+v %v", msg, err)
	}
	again, err := repo.SendConversationAgentMessage(t.Context(), d.ID, send, "", a)
	if err != nil || again.ID != msg.ID {
		t.Fatal("duplicate message", err)
	}
	if _, err = repo.SendConversationAgentMessage(t.Context(), d.ID, send, "unrelated", a); err == nil {
		t.Fatal("forged sender")
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !ok || launch.Run.ConversationID != d.ConversationID {
		t.Fatalf("launch %+v %v", launch, err)
	}
	if _, err = repo.Enqueue(t.Context(), d.ConversationID, sdk.ConversationSend{ClientMessageID: "bypass", Message: "bypass"}, a); err == nil {
		t.Fatal("delegation budget bypass")
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	input := executionStoreInput()
	input.InboxMessageIDs = []string{msg.ID}
	input.Messages = append(input.Messages, sdk.ConversationStepMessage{Role: "user", Content: msg.Content})
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	inbox, err := NewConversationStore(repo.store).ConversationPeerInbox(t.Context(), d.ConversationID, a)
	if err != nil || len(inbox) != 0 {
		t.Fatal("inbox not consumed", err)
	}
	step, found, err := repo.ExecutionStep(t.Context(), claim, 0, nil)
	if err != nil || !found || len(step.Input.InboxMessageIDs) != 1 {
		t.Fatal("frozen inbox lost", err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	notice, err := repo.ConversationPeerInbox(t.Context(), d.SourceConversationID, a)
	if err != nil || len(notice) != 1 || notice[0].Kind != "task_completed" {
		t.Fatalf("notice %+v %v", notice, err)
	}
	callback, found, err := NewConversationStore(repo.store).LaunchConversationPeerMessage(t.Context(), a.RuntimeID)
	if err != nil || !found || callback.ConversationID != d.SourceConversationID {
		t.Fatalf("callback %+v %v", callback, err)
	}
	history, err := repo.Messages(t.Context(), d.SourceConversationID, sdk.ConversationMessageQuery{}, a)
	if err != nil || len(history.Items) == 0 || history.Items[len(history.Items)-1].PeerEvent == nil || history.Items[len(history.Items)-1].PeerEvent.FromAgentID != d.ToAgentID {
		t.Fatalf("callback lost actual peer event identity: %+v %v", history, err)
	}
	if _, found, err = repo.LaunchConversationPeerMessage(t.Context(), a.RuntimeID); err != nil || found {
		t.Fatalf("duplicate callback %v %v", found, err)
	}
}
func TestPeerBriefUpdateAtomicallyStopsOldRunAndResumesNewVersion(t *testing.T) {
	repo, a, d := peerFixture(t)
	_, _, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	input := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	d, _ = repo.ConversationDelegation(t.Context(), d.ID, a)
	brief := d.Brief
	brief.Version = 2
	brief.Goal = "Review prior quarter"
	change := sdk.ConversationDelegationUpdate{ClientID: "requirements", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Period corrected", Brief: &brief}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, change, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Run(t.Context(), d.ConversationID, claim.Run.ID, a)
	if err != nil || run.Status != "cancelled" {
		t.Fatalf("old run %+v %v", run, err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "stale result"}, ""); err == nil {
		t.Fatal("old worker wrote result")
	}
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, change, a)
	if err != nil || replay.Revision != d.Revision {
		t.Fatal("change not replayable", err)
	}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "continue", ExpectedRevision: d.Revision, Action: "resume", Reason: "Use corrected period"}, a)
	if err != nil {
		t.Fatal(err)
	}
	launch, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID)
	if err != nil || !ok || launch.Run.ID == claim.Run.ID || launch.Run.BackgroundTask.BriefVersion != 2 || launch.Run.BackgroundTask.Budget.MaxSteps != 5 {
		t.Fatalf("new version launch %+v %v", launch, err)
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "stale", ExpectedRevision: 1, Action: "pause", Reason: "stale"}, a); err == nil {
		t.Fatal("revision guard bypassed")
	}
}
func TestPeerMutationLeaseAndConfirmationCannotBeForged(t *testing.T) {
	repo, a, d := peerFixture(t)
	claim, req := personalMutationFixture(t, repo, "message-caller", "delegation_update", `{"id":"x","update":{"expected_revision":1,"action":"cancel","reason":"stop"}}`, false)
	send := sdk.ConversationAgentMessageSend{ClientID: "write", ToAgentID: d.ToAgentID, Content: "forged", BriefVersion: 1, ToolRequest: &req}
	if _, err := repo.SendConversationAgentMessage(t.Context(), d.ID, send, d.SourceConversationID, a); err == nil {
		t.Fatal("unconfirmed effect")
	}
	if _, err := repo.Cancel(t.Context(), claim.Run.ConversationID, claim.Run.ID, a); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SendConversationAgentMessage(t.Context(), d.ID, send, d.SourceConversationID, a); err == nil {
		t.Fatal("expired worker effect")
	}
	messages, _ := repo.ConversationAgentMessages(t.Context(), d.ID, a)
	if len(messages) != 0 {
		t.Fatal("denied write persisted")
	}
}
func TestPeerCapacityCycleAndSchemaMetadata(t *testing.T) {
	repo, a, d := peerFixture(t)
	in := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "cycle", ConversationID: d.ConversationID, AgentID: d.FromAgentID}, FromAgentID: d.ToAgentID, Agent: sdk.ConversationAgentSnapshot{ID: d.FromAgentID}, Task: sdk.ConversationTask{SourceConversationID: d.ConversationID}}
	if _, err := repo.CreateConversationDelegation(t.Context(), in, a); err == nil {
		t.Fatal("cycle accepted")
	}
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "parallel", AgentID: d.ToAgentID}, a)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &sdk.ConversationAgentSnapshot{ID: d.ToAgentID, Revision: 1}
	if _, err = repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "one", Message: "work", ExecutionAgent: snapshot}, a); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.Claim(t.Context(), a.RuntimeID, "first", time.Minute); err != nil || !ok {
		t.Fatal(err)
	}
	if _, _, err = repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.Claim(t.Context(), a.RuntimeID, "second", time.Minute); err != nil || ok {
		t.Fatalf("capacity exceeded %v %v", ok, err)
	}
	for _, tool := range sdk.ConversationCollaborationTools() {
		var schema map[string]any
		if unmarshalDurableJSON(tool.InputSchema, &schema) != nil {
			t.Fatal("schema invalid")
		}
	}
}

func TestPeerInboxSkipsMoreThanOnePageOfPausedWork(t *testing.T) {
	repo, a, d := peerFixture(t)
	_, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "pause", ExpectedRevision: d.Revision, Action: "pause", Reason: "wait"}, a)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 65; n++ {
		if n > 0 && n%15 == 0 {
			// Keep this pagination fixture within the production one-minute
			// sender rate while retaining every pending inbox row.
			if _, err = repo.store.Database().ExecContext(t.Context(), "UPDATE "+conversationAgentMessageTable+" SET created_at = 0 WHERE owner_key = ? AND delegation_id = ?", conversationOwner(a), d.ID); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := marshalDurableJSON(n)
		_, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "pending-" + string(raw), ToAgentID: d.ToAgentID, Content: "wait", BriefVersion: 1}, "", a)
		if err != nil {
			t.Fatal(err)
		}
	}
	// A later message to the available sender must still launch after the paused page.
	_, err = repo.SendConversationAgentMessage(t.Context(), d.ID, sdk.ConversationAgentMessageSend{ClientID: "available", ToAgentID: d.FromAgentID, Content: "review waiting state", BriefVersion: 1}, "", a)
	if err != nil {
		t.Fatal(err)
	}
	run, ok, err := repo.LaunchConversationPeerMessage(t.Context(), a.RuntimeID)
	if err != nil || !ok || run.ConversationID != d.SourceConversationID {
		t.Fatalf("blocked by paused inbox: %+v %v", run, err)
	}
}
