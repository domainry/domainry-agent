package agent

import (
	"fmt"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestKnowledgeLibraryMembershipIsolationAndConcurrentManagers(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	b := a
	b.UserID = "second"
	ctx := t.Context()
	in := agentsdk.KnowledgeLibraryCreate{ClientID: "shared", Kind: "shared", Name: "项目资料", Description: "跨部门协作"}
	lib, err := repo.CreateKnowledgeLibrary(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.CreateKnowledgeLibrary(ctx, in, a)
	if err != nil || replay.ID != lib.ID {
		t.Fatal("duplicate create", err)
	}
	changed := in
	changed.Name = "changed"
	_, err = repo.CreateKnowledgeLibrary(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	for _, other := range []agentsdk.ConversationAuthority{b, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}} {
		_, err = repo.KnowledgeLibrary(ctx, lib.ID, other)
		requireConversationCode(t, err, "library_not_found")
		page, err := repo.KnowledgeLibraries(ctx, "", 10, other)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("cross-user listing", page, err)
		}
	}
	lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	read, err := repo.KnowledgeLibrary(ctx, lib.ID, b)
	if err != nil || read.Role != "reader" {
		t.Fatal(read, err)
	}
	_, err = repo.UpdateKnowledgeLibrary(ctx, lib.ID, agentsdk.KnowledgeLibraryUpdate{ExpectedRevision: lib.Revision, Name: "forged"}, b)
	requireConversationCode(t, err, "library_manage_required")
	_, err = repo.KnowledgeLibraryMembers(ctx, lib.ID, "", 20, b)
	requireConversationCode(t, err, "library_manage_required")
	lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "editor", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.RemoveKnowledgeLibraryMember(ctx, lib.ID, a.UserID, lib.Revision, b)
	requireConversationCode(t, err, "library_manage_required")
	_, err = repo.RemoveKnowledgeLibraryMember(ctx, lib.ID, a.UserID, lib.Revision, a)
	requireConversationCode(t, err, "library_last_manager")
	_, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "manager", ExpectedRevision: 1}, a)
	requireConversationCode(t, err, "revision_conflict")
	lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "manager", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, actor := range []agentsdk.ConversationAuthority{a, b} {
		wg.Add(1)
		go func(actor agentsdk.ConversationAuthority) {
			defer wg.Done()
			_, e := repo.RemoveKnowledgeLibraryMember(ctx, lib.ID, actor.UserID, lib.Revision, actor)
			results <- e
		}(actor)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one serial removal, got %d", success)
	}
	var remaining agentsdk.ConversationAuthority
	for _, actor := range []agentsdk.ConversationAuthority{a, b} {
		if current, e := repo.KnowledgeLibrary(ctx, lib.ID, actor); e == nil {
			remaining = actor
			lib = current
		}
	}
	if remaining.UserID == "" {
		t.Fatal("library lost all managers")
	}
	_, err = repo.RemoveKnowledgeLibraryMember(ctx, lib.ID, remaining.UserID, lib.Revision, remaining)
	requireConversationCode(t, err, "library_last_manager")
	removed := a
	if remaining.UserID == a.UserID {
		removed = b
	}
	page, err := repo.KnowledgeLibraries(ctx, "", 20, removed)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("removed member retained access", page, err)
	}
	_, err = repo.CreateKnowledgeLibrary(ctx, in, removed)
	if removed.UserID == a.UserID {
		requireConversationCode(t, err, "library_not_found")
	}
}

func TestKnowledgeLibraryPersonalAndPagination(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	in := agentsdk.KnowledgeLibraryCreate{ClientID: "personal1", Kind: "personal", Name: "个人资料"}
	personal, err := repo.CreateKnowledgeLibrary(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	in.ClientID = "personal2"
	replay, err := repo.CreateKnowledgeLibrary(ctx, in, a)
	if err != nil || replay.ID != personal.ID {
		t.Fatal("more than one personal library", err)
	}
	_, err = repo.SetKnowledgeLibraryMember(ctx, personal.ID, "second", agentsdk.KnowledgeLibraryMemberWrite{Role: "reader", ExpectedRevision: personal.Revision}, a)
	requireConversationCode(t, err, "personal_library_private")
	for i := 0; i < 3; i++ {
		_, err = repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: fmt.Sprint("lib", i), Kind: "shared", Name: fmt.Sprint("Library", i)}, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	after := ""
	for i := 0; i < 10; i++ {
		page, e := repo.KnowledgeLibraries(ctx, after, 1, a)
		if e != nil {
			t.Fatal(e)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate page")
			}
			seen[item.ID] = true
		}
		if page.Complete {
			break
		}
		after = page.NextAfter
	}
	if len(seen) != 4 {
		t.Fatalf("lost libraries: %v", seen)
	}
	lib, err := repo.UpdateKnowledgeLibrary(ctx, personal.ID, agentsdk.KnowledgeLibraryUpdate{ExpectedRevision: personal.Revision, Name: "个人资料（归档）", Archived: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	repo = NewConversationStore(store)
	got, err := repo.KnowledgeLibrary(ctx, lib.ID, a)
	if err != nil || !got.Archived || got.Revision != lib.Revision {
		t.Fatal("reopen lost settings", got, err)
	}
}
