package webhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	"github.com/domainry/domainry-agent/testsupport/databasetest"
	knowledge "github.com/domainry/domainry-knowledge/module"
	"github.com/domainry/domainry-orm/query"
	todocontract "github.com/domainry/domainry-todo/contract"
	todo "github.com/domainry/domainry-todo/module"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

func TestExtractedPersistenceOnAllDatabases(t *testing.T) {
	databasetest.ForEach(t, func(t *testing.T, options databasetest.Options) {
		c, profile, err := persistence.OpenConnection(t.Context(), options.Driver, persistence.ConnectionOptions{Path: options.Path, DSN: options.DSN, Schema: options.Schema})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		d, err := persistence.Renderer(c.Driver, c.Schema)
		if err != nil {
			t.Fatal(err)
		}
		registrar := &webhost.Registrar{DB: c.DB, Renderer: d, DatabaseDriver: c.Driver, Namespace: c.Schema, Profile: profile}
		if err = registrar.Prepare(t.Context()); err != nil {
			t.Fatal(err)
		}
		backend := knowledge.SQLBackend{DB: c.DB, Dialect: d, Engine: profile}
		records, err := knowledge.NewRecordStore(t.Context(), backend, registrar, "pm")
		if err != nil {
			t.Fatal(err)
		}
		km, err := knowledge.LegacyMigrations(d)
		if err != nil {
			t.Fatal(err)
		}
		tm, err := todo.LegacyMigration(d)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err = registrar.ApplyOwnedMigrations(t.Context(), "knowledge", km); err != nil {
				t.Fatal(err)
			}
			if err = registrar.ApplyOwnedMigrations(t.Context(), "todo", []modulehost.SchemaMigration{tm}); err != nil {
				t.Fatal(err)
			}
		}
		// The modules can own their tables without any Agent execution tables.
		a := toolsdk.Authority{Known: true, RuntimeID: "standalone", WorkspaceID: "office", UserID: "one"}
		todoStore, err := todo.NewStore(c.DB, d, profile, nil)
		if err != nil {
			t.Fatal(err)
		}
		mutation := todo.Mutation{Key: "create", Operation: "todo_create", Data: json.RawMessage(`{"items":[{"title":"跟进记录","timezone":"Asia/Shanghai"}]}`)}
		first, err := todoStore.ApplyMutation(t.Context(), mutation, a)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := todoStore.ApplyMutation(t.Context(), mutation, a)
		if err != nil || string(first.Content) != string(replay.Content) {
			t.Fatalf("todo receipt: %v", err)
		}
		page, err := todoStore.Todos(t.Context(), todocontract.TodoQuery{}, a)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("todo persistence: %+v %v", page, err)
		}
		libraryStore := knowledge.NewStore(backend, nil)
		library, err := libraryStore.CreateKnowledgeLibrary(t.Context(), agentsdk.KnowledgeLibraryCreate{ClientID: "one", Kind: "personal", Name: "知识库"}, a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = libraryStore.KnowledgeLibrary(t.Context(), library.ID, a); err != nil {
			t.Fatal(err)
		}
		other := a
		other.UserID = "two"
		if _, err = libraryStore.KnowledgeLibrary(t.Context(), library.ID, other); err == nil {
			t.Fatal("knowledge scope escaped")
		}
		if _, err = todoStore.Todo(t.Context(), page.Items[0].ID, other); err == nil {
			t.Fatal("todo scope escaped")
		}
		verifyRecords(t, records, a)
		work, err := knowledge.NewRecordStore(t.Context(), backend, registrar, "work")
		if err != nil {
			t.Fatal(err)
		}
		workPage, err := work.List(t.Context(), "requirements", "", "", 30, a)
		if err != nil || len(workPage.Items) != 0 {
			t.Fatalf("product scope escaped: %+v %v", workPage, err)
		}
		q, args, err := query.NewSelectBuilder(d, "_schema_migrations").Columns("dirty").Where(query.Equal("owner", "knowledge_records")).Build()
		if err != nil {
			t.Fatal(err)
		}
		var dirty bool
		if err = c.DB.QueryRowContext(t.Context(), q, args...).Scan(&dirty); err != nil || dirty {
			t.Fatalf("record migrations not owned by host: %v", err)
		}
		sentinel := errors.New("DDL interrupted")
		if err = registrar.ApplyOwnedMigration(t.Context(), "failure_test", 1, "failure", "checksum", func(context.Context) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		if err = registrar.ApplyOwnedMigration(t.Context(), "failure_test", 1, "failure", "checksum", func(context.Context) error { t.Fatal("dirty migration replayed"); return nil }); err == nil {
			t.Fatal("dirty migration not rejected")
		}
	})
}

