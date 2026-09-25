package agent

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func peerAcceptanceReview(d sdk.ConversationDelegation) *sdk.ConversationDeliveryReview {
	items := []sdk.ConversationConditionAssessment{}
	for index := range d.Brief.CompletionConditions {
		items = append(items, sdk.ConversationConditionAssessment{Condition: index, Verdict: "met", Basis: "Checked this condition against the actual delivery"})
	}
	return &sdk.ConversationDeliveryReview{DeliveryDigest: conversationHash(d.Delivery), Conditions: items}
}

func TestPeerVerificationReadsActualReceiptsAndCannotOverrideFailedData(t *testing.T) {
	repo, a, d := peerFixture(t)
	brief := d.Brief
	brief.Version++
	brief.CompletionConditions = []string{"Created the requested record", "Delivery total is 3"}
	brief.VerificationRules = []sdk.ConversationCompletionRule{
		{Condition: 0, Kind: "receipt", Tool: "create_thing", ArgumentsSchema: json.RawMessage(`{"type":"object","properties":{"name":{"const":"requested"}},"required":["name"]}`), ResultSchema: json.RawMessage(`{"type":"object","required":["id"]}`)},
		{Condition: 1, Kind: "data", Schema: json.RawMessage(`{"type":"object","properties":{"total":{"const":3}},"required":["total"]}`)},
	}
	var err error
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "rules", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "Declare checks", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	d = decidePeer(t, repo, a, d, "resume", nil)
	if _, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	input := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: "create", Name: "create_thing", Arguments: `{"name":"requested"}`}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true, Revision: "grant"}); err != nil {
		t.Fatal(err)
	}
	result := sdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"record-1"}`)}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, result); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Done"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	ref := sdk.ConversationResultReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Step: 0, CallID: call.ID, SHA256: conversationHash(result)}
	delivery := &sdk.ConversationDelegationDelivery{BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Summary: "Created record", Data: json.RawMessage(`{"total":2}`), Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Original create receipt", Receipts: []sdk.ConversationResultReference{ref}}}}
	bad := ref
	bad.SHA256 = strings.Repeat("f", 64)
	delivery.Conditions[0].Receipts[0] = bad
	request := sdk.ConversationDelegationUpdate{ClientID: "submit", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Deliver", Delivery: delivery}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a); err == nil || !strings.Contains(err.Error(), "completion_evidence_changed") {
		t.Fatal("forged hash accepted", err)
	}
	delivery.Conditions[0].Receipts[0] = ref
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a)
	if err != nil || d.Verification.Ready || d.Verification.Checks[0].Verdict != "met" || d.Verification.Checks[1].Verdict != "unmet" {
		t.Fatalf("program failed to check actual inputs: %+v %v", d.Verification, err)
	}
	oldDigest := d.Verification.DeliveryDigest
	review := peerAcceptanceReview(d)
	review.Conditions[0].Receipts = []sdk.ConversationResultReference{ref}
	accept := sdk.ConversationDelegationUpdate{ClientID: "override", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "Claim all met", Review: review}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a); err == nil || !strings.Contains(err.Error(), "completion_conditions_unmet") {
		t.Fatal("claimed verdict overrode failed data", err)
	}
	delivery.Data = json.RawMessage(`{"total":3}`)
	request.ClientID, request.ExpectedRevision = "corrected", d.Revision
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a)
	if err != nil || !d.Verification.Ready || d.Verification.DeliveryDigest == oldDigest {
		t.Fatal("corrected delivery not evaluated", err)
	}
	accept.ClientID, accept.ExpectedRevision = "accept", d.Revision
	accept.Review = &sdk.ConversationDeliveryReview{DeliveryDigest: d.Verification.DeliveryDigest}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a)
	if err != nil || d.Status != "accepted_delivery" {
		t.Fatal(err)
	}
	history, err := NewConversationStore(repo.store).ConversationDeliveryHistory(t.Context(), d.ID, 0, a)
	if err != nil || len(history.Items) != 3 || history.Items[2].Verification.Ready || history.Items[2].Verification.DeliveryDigest != oldDigest {
		t.Fatalf("old failed delivery overwritten %+v %v", history, err)
	}
	other := a
	other.UserID = "unrelated"
	otherDelivery := *delivery
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		_, err := repo.verifyDelegationDelivery(t.Context(), tx, d, otherDelivery, nil, nil, other)
		return err
	})
	if err == nil {
		t.Fatal("cross-owner receipt used for a program verdict")
	}
}

func TestPeerVerificationRequiresIndependentCurrentReviewAndRetainsHistory(t *testing.T) {
	repo, a, d := peerFixture(t)
	if _, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "reviewer", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Done"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	delivery := &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Report checked", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Recipient says checked"}}}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "deliver", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Submit", Delivery: delivery}, a)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "delivered" || d.Verification == nil || d.Verification.Ready || d.Verification.Checks[0].Method != "recipient" {
		t.Fatalf("recipient accepted itself: %+v", d)
	}
	arguments, _ := marshalDurableJSON(map[string]any{"id": d.ID, "update": map[string]any{"expected_revision": d.Revision, "action": "review_delivery", "reason": "Unrelated review", "review": peerAcceptanceReview(d)}})
	_, unrelated := personalMutationFixture(t, repo, "unrelated-reviewer", "delegation_update", string(arguments), false)
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "wrong-reviewer", ExpectedRevision: d.Revision, Action: "review_delivery", Reason: "Unrelated review", Review: peerAcceptanceReview(d), ToolRequest: &unrelated}, a); err == nil || !strings.Contains(err.Error(), "delegation_actor_invalid") {
		t.Fatal("unrelated Agent reviewed another delegation", err)
	}
	accept := sdk.ConversationDelegationUpdate{ClientID: "accept", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "Reviewed"}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a); err == nil || !strings.Contains(err.Error(), "completion_review_required") {
		t.Fatal("accepted without a review", err)
	}
	review := peerAcceptanceReview(d)
	request := sdk.ConversationDelegationUpdate{ClientID: "review", ExpectedRevision: d.Revision, Action: "review_delivery", Reason: "Each requirement checked", Review: review}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a)
	if err != nil || d.Status != "delivered" || !d.Verification.Ready || d.Verification.Checks[0].Method != "user" {
		t.Fatalf("review lost or accepted automatically: %+v %v", d, err)
	}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a); err != nil {
		t.Fatal("review replay", err)
	}
	accept.ExpectedRevision = d.Revision
	accept.Review = peerAcceptanceReview(d)
	accept.Review.DeliveryDigest = strings.Repeat("f", 64)
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a); err == nil || !strings.Contains(err.Error(), "delivery_changed") {
		t.Fatal("review accepted different delivery", err)
	}
	accept.Review = peerAcceptanceReview(d)
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a)
	if err != nil || d.Status != "accepted_delivery" {
		t.Fatal("reviewed current delivery rejected", err)
	}
	history, err := repo.ConversationDeliveryHistory(t.Context(), d.ID, 0, a)
	if err != nil || len(history.Items) != 3 || history.Items[0].Kind != "accept_delivery" || history.Items[2].Verification.Ready {
		t.Fatalf("history lost: %+v %v", history, err)
	}
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.saveDeliveryRecord(t.Context(), tx, d, "accept_delivery", "overwrite history", a)
	})
	if err == nil {
		t.Fatal("immutable review overwritten")
	}
	brief := d.Brief
	brief.Version++
	brief.CompletionConditions = []string{"Review a different scope"}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "changed", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "New requirement", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	accept.ClientID = "old-review"
	accept.ExpectedRevision = d.Revision
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a); err == nil {
		t.Fatal("old review accepted new requirements")
	}
	other := a
	other.UserID = "other"
	if _, err = repo.ConversationDeliveryHistory(t.Context(), d.ID, 0, other); err == nil {
		t.Fatal("cross-owner review history exposed")
	}
}
