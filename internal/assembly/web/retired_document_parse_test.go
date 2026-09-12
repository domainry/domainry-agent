package web

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	agentpersistence "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentdb "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	agentmodule "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-orm/query"
)

func TestRetiredParsingUpgradeErasesDerivedDataAndKeepsOriginals(t *testing.T) {
	t.Setenv("AUTH_DEFAULT_PASSWORD", "Initial-Retirement-Test!2")
	t.Setenv("AUTH_JWT_SECRET", "retirement-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "retirement-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := agentpersistence.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := agentdb.SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	registrar := &webhost.Registrar{DB: db, Renderer: renderer}
	if err = registrar.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = registrar.ApplyOwnedMigrations(t.Context(), "agent", migrations[:12]); err != nil {
		t.Fatal(err)
	}
	statement, args, err := query.NewInsertBuilder(renderer, "_agent_document_parses").Columns("parse_id", "resource_key", "runtime_id", "parser_key", "state", "revision", "lease_until", "not_before", "payload_json").Values("retired", "synthetic", "retirement-runtime", "removed", "ready", 1, 0, 0, `{"body":"synthetic derived text"}`).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, suffix := range []string{".parses", ".documents", ".attachments"} {
		if err = os.MkdirAll(path+suffix, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(path+suffix, "original-test-marker"), []byte("synthetic"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	options := Options{DatabasePath: path, RuntimeID: "retirement-runtime", WorkspaceID: "retirement-workspace", ApplicationKey: "retirement-app", Agent: agentmodule.Options{ConversationProvider: testModel{}}}
	for range 2 {
		host, err := Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		statement, args, err := query.NewSelectBuilder(renderer, "_agent_document_parses").Columns("parse_id").Build()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := host.db.QueryContext(t.Context(), statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("old parser metadata remains")
		}
		rows.Close()
		if _, err = os.Stat(path + ".parses"); !os.IsNotExist(err) {
			t.Fatal("retired cache remains", err)
		}
		for _, suffix := range []string{".documents", ".attachments"} {
			data, err := os.ReadFile(filepath.Join(path+suffix, "original-test-marker"))
			if err != nil || string(data) != "synthetic" {
				t.Fatal("original store changed", err)
			}
		}
		if err = host.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