func verifyRecords(t *testing.T, records *knowledge.RecordStore, a toolsdk.Authority) {
	t.Helper()
	validate := func(_, next *knowledge.Record) error { next.Status = "draft"; return nil }
	in := knowledge.RecordWrite{ClientID: "same", Title: "需求分析", Data: json.RawMessage(`{"notes":"中文 😀"}`)}
	const workers = 6
	results := make(chan knowledge.Record, workers)
	failures := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			r, err := records.Save(t.Context(), "requirements", in, a, validate)
			results <- r
			failures <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal("concurrent receipt", err)
		}
	}
	var first knowledge.Record
	for r := range results {
		if first.ID == "" {
			first = r
		}
		if r.ID != first.ID || r.Revision != 1 || string(r.Data) != string(in.Data) {
			t.Fatal("duplicate business effect")
		}
	}
	in.ID, in.ExpectedRevision, in.ClientID, in.Title = first.ID, 1, "edit", "修订需求"
	start = make(chan struct{})
	edits := make(chan error, workers)
	for n := range workers {
		edited := in
		edited.ClientID = fmt.Sprintf("edit-%d", n)
		wg.Go(func() {
			<-start
			_, err := records.Save(t.Context(), "requirements", edited, a, validate)
			edits <- err
		})
	}
	close(start)
	wg.Wait()
	close(edits)
	successes := 0
	for err := range edits {
		if err == nil {
			successes++
			continue
		}
		var coded *toolsdk.Error
		if !errors.As(err, &coded) || coded.Code != "knowledge.records.revision_conflict" {
			t.Fatal("unclassified CAS conflict", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent revisions committed %d writes", successes)
	}
	previous, err := records.Get(t.Context(), "requirements", first.ID, 1, a)
	if err != nil || previous.Title != first.Title {
		t.Fatalf("immutable version: %+v %v", previous, err)
	}
	in.ClientID, in.ExpectedRevision = "rejected", 2
	rejected := errors.New("validator rejection")
	if _, err = records.Save(t.Context(), "requirements", in, a, func(_, _ *knowledge.Record) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	if _, found, err := records.Receipt(t.Context(), "requirements", in, a); err != nil || found {
		t.Fatal("rejected write left a receipt", err)
	}
	current, err := records.Get(t.Context(), "requirements", first.ID, 0, a)
	if err != nil || current.Revision != 2 {
		t.Fatal("rejected write changed revision", err)
	}
	in.ID, in.ExpectedRevision, in.ClientID, in.Title = "", 0, "SAME", "case-sensitive client key"
	upper, err := records.Save(t.Context(), "requirements", in, a, validate)
	if err != nil || upper.ID == first.ID {
		t.Fatal("case-sensitive key conflated", err)
	}
	page, err := records.List(t.Context(), "requirements", "", "", 1, a)
	if err != nil || len(page.Items) != 1 || page.Complete {
		t.Fatalf("pagination: %+v %v", page, err)
	}
	next, err := records.List(t.Context(), "requirements", "", page.NextCursor, 1, a)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || !next.Complete {
		t.Fatalf("cursor: %+v %v", next, err)
	}
	other := a
	other.WorkspaceID = "another"
	if _, err = records.Get(t.Context(), "requirements", first.ID, 0, other); err == nil {
		t.Fatal("record scope escaped")
	}
}

func TestStartupLockExclusionAndReleaseOnAllDatabases(t *testing.T) {
	databasetest.ForEach(t, func(t *testing.T, options databasetest.Options) {
		config := persistence.ConnectionOptions{Path: options.Path, DSN: options.DSN, Schema: options.Schema}
		c, _, err := persistence.OpenConnection(t.Context(), options.Driver, config)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		second, _, err := persistence.OpenConnection(ctx, options.Driver, config)
		if second != nil {
			second.Close()
		}
		if err == nil {
			t.Fatal("two hosts acquired the migration lock")
		}
		if err = c.Close(); err != nil {
			t.Fatal(err)
		}
		second, _, err = persistence.OpenConnection(t.Context(), options.Driver, config)
		if err != nil {
			t.Fatal("migration lock leaked", err)
		}
		if err = second.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
