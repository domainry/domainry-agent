package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func receiverManagementFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation) {
	t.Helper()
	repo, issuer, receiver, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	launch, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID)
	if err != nil || !found || launch.Authority != receiver {
		t.Fatal("wrong original executor", launch, err)
	}
	claim, found, err := repo.Claim(t.Context(), issuer.RuntimeID, "original-worker", time.Minute)
	if err != nil || !found || claim.Authority != receiver {
		t.Fatal("wrong original claim", claim, err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Original work completed"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "receiver-delivery", ExpectedRevision: d.Revision, Action: "deliver", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Summary: "Original totals checked", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Original receiver checked totals"}}}}, receiver)
	if err != nil {
		t.Fatal(err)
	}
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: receiver.UserID, Operations: []string{"view", "manage"}}}
	d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "receiver-management", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, receiver, d
}

func TestReceiverManagementUsesAdmittedDeliveryReadAndRejectsWithdrawnReplay(t *testing.T) {
	repo, issuer, receiver, d := receiverManagementFixture(t)
	request := sdk.ConversationDelegationUpdate{ClientID: "receiver-review", ExpectedRevision: d.Revision, Action: "review_delivery", Review: peerAcceptanceReview(d), Reason: "Explicitly authorized receiver review"}
	reviewed, err := repo.UpdateConversationDelegation(t.Context(), d.ID, request, receiver)
	if err != nil || reviewed.Status != "delivered" || !reviewed.Verification.Ready || reviewed.Verification.ActorID != receiver.UserID || reviewed.OwnerUserID != issuer.UserID || reviewed.TaskID != d.TaskID {
		t.Fatal("receiver's independent delivery read was lost", reviewed, err)
	}
	if replay, err := NewConversationStore(repo.store).UpdateConversationDelegation(t.Context(), d.ID, request, receiver); err != nil || replay.Revision != reviewed.Revision {
		t.Fatal("review receipt changed after restart", replay, err)
	}
	accepted, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "receiver-accept", ExpectedRevision: reviewed.Revision, Action: "accept_delivery", Review: peerAcceptanceReview(reviewed), Reason: "Accept current checked delivery"}, receiver)
	if err != nil || accepted.Status != "accepted_delivery" || accepted.ExecutionSubject == nil || accepted.ExecutionSubject.UserID != receiver.UserID {
		t.Fatal("authorized receiver could not accept completed original work", accepted, err)
	}
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: receiver.UserID, Operations: []string{"view"}}}
	if _, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "withdraw-receiver-management", ExpectedRevision: accepted.Revision, Action: "set_participants", Participants: &grants}, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, request, receiver); err == nil {
		t.Fatal("withdrawn receiver replayed management using independent delivery read")
	}
	if _, err := repo.Get(t.Context(), d.SourceConversationID, receiver); err == nil {
		t.Fatal("management opened the issuer's raw conversation")
	}
}

func TestOrdinaryManagerCannotInheritReceiversDeliveryRead(t *testing.T) {
	repo, issuer, receiver, d := receiverManagementFixture(t)
	manager := receiver
	manager.UserID = "ordinary-manager"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: manager.UserID, Operations: []string{"view", "manage"}}}
	d, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "ordinary-management", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"review_delivery", "accept_delivery"} {
		if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "ordinary-" + action, ExpectedRevision: d.Revision, Action: action, Review: peerAcceptanceReview(d)}, manager); err == nil || !strings.Contains(err.Error(), "collaboration_access_denied") {
			t.Fatal("ordinary manager inherited another user's admitted delivery read", action, err)
		}
	}
}

func receiverReviewToolFixture(t *testing.T, repo *ConversationStore, receiver sdk.ConversationAuthority, d sdk.ConversationDelegation) (persistence.ConversationClaim, sdk.ConversationToolRequest, sdk.ConversationDelegationUpdate) {
	t.Helper()
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "receiver-review-peer"}, receiver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "receiver-review-step", Message: "Review the explicitly managed delegation"}, receiver); err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), receiver.RuntimeID, "receiver-reviewer", time.Minute)
	if err != nil || !found || claim.Authority.UserID != receiver.UserID || claim.Run.ConversationID != c.ID {
		t.Fatal("wrong actual peer claim", claim, err)
	}
	update := sdk.ConversationDelegationUpdate{ClientID: "receiver-agent-review", ExpectedRevision: d.Revision, Action: "review_delivery", Review: peerAcceptanceReview(d), Reason: "Receiver Agent reviewed current delivery"}
	arguments, err := json.Marshal(sdk.ConversationDelegationToolUpdate{ID: d.ID, Update: update})
	if err != nil {
		t.Fatal(err)
	}
	var definition sdk.ConversationToolDefinition
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "delegation_update" {
			definition = tool
		}
	}
	input := executionStoreInput()
	input.Tools = []sdk.ConversationToolDefinition{definition}
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: "receiver-review", Name: definition.Key, Arguments: string(arguments)}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true})
	if err != nil {
		t.Fatal(err)
	}
	return claim, sdk.ConversationToolRequest{Authority: receiver, ConversationID: c.ID, RunID: claim.Run.ID, Step: 0, Call: call, Definition: definition, IdempotencyKey: ledger.IdempotencyKey, LeaseOwner: claim.Owner, Fence: claim.Fence}, update
}

func TestReceiverManagementAgentReviewsFromItsOwnLeasedPeerConversation(t *testing.T) {
	repo, issuer, receiver, d := receiverManagementFixture(t)
	claim, request, update := receiverReviewToolFixture(t, repo, receiver, d)
	update.ToolRequest = &request
	reviewed, err := repo.UpdateConversationDelegation(t.Context(), d.ID, update, receiver)
	if err != nil || reviewed.Verification == nil || reviewed.Verification.Source == nil || reviewed.Verification.Source.ConversationID != claim.Run.ConversationID || reviewed.Verification.Source.RunID != claim.Run.ID || reviewed.Verification.ActorID != receiver.UserID || reviewed.Verification.Checks[0].Method != "agent" || reviewed.SourceConversationID != d.SourceConversationID || reviewed.OwnerUserID != issuer.UserID {
		t.Fatal("authorized peer review borrowed issuer identity or conversation", reviewed, err)
	}
	forged := update
	forged.ToolRequest = new(sdk.ConversationToolRequest)
	*forged.ToolRequest = request
	forged.ToolRequest.ConversationID = d.SourceConversationID
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, forged, receiver); err == nil {
		t.Fatal("forged issuer lease replayed a successful review")
	}
	if _, err := repo.Cancel(t.Context(), claim.Run.ConversationID, claim.Run.ID, receiver); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, update, receiver); err == nil {
		t.Fatal("expired peer lease replayed a successful review")
	}
}
