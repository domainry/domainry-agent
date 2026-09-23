package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/execution"
)

func disputeClaim(conclusion, period string) sdk.ConversationDisagreementClaimInput {
	return sdk.ConversationDisagreementClaimInput{Conclusion: conclusion, DataScope: "All report rows", Period: period, SourceVersion: "report-v1", Calculation: "Sum each included row once"}
}
func disputeDecision(d sdk.ConversationDelegation, outcome string) sdk.ConversationDisagreementDecision {
	return sdk.ConversationDisagreementDecision{Outcome: outcome, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, DeliveryDigest: disagreementDeliveryDigest(d), Basis: "Compared underlying report rows", Comparison: sdk.ConversationEvidenceComparison{DataScope: "Same report scope", Period: "First claim is Q1; second also includes Q2", SourceVersion: "Same immutable report version", Calculation: "Recomputed without Q2 rows"}}
}
func updateDispute(t *testing.T, repo *ConversationStore, a sdk.ConversationAuthority, d sdk.ConversationDelegation, in sdk.ConversationDisagreementChange) sdk.ConversationDelegation {
	t.Helper()
	request := sdk.ConversationDelegationUpdate{ClientID: fmt.Sprintf("issue-%s-%d", in.Operation, d.Revision), ExpectedRevision: d.Revision, Action: "disagreement", Reason: "Record evidence comparison", Disagreement: &in}
	out, err := repo.UpdateConversationDelegation(t.Context(), d.ID, request, a)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, request, a)
	if err != nil || conversationHash(out) != conversationHash(replay) {
		t.Fatal("disagreement replay changed result", err)
	}
	return out
}

func TestPeerDisagreementBlocksAcceptancePreservesHistoryAndBindsCurrentContext(t *testing.T) {
	repo, a, d := peerFixture(t)
	if _, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Report complete"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "delivery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Report", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Total is 10", Data: json.RawMessage(`{"total":10}`)}}, a)
	if err != nil {
		t.Fatal(err)
	}
	d = decidePeer(t, repo, a, d, "accept_delivery", nil)
	condition := 0
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "raise", Title: "Reported totals differ", Condition: &condition, Claims: []sdk.ConversationDisagreementClaimInput{disputeClaim("Total is 10", "Q1"), disputeClaim("Total is 15", "Q1 and Q2")}})
	if d.Status != "delivered" || len(d.Disagreements) != 1 || d.Disagreements[0].Status != "open" {
		t.Fatalf("accepted result did not return to review %+v", d)
	}
	accept := sdk.ConversationDelegationUpdate{ClientID: "premature", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "Ignore disagreement", Review: peerAcceptanceReview(d)}
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, accept, a); err == nil || !strings.Contains(err.Error(), "completion_conditions_unmet") {
		t.Fatal("open dispute accepted", err)
	}
	issue := d.Disagreements[0]
	ask := disputeDecision(d, "ask_user")
	ask.NextAction = "Confirm whether Q2 belongs in this report"
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "decide", ID: issue.ID, ExpectedRevision: issue.Revision, Decision: &ask})
	if d.Disagreements[0].OwnerUserID != a.UserID || d.Disagreements[0].Status != "waiting_user" {
		t.Fatal("missing user responsibility")
	}
	history, err := repo.ConversationDisagreementHistory(t.Context(), d.ID, issue.ID, 0, a)
	if err != nil || len(history.Items) != 2 {
		t.Fatal(err)
	}
	chosen := history.Items[0].Claims[0].ID
	adopt := disputeDecision(d, "adopt")
	adopt.AdoptClaimID = chosen
	wrong := adopt
	wrong.DeliveryDigest = strings.Repeat("f", 64)
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "wrong-context", ExpectedRevision: d.Revision, Action: "disagreement", Reason: "Review", Disagreement: &sdk.ConversationDisagreementChange{Operation: "decide", ID: issue.ID, ExpectedRevision: d.Disagreements[0].Revision, Decision: &wrong}}, a); err == nil || !strings.Contains(err.Error(), "disagreement_context_changed") {
		t.Fatal("stale delivery decision accepted", err)
	}
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "decide", ID: issue.ID, ExpectedRevision: d.Disagreements[0].Revision, Decision: &adopt})
	if d.Status != "delivered" || execution.DisagreementBlocks(d.Disagreements[0], d, disagreementDeliveryDigest(d)) {
		t.Fatal("resolution auto-accepted or remained unresolved")
	}
	d = decidePeer(t, repo, a, d, "accept_delivery", nil)
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "add_claim", ID: issue.ID, ExpectedRevision: d.Disagreements[0].Revision, Claims: []sdk.ConversationDisagreementClaimInput{disputeClaim("Total is 11 after corrected row", "Q1")}})
	if d.Status != "delivered" || d.Disagreements[0].Status != "open" {
		t.Fatal("new evidence did not reopen acceptance")
	}
	history, err = NewConversationStore(repo.store).ConversationDisagreementHistory(t.Context(), d.ID, issue.ID, 0, a)
	if err != nil || len(history.Items) != 2 || history.Complete || history.Items[1].Decision == nil || history.Items[1].Decision.AdoptClaimID != chosen || len(history.Items[0].Claims) != 3 || len(history.Items[1].Claims) != 2 {
		t.Fatalf("immutable history lost %+v %v", history, err)
	}
	older, err := repo.ConversationDisagreementHistory(t.Context(), d.ID, issue.ID, history.NextBefore, a)
	if err != nil || !older.Complete || len(older.Items) != 2 || older.Items[1].Event != "raise" {
		t.Fatalf("paging history %+v %v", older, err)
	}
	changed := history.Items[0]
	changed.Claims[0].Conclusion = "Rewrite old evidence"
	if err = repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.saveDisagreement(t.Context(), tx, d, changed, a) }); err == nil {
		t.Fatal("old issue snapshot overwritten")
	}
	other := a
	other.UserID = "unrelated"
	if _, err = repo.ConversationDisagreementHistory(t.Context(), d.ID, issue.ID, 0, other); err == nil {
		t.Fatal("cross-owner issue history read")
	}
	adopt = disputeDecision(d, "adopt")
	adopt.AdoptClaimID = chosen
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "decide", ID: issue.ID, ExpectedRevision: d.Disagreements[0].Revision, Decision: &adopt})
	brief := d.Brief
	brief.Version++
	brief.Constraints = []string{"Use only the corrected Q1 rows"}
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "new-scope", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "New scope", Brief: &brief}, a)
	if err != nil {
		t.Fatal(err)
	}
	if !execution.DisagreementBlocks(d.Disagreements[0], d, disagreementDeliveryDigest(d)) {
		t.Fatal("old decision accepted new agreement")
	}
}

