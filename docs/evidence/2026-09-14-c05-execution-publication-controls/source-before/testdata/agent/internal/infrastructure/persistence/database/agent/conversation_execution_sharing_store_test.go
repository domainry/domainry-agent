package agent

import (
	"database/sql"
	sdk "github.com/domainry/domainry-agent-sdk"
	"testing"
	"time"
)

func TestExecutionPublicationFromStoppedAgentCannotPublishOrWithdraw(t *testing.T) {
	repo, issuer, executor, _, d, ref := participantDeliveryStoreFixture(t, false)
	in := sdk.ConversationExecutionShare{Reference: sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID}, ExpectedRevision: d.Revision, ClientID: "valid-execution-publication", Reason: "explicit actual owner publication"}
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, executor); err != nil {
		t.Fatal(err)
	}
	d, err := repo.ConversationDelegation(t.Context(), d.ID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	definition := sdk.ConversationToolDefinition{}
	for _, tool := range sdk.ConversationCollaborationTools() {
		if tool.Key == "delegation_execution_publish" {
			definition = tool
		}
	}
	// The source run is already stopped/completed. A delayed Agent call must
	// fail before idempotency replay or modifying any current reader proofs.
	in.ToolRequest = &sdk.ConversationToolRequest{Authority: executor, ConversationID: ref.ConversationID, RunID: ref.RunID, LeaseOwner: "stopped-worker", Fence: 1, IdempotencyKey: "old-call", Call: sdk.ConversationToolCall{ID: "late-share", Name: definition.Key, Arguments: "{}"}, Definition: definition}
	for _, withdraw := range []bool{false, true} {
		in.Withdraw, in.ExpectedRevision, in.ClientID = withdraw, d.Revision, "stopped-share"
		if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, executor); err == nil {
			t.Fatal("stopped execution changed publication", withdraw)
		}
	}
	current, err := repo.ConversationDelegation(t.Context(), d.ID, issuer)
	if err != nil || current.Revision != d.Revision {
		t.Fatal("stopped execution mutated delegation", current.Revision, err)
	}
	proofs, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, issuer)
	if err != nil || len(proofs) != 1 {
		t.Fatal("stopped execution removed current publication", proofs, err)
	}
}

func TestExecutionSharingKeepsActualPublisherAndProviderAndWithdrawsOnlyExecution(t *testing.T) {
	repo, issuer, publisher, reader, d, ref := participantDeliveryStoreFixture(t, false)
	ref.BeforeStep = 0
	in := sdk.ConversationExecutionShare{ClientID: "actual-executor-publication", ExpectedRevision: d.Revision, Reference: ref, Reason: "明确共享这次执行过程"}
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, issuer); err == nil {
		t.Fatal("issuer published another user's private run")
	}
	publication, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, publisher)
	if err != nil || publication.Publisher != publisher || publication.Reference != ref {
		t.Fatal(publication, err)
	}
	if replay, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, publisher); err != nil || replay != publication {
		t.Fatal("publication replay changed", replay, err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "execution_read"}}}
	d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "late-execution-reader", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := NewConversationStore(repo.store).ConversationDelegationExecutions(t.Context(), d.ID, reader)
	if err != nil || len(releases) != 1 || releases[0].Publisher == nil || *releases[0].Publisher != publisher || releases[0].Producer.UserID != publisher.UserID || releases[0].Producer.RoleKey != "reviewer-role" || releases[0].Reference != ref {
		t.Fatal("publication lost original provider/actual publisher", releases, err)
	}
	if _, err := repo.Run(t.Context(), ref.ConversationID, ref.RunID, reader); err == nil {
		t.Fatal("sharing opened private run port")
	}
	withdraw := in
	withdraw.ClientID = "withdraw-execution-publication"
	withdraw.ExpectedRevision = d.Revision
	withdraw.Withdraw = true
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, withdraw, publisher); err != nil {
		t.Fatal(err)
	}
	if releases, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, reader); err != nil || len(releases) != 0 {
		t.Fatal("withdrawn execution retained", releases, err)
	}
	ref.BeforeStep = 2
	if releases, err := repo.ConversationSourceReleases(t.Context(), ref, issuer); err != nil || len(releases) != 1 || releases[0].Purpose != "delivery" {
		t.Fatal("execution withdrawal changed original delivery", releases, err)
	}
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, publisher); err != nil {
		t.Fatal("original publication replay failed", err)
	}
	if releases, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, reader); err != nil || len(releases) != 0 {
		t.Fatal("old publication replay restored withdrawn rights", releases, err)
	}
}

