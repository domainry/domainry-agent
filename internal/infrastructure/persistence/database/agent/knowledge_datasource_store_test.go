package agent

import (
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestKnowledgeDatasourceAtomicClaimAndRecovery(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	create := func(client string) agentsdk.KnowledgeLibrary {
		t.Helper()
		lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: client, Kind: "shared", Name: client}, a)
		if err != nil {
			t.Fatal(err)
		}
		return lib
	}
	first, second := create("first"), create("second")
	in := persistence.KnowledgeDatasourceAssignment{DatasourceKey: "approved", SourceID: strings.Repeat("a", 64), AccessPolicySHA256: strings.Repeat("b", 64), ExpectedRevision: first.Revision}
	stale := in
	stale.ExpectedRevision++
	_, err := repo.BindKnowledgeDatasource(ctx, first.ID, stale, a)
	requireConversationCode(t, err, "revision_conflict")
	if used, err := repo.KnowledgeSourceManaged(ctx, in.SourceID); err != nil || used {
		t.Fatal("failed transaction claimed source", err)
	}

	type result struct {
		library agentsdk.KnowledgeLibrary
		err     error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, lib := range []agentsdk.KnowledgeLibrary{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := repo.BindKnowledgeDatasource(ctx, lib.ID, in, a)
			results <- result{got, e}
		}()
	}
	wg.Wait()
	close(results)
	var winner agentsdk.KnowledgeLibrary
	count := 0
	for r := range results {
		if r.err == nil {
			count++
			winner = r.library
		} else {
			requireConversationCode(t, r.err, "document_source_already_bound")
		}
	}
	if count != 1 || winner.Revision != first.Revision+1 {
		t.Fatal("source not claimed exactly once", count, winner.Revision)
	}
	loser := first
	if loser.ID == winner.ID {
		loser = second
	}
	if _, found, e := repo.KnowledgeDatasourceBinding(ctx, documentScopeForLibrary(loser.ID, a)); e != nil || found {
		t.Fatal("losing transaction left binding", e)
	}
	if source, e := repo.KnowledgeDocumentLibrarySource(ctx, loser.ID, a); e != nil || source != "" {
		t.Fatal("losing transaction left source registry", e)
	}

	repo = NewConversationStore(store)
	replayed, err := repo.BindKnowledgeDatasource(ctx, winner.ID, in, a)
	if err != nil || replayed.Revision != winner.Revision {
		t.Fatal("lost response repeated mutation", err)
	}
	saved, found, err := repo.KnowledgeDatasourceBinding(ctx, documentScopeForLibrary(winner.ID, a))
	if err != nil || !found || saved.CreatedBy != a.UserID || saved.AccessPolicySHA256 != in.AccessPolicySHA256 || saved.CreatedAt.IsZero() {
		t.Fatal("binding provenance not persisted", err)
	}
	for _, changed := range []persistence.KnowledgeDatasourceAssignment{
		{DatasourceKey: "renamed", SourceID: in.SourceID, AccessPolicySHA256: in.AccessPolicySHA256, ExpectedRevision: winner.Revision},
		{DatasourceKey: in.DatasourceKey, SourceID: strings.Repeat("c", 64), AccessPolicySHA256: in.AccessPolicySHA256, ExpectedRevision: winner.Revision},
		{DatasourceKey: in.DatasourceKey, SourceID: in.SourceID, AccessPolicySHA256: strings.Repeat("c", 64), ExpectedRevision: winner.Revision},
	} {
		_, e := repo.BindKnowledgeDatasource(ctx, winner.ID, changed, a)
		requireConversationCode(t, e, "datasource_already_bound")
	}

	other := a
	other.UserID = "reader"
	for _, role := range []string{"reader", "editor", "manager"} {
		winner, err = repo.SetKnowledgeLibraryMember(ctx, winner.ID, other.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: role, ExpectedRevision: winner.Revision}, a)
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.BindKnowledgeDatasource(ctx, winner.ID, in, other)
		if role != "manager" {
			requireConversationCode(t, err, "library_manage_required")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	winner, err = repo.RemoveKnowledgeLibraryMember(ctx, winner.ID, other.UserID, winner.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.BindKnowledgeDatasource(ctx, winner.ID, in, other)
	requireConversationCode(t, err, "library_not_found")
	// A worker can still resolve the frozen source to clean up after member removal.
	if _, found, err := repo.KnowledgeDatasourceBinding(ctx, documentScopeForLibrary(winner.ID, other)); err != nil || !found {
		t.Fatal("worker lost persisted binding", err)
	}
	other = a
	other.WorkspaceID = "foreign"
	if _, found, err := repo.KnowledgeDatasourceBinding(ctx, documentScopeForLibrary(winner.ID, other)); err != nil || found {
		t.Fatal("binding escaped workspace", err)
	}
	winner, err = repo.UpdateKnowledgeLibrary(ctx, winner.ID, agentsdk.KnowledgeLibraryUpdate{Name: winner.Name, Archived: true, ExpectedRevision: winner.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.BindKnowledgeDatasource(ctx, winner.ID, in, a)
	requireConversationCode(t, err, "library_archived")
	_, err = repo.BindKnowledgeDatasource(ctx, loser.ID, in, a)
	requireConversationCode(t, err, "document_source_already_bound")
}
