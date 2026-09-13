package agent

import (
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestPeerDiscoveryLoadFollowsDurableTaskAndRunTransitions(t *testing.T) {
	repo, a, d := peerFixture(t)
	check := func(queued, running int) {
		t.Helper()
		observed, err := NewConversationStore(repo.store).ConversationAgentObservations(t.Context(), a)
		if err != nil || observed.Load[d.ToAgentID].Queued != queued || observed.Load[d.ToAgentID].Running != running {
			t.Fatalf("wrong durable load: %+v %v", observed, err)
		}
	}
	check(1, 0)
	if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
		t.Fatalf("launch %v %v", ok, err)
	}
	check(1, 0) // task launch must not count both a queued task and its run
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "discovery-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	check(0, 1)
	other := a
	other.UserID = "another-user"
	observed, err := repo.ConversationAgentObservations(t.Context(), other)
	if err != nil || len(observed.Load) != 0 || len(observed.History) != 0 {
		t.Fatalf("cross-owner observations %+v %v", observed, err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Checked", Usage: map[string]any{"input_tokens": 123, "output_tokens": 45}}, ""); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	observed, err = repo.ConversationAgentObservations(t.Context(), a)
	if err != nil || len(observed.History) != 1 || observed.History[0].Status != "completed" || observed.History[0].Revision != 1 || observed.History[0].Usage["input_tokens"] != float64(123) {
		t.Fatalf("completion evidence %+v %v", observed, err)
	}
	d, _ = repo.ConversationDelegation(t.Context(), d.ID, a)
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "deliver-discovery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Done", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, Summary: "Checked"}}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "accept-discovery", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "Evidence checked", Review: peerAcceptanceReview(d)}, a); err != nil {
		t.Fatal(err)
	}
	observed, err = NewConversationStore(repo.store).ConversationAgentObservations(t.Context(), a)
	if err != nil || len(observed.History) != 2 {
		t.Fatalf("acceptance evidence %+v %v", observed, err)
	}
	accepted := 0
	for _, sample := range observed.History {
		if sample.Status == "accepted_delivery" {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatal("acceptance was lost or counted twice")
	}
}