func TestPeerDisagreementResponsibilityFollowsTransferWithOriginalDecisionHistory(t *testing.T) {
	repo, a, d := peerFixture(t)
	if _, found, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Check needed"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "raise", Title: "Scope conflict", Claims: []sdk.ConversationDisagreementClaimInput{disputeClaim("10", "Q1"), disputeClaim("15", "Q2")}})
	decision := disputeDecision(d, "inspect")
	decision.OwnerAgentID = d.ToAgentID
	decision.NextAction = "Read the correct period"
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "decide", ID: d.Disagreements[0].ID, ExpectedRevision: 1, Decision: &decision})
	in := transferAdmission(t, repo, a, d, "next-reviewer")
	after, err := repo.TransferConversationDelegation(t.Context(), d.ID, in, a)
	if err != nil {
		t.Fatal(err)
	}
	if after.Disagreements[0].OwnerAgentID != after.ToAgentID || after.Disagreements[0].Revision != 3 {
		t.Fatalf("old assignment retained issue responsibility %+v", after.Disagreements)
	}
	history, err := repo.ConversationDisagreementHistory(t.Context(), d.ID, d.Disagreements[0].ID, 0, a)
	if err != nil {
		t.Fatal(err)
	}
	if history.Items[0].Event != "transfer_responsibility" || history.Items[0].DecisionActor == nil || history.Items[0].Decision.OwnerAgentID != d.ToAgentID || history.Items[1].OwnerAgentID != d.ToAgentID {
		t.Fatalf("decision/history changed %+v", history)
	}
}

func TestPeerDisagreementDecisionCannotImpersonateIssuerAndNoticesTrackExactRevision(t *testing.T) {
	repo, a, d := peerFixture(t)
	d = updateDispute(t, repo, a, d, sdk.ConversationDisagreementChange{Operation: "raise", Title: "Conflict", Claims: []sdk.ConversationDisagreementClaimInput{disputeClaim("A", "Q1"), disputeClaim("B", "Q2")}})
	first, err := repo.ConversationPeerInbox(t.Context(), d.SourceConversationID, a)
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	decision := disputeDecision(d, "revise")
	decision.OwnerAgentID = d.ToAgentID
	decision.NextAction = "Recompute Q1"
	request := sdk.ConversationDelegationUpdate{ClientID: "forged", ExpectedRevision: d.Revision, Action: "disagreement", Reason: "Impersonate issuer", Disagreement: &sdk.ConversationDisagreementChange{Operation: "decide", ID: d.Disagreements[0].ID, ExpectedRevision: 1, Decision: &decision}}
	raw, _ := json.Marshal(map[string]any{"id": d.ID, "update": map[string]any{"expected_revision": d.Revision, "action": "disagreement", "reason": request.Reason, "disagreement": request.Disagreement}})
	_, actor := personalMutationFixture(t, repo, "unrelated", "delegation_update", string(raw), false)
	request.ToolRequest = &actor
	if _, err = repo.UpdateConversationDelegation(t.Context(), d.ID, request, a); err == nil || !strings.Contains(err.Error(), "delegation_actor_invalid") {
		t.Fatal("unrelated Agent decided issue", err)
	}
	d = updateDispute(t, repo, a, d, *request.Disagreement)
	old, err := repo.ConversationPeerInbox(t.Context(), d.SourceConversationID, a)
	if err != nil || len(old) != 0 {
		t.Fatal("old issue notice can still wake", err)
	}
	next, err := repo.ConversationPeerInbox(t.Context(), d.ConversationID, a)
	if err != nil || len(next) != 1 || next[0].DisagreementRevision != 2 {
		t.Fatalf("responsible Agent not notified %+v %v", next, err)
	}
	ready, err := repo.peerMessageReady(t.Context(), repo.store.Database(), first[0], a)
	if err != nil || ready {
		t.Fatal("stale issue notification accepted", err)
	}
}
