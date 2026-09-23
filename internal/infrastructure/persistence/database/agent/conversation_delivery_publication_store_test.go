package agent

import (
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestRepublishAcceptedDeliveryPreservesOriginalTaskAndHistory(t *testing.T) {
	repo, issuer, executor, d, root, _ := sourcePublisherFixture(t)
	delivery := sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "Original findings", Evidence: []sdk.ConversationRunReference{root}, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "Compared original totals"}}}
	var err error
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "original-delivery", ExpectedRevision: d.Revision, Action: "deliver", Reason: "Original publication", Delivery: &delivery}, executor)
	if err != nil {
		t.Fatal(err)
	}
	deliveredRevision := d.Revision
	d, err = repo.UpdateConversationDelegation(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "accept-original", ExpectedRevision: d.Revision, Action: "accept_delivery", Reason: "Accept original", Review: peerAcceptanceReview(d)}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	accepted := conversationHash(d)
	task, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil {
		t.Fatal(err)
	}
	original, err := repo.ConversationDeliveryPublicationRecord(t.Context(), d.ID, deliveredRevision, executor)
	if err != nil {
		t.Fatal(err)
	}
	history, err := repo.ConversationDeliveryHistory(t.Context(), d.ID, 0, issuer)
	if err != nil {
		t.Fatal(err)
	}
	publisher := executor
	publisher.RoleKey = "current-explicit-publisher"
	in := sdk.ConversationDelegationUpdate{ClientID: "republish-original", Action: "republish_delivery", ExpectedRevision: d.Revision, Reason: "Explicitly share this immutable original", Publication: &sdk.ConversationDeliveryPublication{DeliveryRevision: deliveredRevision, RecordDigest: conversationHash(original)}}
	for _, kind := range []string{"digest", "revision", "actor", "body", "missing"} {
		bad, actor := in, publisher
		publication := *in.Publication
		bad.Publication, bad.ClientID = &publication, "bad-"+kind
		switch kind {
		case "digest":
			bad.Publication.RecordDigest = strings.Repeat("0", 64)
		case "revision":
			bad.ExpectedRevision--
		case "actor":
			actor = issuer
		case "body":
			bad.Delivery = &delivery
		case "missing":
			bad.Publication.DeliveryRevision = 999
		}
		if _, err := repo.UpdateConversationDelegation(t.Context(), d.ID, bad, actor); err == nil {
			t.Fatal("invalid publication committed", kind)
		}
		current, err := repo.ConversationDelegation(t.Context(), d.ID, executor)
		if err != nil || conversationHash(current) != accepted {
			t.Fatal("failed publication changed accepted state", kind, err)
		}
	}
	out, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil {
		t.Fatal(err)
	}
	copy := out
	copy.Revision, copy.UpdatedAt = d.Revision, d.UpdatedAt
	if conversationHash(copy) != accepted || out.Revision != d.Revision+1 {
		t.Fatal("publication changed original delivery, verification, decision or task identity", out)
	}
	currentTask, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil || conversationHash(currentTask) != conversationHash(task) {
		t.Fatal("publication changed completed execution task", err)
	}
	for _, before := range history.Items {
		after, err := NewConversationStore(repo.store).ConversationDeliveryPublicationRecord(t.Context(), d.ID, before.Revision, executor)
		if err != nil || conversationHash(after) != conversationHash(before) {
			t.Fatal("publication rewrote immutable history", before.Revision, err)
		}
	}
	audit, err := NewConversationStore(repo.store).ConversationDeliveryPublicationRecord(t.Context(), d.ID, out.Revision, executor)
	if err != nil || audit.Kind != "republish_delivery" || audit.Publication == nil || audit.Publication.Publisher != publisher || audit.Publication.DeliveryRevision != deliveredRevision || audit.Publication.RecordDigest != conversationHash(original) || conversationHash(audit.Delivery) != conversationHash(original.Delivery) || conversationHash(audit.Verification) != conversationHash(original.Verification) {
		t.Fatal("publication audit lost actual actor or selected original", audit, err)
	}
	releases, err := NewConversationStore(repo.store).ConversationSourceReleases(t.Context(), root, issuer)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, release := range releases {
		found = found || release.Publisher != nil && *release.Publisher == publisher && release.Purpose == "delivery" && release.Producer.RoleKey == "old-proof-role"
	}
	if !found {
		t.Fatal("explicit publication did not preserve original proof and current publisher", releases)
	}
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil || conversationHash(replay) != conversationHash(out) {
		t.Fatal("exact retry was not idempotent", replay, err)
	}
	after, err := repo.ConversationDeliveryHistory(t.Context(), d.ID, 0, issuer)
	if err != nil || len(after.Items) != len(history.Items)+1 {
		t.Fatal("retry duplicated publication audit", after, err)
	}
}
