package agent

import (
	"database/sql"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func participantDeliveryStoreFixture(t *testing.T, grantFirst bool) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, sdk.ConversationRunReference) {
	t.Helper()
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	reader := issuer
	reader.UserID, reader.RoleKey = "third-reader", "delivery-reading-role"
	if grantFirst {
		grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "delivery_read"}}}
		d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "grant-before-delivery", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), issuer.RuntimeID, "published-source-worker", time.Minute)
	if err != nil || !found || claim.Authority != executor {
		t.Fatal("wrong original execution", claim, err)
	}
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: "submitted-result", Name: "create_thing", Arguments: `{"name":"requested"}`}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	result := sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"id":"original-record"}`)}
	if err := repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, result); err != nil {
		t.Fatal(err)
	}
	if err := repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "Original work completed"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	ref := sdk.ConversationResultReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Step: 0, CallID: call.ID, SHA256: conversationHash(result)}
	// A later actual publishing role must not replace the provider's role.
	publishing := executor
	publishing.RoleKey = "later-publishing-role"
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "original-delivery", ExpectedRevision: d.Revision, Action: "deliver", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Submitted findings", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Original record", Receipts: []sdk.ConversationResultReference{ref}}}}}, publishing)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, publishing, reader, d, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2}
}

func TestParticipantDeliveryRelayKeepsProviderAndPublisherAndRequiresExactDelegation(t *testing.T) {
	repo, issuer, publisher, reader, d, ref := participantDeliveryStoreFixture(t, false)
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "delivery_read"}}}
	updated, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "relay-original-publication", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil || updated.Participants[0].Publisher == nil || *updated.Participants[0].Publisher != issuer {
		t.Fatal("reader grant lost its real issuer", updated, err)
	}
	releases, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), ref, reader)
	if err != nil || len(releases) != 1 || releases[0].Publisher == nil || *releases[0].Publisher != publisher || releases[0].Producer.UserID != publisher.UserID || releases[0].Producer.RoleKey != "reviewer-role" || releases[0].Reference != ref || releases[0].DelegationID != d.ID || releases[0].Purpose != "delivery" {
		t.Fatal("relay manufactured issuer publication or changed original proof", releases, err)
	}
	if _, err := repo.Get(t.Context(), d.ConversationID, reader); err == nil {
		t.Fatal("relay opened private execution")
	}
	bad := d
	bad.ID = "another-delegation"
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.shareParticipantDeliveryRoots(t.Context(), tx, bad, issuer, reader, []sdk.ConversationRunReference{ref})
	})
	if err == nil {
		t.Fatal("publication from a different delegation was relayed")
	}
}

func TestParticipantDeliveryGrantPublishesFutureDeliveriesWithoutChangingExecutor(t *testing.T) {
	repo, _, publisher, reader, d, ref := participantDeliveryStoreFixture(t, true)
	releases, err := repo.ConversationSourceReleases(t.Context(), ref, reader)
	if err != nil || len(releases) != 1 || releases[0].Publisher == nil || *releases[0].Publisher != publisher || releases[0].Producer.RoleKey != "reviewer-role" || d.ExecutionSubject.UserID != publisher.UserID {
		t.Fatal("standing delivery reader missed publication or became executor", releases, d, err)
	}
}
