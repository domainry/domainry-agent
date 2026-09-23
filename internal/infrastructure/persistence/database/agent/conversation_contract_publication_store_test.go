package agent

import (
	"database/sql"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func contractPublicationFixture(t *testing.T) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationAuthority, sdk.ConversationDelegation, sdk.ConversationRunReference) {
	t.Helper()
	repo, issuer, executor, in := delegationSubjectFixture(t)
	original := issuer
	original.RoleKey = "old-contract-proof"
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "contract-proof"}, original)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "contract-proof", Message: "Original input"}, original)
	if err != nil {
		t.Fatal(err)
	}
	root := sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}
	in.Request.Requirements = sdk.ConversationAgentRequirements{TaskType: "original-review", Sources: []sdk.ConversationRunReference{root}}
	in.Task.Requirements = in.Request.Requirements
	d, err := repo.CreateConversationDelegation(t.Context(), in, issuer)
	if err != nil {
		t.Fatal(err)
	}
	return repo, issuer, executor, d, root
}

func TestContractPublicationPreservesOriginalAndBindsActualPublisherWithExactRetry(t *testing.T) {
	repo, issuer, executor, d, root := contractPublicationFixture(t)
	original, err := repo.ConversationContractPublicationRecord(t.Context(), d.ID, 0, issuer)
	if err != nil || original.Agreement.Requirements == nil || len(original.Requirements.Sources) != 1 || original.Requirements.Sources[0] != root {
		t.Fatal("missing original requirements snapshot", original, err)
	}
	task, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil {
		t.Fatal(err)
	}
	// Closed relationships may republish without altering the original result.
	d.Status, d.Decision = "accepted_delivery", "Original accepted decision"
	if err = repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.saveConversationDelegation(t.Context(), tx, d, d.Revision, issuer) }); err != nil {
		t.Fatal(err)
	}
	publisher := issuer
	publisher.RoleKey = "actual-contract-publisher"
	in := sdk.ConversationDelegationUpdate{ClientID: "explicit-original-contract", ExpectedRevision: d.Revision, Action: "republish_contract", Reason: "Share exact original", ContractPublication: &sdk.ConversationContractPublication{AgreementRevision: 1, RecordDigest: conversationHash(original)}}
	for _, kind := range []string{"digest", "revision", "actor", "mixed"} {
		bad := in
		selection := *in.ContractPublication
		bad.ContractPublication = &selection
		actor := publisher
		switch kind {
		case "digest":
			selection.RecordDigest = strings.Repeat("0", 64)
		case "revision":
			bad.ExpectedRevision++
		case "actor":
			actor = executor
		case "mixed":
			bad.Publication = &sdk.ConversationDeliveryPublication{}
		}
		if _, e := repo.UpdateConversationDelegation(t.Context(), d.ID, bad, actor); e == nil {
			t.Fatal("invalid publication committed", kind)
		}
	}
	out, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil || out.Revision != d.Revision+1 {
		t.Fatal(out, err)
	}
	before, after := d, out
	before.Revision = after.Revision
	before.UpdatedAt = after.UpdatedAt
	if conversationHash(before) != conversationHash(after) {
		t.Fatal("publication changed original relationship", before, after)
	}
	currentTask, err := repo.ConversationTask(t.Context(), d.TaskID, executor)
	if err != nil || conversationHash(task) != conversationHash(currentTask) {
		t.Fatal("publication changed execution task", err)
	}
	stored, err := repo.ConversationContractPublicationRecord(t.Context(), d.ID, 1, publisher)
	if err != nil || conversationHash(stored) != conversationHash(original) {
		t.Fatal("publication rewrote immutable agreement", err)
	}
	replay, err := repo.UpdateConversationDelegation(t.Context(), d.ID, in, publisher)
	if err != nil || conversationHash(replay) != conversationHash(out) {
		t.Fatal("exact retry lost committed receipt", err)
	}
	history, err := NewConversationStore(repo.store).ConversationContractPublicationHistory(t.Context(), d.ID, 0, executor)
	if err != nil || len(history.Items) != 1 || history.Items[0].Publisher != publisher || history.Items[0].RecipientUserID != executor.UserID || history.Items[0].Reason != in.Reason {
		t.Fatal("incorrect publication audit", history, err)
	}
	releases, err := repo.ConversationSourceReleases(t.Context(), root, executor)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, release := range releases {
		found = found || release.Publisher != nil && *release.Publisher == publisher && release.Producer.RoleKey == "old-contract-proof" && release.Purpose == "contract"
	}
	if !found {
		t.Fatal("original proof replaced by current publication role", releases)
	}
}
