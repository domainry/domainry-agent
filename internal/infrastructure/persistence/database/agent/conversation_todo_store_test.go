package agent

import (
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func todoString(s string) *string { return &s }

func TestTodoBatchAtomicityReceiptsAndIndependentLifecycle(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	input := agentsdk.ConversationTodoCreate{ClientID: "batch", SourceConversationID: c.ID, Items: []agentsdk.ConversationTodoInput{{Title: "发布公告", Timezone: "Asia/Shanghai"}, {Title: "更新文档", DueDate: "2026-02-30", Timezone: "Asia/Shanghai"}, {Title: "通知支持团队", Timezone: "Asia/Shanghai"}}}
	if _, err = repo.CreateTodos(ctx, input, a); err == nil {
		t.Fatal("invalid batch accepted")
	}
	page, _ := repo.Todos(ctx, agentsdk.ConversationTodoQuery{}, a)
	if len(page.Items) != 0 {
		t.Fatal("partial invalid batch saved")
	}
	input.Items[1].DueDate = "2026-09-11"
	batch, err := repo.CreateTodos(ctx, input, a)
	if err != nil || len(batch.Items) != 3 {
		t.Fatal(err)
	}
	for index, item := range batch.Items {
		if item.Position != index+1 || item.BatchID != batch.BatchID || item.SourceConversationID != c.ID || item.Revision != 1 {
			t.Fatal("lost batch order/provenance")
		}
	}
	replay, err := repo.CreateTodos(ctx, input, a)
	if err != nil || conversationHash(batch) != conversationHash(replay) {
		t.Fatal("duplicate batch", err)
	}
	changed := input
	changed.Items = append([]agentsdk.ConversationTodoInput(nil), input.Items...)
	changed.Items[0].Title = "changed"
	_, err = repo.CreateTodos(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	if err = repo.Delete(ctx, c.ID, c.Revision, a); err != nil {
		t.Fatal(err)
	}
	replay, err = repo.CreateTodos(ctx, input, a)
	if err != nil || conversationHash(batch) != conversationHash(replay) {
		t.Fatal("conversation deletion lost personal receipt", err)
	}
	page, err = repo.Todos(ctx, agentsdk.ConversationTodoQuery{BatchID: batch.BatchID}, a)
	if err != nil || len(page.Items) != 3 {
		t.Fatal("conversation deletion removed independent todos", err)
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}} {
		if _, err = repo.Todo(ctx, batch.Items[0].ID, other); err == nil {
			t.Fatal("cross-owner read")
		}
		if err = repo.DeleteTodo(ctx, batch.Items[0].ID, agentsdk.ConversationTodoDelete{ClientID: "cross", ExpectedRevision: 1}, other); err == nil {
			t.Fatal("cross-owner delete")
		}
		page, err = repo.Todos(ctx, agentsdk.ConversationTodoQuery{}, other)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("cross-owner list")
		}
	}
}