func TestExecutionSharingLivePublicationKeepsExactOriginalRunAsLedgerAdvances(t *testing.T) {
	repo, issuer, executor, admission := delegationSubjectFixture(t)
	d, err := repo.CreateConversationDelegation(t.Context(), admission, issuer)
	if err != nil {
		t.Fatal(err)
	}
	reader := issuer
	reader.UserID, reader.RoleKey = "live-execution-reader", "read-role"
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "execution_read"}}}
	d, err = repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "live-reader-grant", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.LaunchConversationTask(t.Context(), issuer.RuntimeID); err != nil || !found {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), issuer.RuntimeID, "live-shared-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, issuer, d.ID)
	ref := sdk.ConversationRunReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID}
	publication, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, sdk.ConversationExecutionShare{ClientID: "share-live-run", ExpectedRevision: d.Revision, Reference: ref, Reason: "明确共享运行中的任务"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	input := executionStoreInput()
	if _, _, err := repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := sdk.ConversationToolCall{ID: "live-call", Name: "create_thing", Arguments: `{"name":"requested"}`}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{call}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, sdk.ConversationToolResult{Status: "completed", Content: []byte(`{"id":"live-original"}`)}); err != nil {
		t.Fatal(err)
	}
	releases, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, reader)
	if err != nil || len(releases) != 1 || releases[0].Reference != ref || releases[0].Publisher == nil || *releases[0].Publisher != executor {
		t.Fatal("live ledger replaced publication", releases, err)
	}
	if run, err := repo.Run(t.Context(), ref.ConversationID, ref.RunID, executor); err != nil || run.Status != "running" || len(run.Steps) != 1 || len(run.Steps[0].Calls) != 1 {
		t.Fatal("live step not retained", run, err)
	}
	// Relaying an owned publication under a later role keeps the real publisher.
	later := executor
	later.RoleKey = "later-reader-grant-role"
	err = repo.transaction(t.Context(), func(tx *sql.Tx) error {
		return repo.shareParticipantPublishedRoots(t.Context(), tx, d, "execution", later, reader, []sdk.ConversationRunReference{ref})
	})
	if err != nil {
		t.Fatal(err)
	}
	if releases, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, reader); err != nil || len(releases) != 1 || *releases[0].Publisher != publication.Publisher {
		t.Fatal("owned relay manufactured later publisher", releases, err)
	}
}

func TestExecutionSharingPublishesToStandingExecutionReadersAndRejectsUnrelatedRun(t *testing.T) {
	repo, issuer, publisher, reader, d, ref := participantDeliveryStoreFixture(t, false)
	grants := []sdk.ConversationDelegationParticipantInput{{UserID: reader.UserID, Operations: []string{"view", "execution_read"}}}
	d, err := repo.SetConversationDelegationParticipants(t.Context(), d.ID, sdk.ConversationDelegationUpdate{ClientID: "standing-execution-reader", ExpectedRevision: d.Revision, Action: "set_participants", Participants: &grants}, issuer)
	if err != nil {
		t.Fatal(err)
	}
	ref.BeforeStep = 0
	in := sdk.ConversationExecutionShare{ClientID: "standing-execution-publication", ExpectedRevision: d.Revision, Reference: ref, Reason: "明确共享执行过程"}
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, publisher); err != nil {
		t.Fatal(err)
	}
	if releases, err := repo.ConversationDelegationExecutions(t.Context(), d.ID, reader); err != nil || len(releases) != 1 {
		t.Fatal("standing reader missed publication", releases, err)
	}
	in.ClientID = "unrelated-private-run"
	in.ExpectedRevision++
	in.Reference.ConversationID = d.SourceConversationID
	in.Reference.RunID = d.SourceRunID
	if _, err := repo.PublishConversationDelegationExecution(t.Context(), d.ID, in, issuer); err == nil {
		t.Fatal("private source conversation was published as delegated execution")
	}
}
