package agent

import (
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestKnowledgeDocumentLeaseTakeoverNeverRepeatsPutOrRevivesDeletion(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a, ctx := conversationTestAuthority(), t.Context()
	lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "library", Kind: "shared", Name: "Shared"}, a)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("a", 64)
	if err = repo.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}, source); err != nil {
		t.Fatal(err)
	}
	input := persistence.KnowledgeDocumentReserve{LibraryID: lib.ID, ClientID: "file", Filename: "a.txt", ContentType: "text/plain", SHA256: strings.Repeat("b", 64), Bytes: 1, SourceID: source, AccessPolicySHA256: strings.Repeat("c", 64)}
	r, err := repo.ReserveKnowledgeDocument(ctx, input, a)
	if err != nil {
		t.Fatal(err)
	}
	requestID := r.PutRequestID
	if requestID == "" || r.AccessPolicySHA256 != input.AccessPolicySHA256 {
		t.Fatal("reservation omitted stable command/policy")
	}
	changed := input
	changed.AccessPolicySHA256 = strings.Repeat("d", 64)
	_, err = repo.ReserveKnowledgeDocument(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	r, err = repo.CommitKnowledgeDocumentContent(ctx, r.Document.ID, r.Document.Revision, "private-ref", a)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old, found, err := repo.ClaimKnowledgeDocumentWork(ctx, a.RuntimeID, "first", now, time.Minute)
	if err != nil || !found {
		t.Fatal("claim", err)
	}
	r, started, err := repo.StartKnowledgeDocumentPut(ctx, old)
	if err != nil || !started {
		t.Fatal("first put", err)
	}
	// Simulate process loss after the durable marker and before a response.
	restarted := NewConversationStore(store)
	newLease, found, err := restarted.ClaimKnowledgeDocumentWork(ctx, a.RuntimeID, "second", now.Add(2*time.Minute), time.Minute)
	if err != nil || !found {
		t.Fatal("takeover", err)
	}
	after, started, err := restarted.StartKnowledgeDocumentPut(ctx, newLease)
	if err != nil || started {
		t.Fatal("uncertain upload repeated", err)
	}
	if after.PutRequestID != requestID || after.AccessPolicySHA256 != input.AccessPolicySHA256 {
		t.Fatal("takeover replaced logical command/policy")
	}
	err = restarted.ApplyKnowledgeDocumentProgress(ctx, old, persistence.KnowledgeDocumentProgress{Event: "indexed", IndexStatus: "INDEXED"})
	requireConversationCode(t, err, "document_lease_lost")
	r, err = restarted.RequestKnowledgeDocumentDeletion(ctx, r.Document.ID, r.Document.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	// A late successful index observation confirms cleanup eligibility only.
	err = restarted.ApplyKnowledgeDocumentProgress(ctx, newLease, persistence.KnowledgeDocumentProgress{Event: "indexed", IndexStatus: "INDEXED", RetryAt: now})
	if err != nil {
		t.Fatal(err)
	}
	r, err = restarted.KnowledgeDocumentRecord(ctx, r.Document.ID, a)
	if err != nil || r.Document.State != "deleting" || !r.IndexObserved {
		t.Fatal("late index revived deletion", r, err)
	}
	lease, found, err := restarted.ClaimKnowledgeDocumentWork(ctx, a.RuntimeID, "third", now.Add(3*time.Minute), time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	err = restarted.ApplyKnowledgeDocumentProgress(ctx, lease, persistence.KnowledgeDocumentProgress{Event: "deleted"})
	requireConversationCode(t, err, "document_cleanup_unconfirmed")
	if err = restarted.StartKnowledgeDocumentDelete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	err = restarted.ApplyKnowledgeDocumentProgress(ctx, lease, persistence.KnowledgeDocumentProgress{Event: "deleted"})
	requireConversationCode(t, err, "document_cleanup_unconfirmed")
	if err = restarted.ApplyKnowledgeDocumentProgress(ctx, lease, persistence.KnowledgeDocumentProgress{Event: "delete_acknowledged", RetryAt: now}); err != nil {
		t.Fatal(err)
	}
	lease, found, err = restarted.ClaimKnowledgeDocumentWork(ctx, a.RuntimeID, "cleanup", now.Add(4*time.Minute), time.Minute)
	if err != nil || !found {
		t.Fatal("acknowledged cleanup claim", err)
	}
	if err = restarted.ApplyKnowledgeDocumentProgress(ctx, lease, persistence.KnowledgeDocumentProgress{Event: "deleted"}); err != nil {
		t.Fatal(err)
	}
	r, err = restarted.KnowledgeDocumentRecord(ctx, r.Document.ID, a)
	if err != nil || r.Document.State != "deleted" || r.BodyRef != "" {
		t.Fatal(r, err)
	}
	managed, err := restarted.KnowledgeSourceManaged(ctx, source)
	if err != nil || !managed {
		t.Fatal("deletion erased mandatory filtering marker", err)
	}
	other, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "other", Kind: "shared", Name: "Other"}, a)
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: other.ID}, source)
	requireConversationCode(t, err, "document_source_already_bound")
}

func TestKnowledgeDocumentMembershipCheckedBeforeQueuedUpload(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a, ctx := conversationTestAuthority(), t.Context()
	editor := a
	editor.UserID = "editor"
	lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "library", Kind: "shared", Name: "Shared"}, a)
	if err != nil {
		t.Fatal(err)
	}
	lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, editor.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "editor", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("a", 64)
	if err = repo.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}, source); err != nil {
		t.Fatal(err)
	}
	r, err := repo.ReserveKnowledgeDocument(ctx, persistence.KnowledgeDocumentReserve{LibraryID: lib.ID, ClientID: "file", Filename: "a.txt", ContentType: "text/plain", SHA256: strings.Repeat("b", 64), Bytes: 1, SourceID: source}, editor)
	if err != nil {
		t.Fatal(err)
	}
	r, err = repo.CommitKnowledgeDocumentContent(ctx, r.Document.ID, r.Document.Revision, "ref", editor)
	if err != nil {
		t.Fatal(err)
	}
	lib, err = repo.RemoveKnowledgeLibraryMember(ctx, lib.ID, editor.UserID, lib.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	lease, found, err := repo.ClaimKnowledgeDocumentWork(ctx, a.RuntimeID, "worker", time.Now().UTC(), time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	_, _, err = repo.StartKnowledgeDocumentPut(ctx, lease)
	requireConversationCode(t, err, "library_not_found")
	r, err = repo.KnowledgeDocumentRecord(ctx, r.Document.ID, a)
	if err != nil || r.PutStarted {
		t.Fatal("revoked editor published", err)
	}
	r, err = repo.RequestKnowledgeDocumentDeletion(ctx, r.Document.ID, r.Document.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	// Trusted cleanup of an already authorized deletion survives initiator removal.
	if err = repo.ApplyKnowledgeDocumentProgress(ctx, lease, persistence.KnowledgeDocumentProgress{Event: "deleted"}); err != nil {
		t.Fatal(err)
	}
}
