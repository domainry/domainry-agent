package agent

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestConversationStepSourcesSurviveDatabaseReopenAndCannotChangeOnReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.db")
	open := func() (*ConversationStore, *sql.DB) {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		dialect, _ := ormdialect.New(ormdialect.SQLite)
		store, err := NewStore(db, dialect.WithSchema(""), sqlite.NewEngine())
		if err != nil {
			t.Fatal(err)
		}
		return NewConversationStore(store), db
	}
	repo, db := open()
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err = db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "step-source", Title: "Evidence sources"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "source-input", Message: "Check disagreement"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "source-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	in := executionStoreInput()
	in.ContextSources = []sdk.ConversationRunReference{{ConversationID: "evidence", RunID: "original-run", BeforeStep: 2}, {ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}}
	bad := in
	bad.ContextSources = []sdk.ConversationRunReference{{ConversationID: c.ID, RunID: run.ID, BeforeStep: 2}}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &bad); err == nil {
		t.Fatal("future context source accepted")
	}
	if _, found, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil || !found {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, _ = open()
	ref := sdk.ConversationRunReference{ConversationID: c.ID, RunID: run.ID}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), ref, a)
	if err != nil || len(snapshot.StepSources) != 1 || snapshot.StepSources[0].Step != 0 || !reflect.DeepEqual(snapshot.StepSources[0].Sources, in.ContextSources) {
		t.Fatalf("source provenance lost on reopen: %+v %v", snapshot.StepSources, err)
	}
	step, found, err := repo.ExecutionStep(t.Context(), claim, 0, nil)
	if err != nil || !found || !reflect.DeepEqual(step.Input.ContextSources, in.ContextSources) {
		t.Fatal("frozen model input lost sources", err)
	}
	changed := in
	changed.ContextSources = nil
	_, _, err = repo.ExecutionStep(t.Context(), claim, 0, &changed)
	requireConversationCode(t, err, "step_input_conflict")
	other := a
	other.UserID = "another-user"
	if _, err = repo.ConversationSourceSnapshot(t.Context(), ref, other); err == nil {
		t.Fatal("sources leaked across owner")
	}
}
