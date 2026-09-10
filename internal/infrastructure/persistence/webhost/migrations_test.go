package webhost

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	_ "modernc.org/sqlite"
)

func TestNestedOwnerMigrationsReplayAndFailClosed(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "host.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	renderer, err := persistence.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	r := &Registrar{DB: db, Renderer: renderer}
	if err = r.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	called := 0
	apply := func(ctx context.Context) error {
		called++
		return r.ApplyOwnedMigration(ctx, "metadata", 1, "metadata", "metadata-checksum", func(context.Context) error { called++; return nil })
	}
	for range 2 {
		if err = r.ApplyOwnedMigration(t.Context(), "identity", 1, "identity", "identity-checksum", apply); err != nil {
			t.Fatal(err)
		}
	}
	if called != 2 {
		t.Fatalf("applied migrations replayed: %d", called)
	}
	if err = r.ApplyOwnedMigration(t.Context(), "identity", 1, "identity", "changed", apply); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("checksum drift: %v", err)
	}
	failure := errors.New("interrupted migration")
	if err = r.ApplyOwnedMigration(t.Context(), "agent", 1, "agent", "agent-checksum", func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("failure not retained: %v", err)
	}
	if err = r.ApplyOwnedMigration(t.Context(), "agent", 1, "agent", "agent-checksum", apply); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty migration retried: %v", err)
	}
}