func TestTodoOriginalPositionsRevisionDateAndCompletion(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	batch, err := repo.CreateTodos(ctx, agentsdk.ConversationTodoCreate{ClientID: "batch", Items: []agentsdk.ConversationTodoInput{{Title: "first", Timezone: "UTC"}, {Title: "second", Timezone: "America/New_York", DueAt: "2026-09-10T09:00:00-04:00"}, {Title: "third", Timezone: "UTC"}}}, a)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := repo.UpdateTodo(ctx, batch.Items[0].ID, agentsdk.ConversationTodoUpdate{ClientID: "complete", ExpectedRevision: 1, Patch: agentsdk.ConversationTodoPatch{Status: todoString("completed")}}, a)
	if err != nil || completed.CompletedAt == nil || completed.Status != "completed" {
		t.Fatal("completion", err)
	}
	page, err := repo.Todos(ctx, agentsdk.ConversationTodoQuery{Status: "open", BatchID: batch.BatchID}, a)
	if err != nil || len(page.Items) != 2 || page.Items[0].Position != 2 {
		t.Fatal("filter renumbered batch", err)
	}
	update := agentsdk.ConversationTodoUpdate{ClientID: "date", ExpectedRevision: 1, Patch: agentsdk.ConversationTodoPatch{DueDate: todoString("2026-09-11"), DueAt: todoString("")}}
	second, err := repo.UpdateTodo(ctx, batch.Items[1].ID, update, a)
	if err != nil || second.DueDate != "2026-09-11" || second.DueAt != "" || second.Position != 2 {
		t.Fatal("second item date", err)
	}
	duplicate, err := repo.UpdateTodo(ctx, second.ID, update, a)
	if err != nil || duplicate.Revision != 2 {
		t.Fatal("repeated update", err)
	}
	update.ClientID = "stale"
	_, err = repo.UpdateTodo(ctx, second.ID, update, a)
	requireConversationCode(t, err, "revision_conflict")
	reopened, err := repo.UpdateTodo(ctx, completed.ID, agentsdk.ConversationTodoUpdate{ClientID: "reopen", ExpectedRevision: 2, Patch: agentsdk.ConversationTodoPatch{Status: todoString("open")}}, a)
	if err != nil || reopened.CompletedAt != nil || reopened.Status != "open" {
		t.Fatal("reopen", err)
	}
	del := agentsdk.ConversationTodoDelete{ClientID: "delete", ExpectedRevision: 3}
	if err = repo.DeleteTodo(ctx, reopened.ID, del, a); err != nil {
		t.Fatal(err)
	}
	if err = repo.DeleteTodo(ctx, reopened.ID, del, a); err != nil {
		t.Fatal("duplicate delete", err)
	}
	page, err = repo.Todos(ctx, agentsdk.ConversationTodoQuery{BatchID: batch.BatchID}, a)
	if err != nil || len(page.Items) != 2 || page.Items[0].Position != 2 || page.Items[1].Position != 3 {
		t.Fatal("delete renumbered remaining items", err)
	}
}

func TestTodoDeadlinesRejectAmbiguousOrMismatchedInputs(t *testing.T) {
	for _, value := range []struct {
		date, at, zone string
		valid          bool
	}{
		{"2028-02-29", "", "Asia/Shanghai", true}, {"2026-02-29", "", "UTC", false},
		{"", "2026-03-08T02:30:00-05:00", "America/New_York", false},
		{"", "2026-11-01T01:30:00-04:00", "America/New_York", true},
		{"", "2026-11-01T01:30:00-05:00", "America/New_York", true},
		{"2026-09-11", "2026-09-11T12:00:00Z", "UTC", false},
		{"", "2026-09-11T12:00:00", "UTC", false}, {"2026-09-11", "", "Local", false},
	} {
		err := validTodo(agentsdk.ConversationTodoInput{Title: "deadline", DueDate: value.date, DueAt: value.at, Timezone: value.zone})
		if (err == nil) != value.valid {
			t.Errorf("deadline %+v: %v", value, err)
		}
	}
}

func TestTodoPaginationIsBoundedCompleteAndOwnerBound(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	for batch := 0; batch < 2; batch++ {
		items := []agentsdk.ConversationTodoInput{}
		for i := 0; i < 20; i++ {
			items = append(items, agentsdk.ConversationTodoInput{Title: fmt.Sprintf("literal %%_ item %d-%d", batch, i), Description: strings.Repeat("\x01", 1024), Timezone: "UTC"})
		}
		if _, err := repo.CreateTodos(ctx, agentsdk.ConversationTodoCreate{ClientID: fmt.Sprint("batch", batch), Items: items}, a); err != nil {
			t.Fatal(err)
		}
	}
	query := agentsdk.ConversationTodoQuery{Query: "%_", Limit: 20}
	seen := map[string]bool{}
	firstCursor := ""
	for round := 0; round < 50; round++ {
		page, err := repo.Todos(ctx, query, a)
		if err != nil {
			t.Fatal(err)
		}
		if len(conversationJSON(page)) > 32768 {
			t.Fatal("result escaped beyond tool budget")
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate item across pages")
			}
			seen[item.ID] = true
		}
		if page.Complete {
			break
		}
		if page.NextCursor == "" || page.NextCursor == query.Cursor {
			t.Fatal("cursor did not advance")
		}
		query.Cursor = page.NextCursor
		if firstCursor == "" {
			firstCursor = query.Cursor
		}
	}
	if len(seen) != 40 || firstCursor == "" {
		t.Fatalf("read %d of 40", len(seen))
	}
	query.Cursor = firstCursor
	other := a
	other.UserID = "other"
	_, err := repo.Todos(ctx, query, other)
	requireConversationCode(t, err, "todo_cursor_invalid")
	query.Query = "different"
	_, err = repo.Todos(ctx, query, a)
	requireConversationCode(t, err, "todo_cursor_invalid")
}
