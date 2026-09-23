package agent

import (
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func sourcePublisherFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, sdk.ConversationRunReference, sdk.ConversationRunReference) {
	t.Helper()
	repo, issuer, executor, in := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, launched, err := repo.LaunchConversationTask(t.Context(), executor.RuntimeID); err != nil || !launched {
		t.Fatal("launch", launched, err)
	}
	claim, found, err := repo.Claim(t.Context(), executor.RuntimeID, "publisher-test", time.Minute)
	if err != nil || !found {
		t.Fatal("claim", found, err)
	}
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Reviewed"}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Reviewed"}, ""); err != nil {
		t.Fatal(err)
	}
	d, err = repo.ConversationDelegation(t.Context(), d.ID, executor)
	if err != nil {
		t.Fatal(err)
	}
	original := executor
	original.RoleKey = "old-proof-role"
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "old-proof"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "old-proof", Message: "Old receipt"}, original)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, executor, d, sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}, sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, BeforeStep: 1}
}

func TestSourcePublisherManualDeliveryUsesActualRoleAndReviewDoesNotRepublish(t *testing.T) {
	repo, issuer, executor, d, root, _ := sourcePublisherFixture(t)
	publisher := executor
	publisher.RoleKey = "actual-manual-publishing-role"
	delivery := sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Reviewed", Evidence: []sdk.ConversationRunReference{root}, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Compared totals"}}}
	var err error
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "manual-delivery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Share original receipt", Delivery: &delivery}, publisher)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		items, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
		if err != nil || len(items) != 1 || items[0].Publisher == nil || *items[0].Publisher != publisher || items[0].Producer.RoleKey != "old-proof-role" {
			t.Fatal("actual publisher replaced by admission/reviewer role", items, err)
		}
	}
	check()
	reviewer := issuer
	reviewer.RoleKey = "actual-reviewing-role"
	for _, action := range []string{"review_delivery", "accept_delivery"} {
		d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: action, ExpectedRevision: d.Revision, Action: action, Reason: "Assess submitted result", Review: peerAcceptanceReview(d)}, reviewer)
		if err != nil {
			t.Fatal(action, err)
		}
		check()
	}
}
