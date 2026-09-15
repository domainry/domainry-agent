package agent

import (
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func completedForkSource(t *testing.T, repo *ConversationStore, clientID string, authority agentsdk.ConversationAuthority) (agentsdk.Conversation, agentsdk.ConversationRun) {
	t.Helper()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: clientID, Title: "Source", MemoryEnabled: true}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: clientID + "-message", Message: "Explore the first approach"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), authority.RuntimeID, clientID+"-worker", time.Minute)
	if err != nil || !found || claim.Run.ID != run.ID {
		t.Fatalf("claim=%+v found=%t err=%v", claim, found, err)
	}
	input := agentsdk.ConversationModelRequest{
		Messages:       []agentsdk.ConversationModelMessage{{Role: "system", Content: "Current system"}, {Role: "user", Content: "Explore the first approach"}},
		ModelIdentity:  agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "messages", Model: "fixture", Fingerprint: "fixture-v1"},
		Purpose:        "reply",
		IdempotencyKey: run.ID + ":reply",
		MaxOutputBytes: 1024,
	}
	if _, found, err = repo.ModelInput(t.Context(), claim, &input); err != nil || !found {
		t.Fatalf("freeze input found=%t err=%v", found, err)
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "First answer", Model: "fixture"}, ""); err != nil {
		t.Fatal(err)
	}
	run, err = repo.Run(t.Context(), conversation.ID, run.ID, authority)
	if err != nil || run.Status != "completed" || run.AssistantMessageID == "" || run.CompletedAt == nil || run.LastEventSeq < 1 {
		t.Fatalf("completed source=%+v err=%v", run, err)
	}
	return conversation, run
}

func TestConversationForkIsAtomicIdempotentAndOwnerScoped(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := conversationTestAuthority()
	source, run := completedForkSource(t, repo, "fork-source", authority)
	seed := persistence.ConversationForkSeed{
		Version:          agentsdk.ConversationTrajectoryVersion,
		Source:           agentsdk.ConversationRunReference{ConversationID: source.ID, RunID: run.ID},
		BoundaryEventSeq: run.LastEventSeq,
		SourceSHA256:     strings.Repeat("a", 64),
		Messages: []agentsdk.ConversationModelMessage{
			{Role: "system", Content: "Historical boundary"},
			{Role: "user", Content: "Explore the first approach"},
			{Role: "assistant", Content: "First answer"},
		},
		CreatedAt: *run.CompletedAt,
	}
	request := agentsdk.ConversationForkRequest{ClientID: "fork-child", Title: "Alternative", AgentID: "agent-one"}
	child, err := repo.ForkConversation(t.Context(), request, seed, authority)
	if err != nil {
		t.Fatal(err)
	}
	if child.ID == source.ID || child.Fork == nil || child.Fork.ConversationID != source.ID || child.Fork.RunID != run.ID || child.Fork.BoundaryEventSeq != run.LastEventSeq || child.ActiveRunID != "" || child.Revision != 1 || !child.MemoryEnabled {
		t.Fatalf("fork=%+v source=%+v run=%+v", child, source, run)
	}
	replayed, err := repo.ForkConversation(t.Context(), request, seed, authority)
	if err != nil || replayed.ID != child.ID {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	changed := request
	changed.Title = "Changed alternative"
	_, err = repo.ForkConversation(t.Context(), changed, seed, authority)
	requireConversationCode(t, err, "idempotency_conflict")

	stored, found, err := repo.ConversationForkSeed(t.Context(), child.ID, authority)
	if err != nil || !found || conversationHash(stored) != conversationHash(seed) {
		t.Fatalf("seed=%+v found=%t err=%v", stored, found, err)
	}
	other := authority
	other.UserID = "other-user"
	if _, found, err = repo.ConversationForkSeed(t.Context(), child.ID, other); err != nil || found {
		t.Fatalf("foreign seed found=%t err=%v", found, err)
	}
	if err = repo.Delete(t.Context(), child.ID, child.Revision, authority); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repo.ConversationForkSeed(t.Context(), child.ID, authority); err != nil || found {
		t.Fatalf("deleted seed found=%t err=%v", found, err)
	}
}

func TestConversationForkRejectsAnUnstableBoundary(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := conversationTestAuthority()
	source, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "unstable-source"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), source.ID, agentsdk.ConversationSend{ClientMessageID: "unstable-message", Message: "still running"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err = repo.Run(t.Context(), source.ID, run.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	seed := persistence.ConversationForkSeed{
		Version:          agentsdk.ConversationTrajectoryVersion,
		Source:           agentsdk.ConversationRunReference{ConversationID: source.ID, RunID: run.ID},
		BoundaryEventSeq: run.LastEventSeq,
		SourceSHA256:     strings.Repeat("b", 64),
		Messages:         []agentsdk.ConversationModelMessage{{Role: "system", Content: "Historical boundary"}, {Role: "user", Content: "still running"}},
		CreatedAt:        time.Now().UTC(),
	}
	if !validConversationForkSeed(seed) {
		t.Fatalf("fixture seed invalid: run=%+v seed=%+v", run, seed)
	}
	_, err = repo.ForkConversation(t.Context(), agentsdk.ConversationForkRequest{ClientID: "unstable-child", Title: "Unstable child"}, seed, authority)
	requireConversationCode(t, err, "fork_boundary_unstable")
	page, listErr := repo.List(t.Context(), agentsdk.ConversationQuery{IncludeArchived: true}, authority)
	if listErr != nil || len(page.Items) != 1 || page.Items[0].ID != source.ID {
		t.Fatalf("unstable fork left a child: page=%+v err=%v", page, listErr)
	}
}
