package module

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
)

func TestModulePublishesOnlyAgentOwnedSchema(t *testing.T) {
	tables := SchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 19 || !slices.Equal(OwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("Agent schema ownership=%d tables=%v", len(tables), OwnedTables())
	}
	for _, table := range tables {
		if table.Owner != "agent" || !strings.HasPrefix(table.Name, "_agent_") {
			t.Fatalf("Agent Module claimed foreign table: %+v", table)
		}
	}
}

func TestModulePublishesCanonicalAgentMigrations(t *testing.T) {
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	statements := ""
	for _, migration := range migrations {
		statements += strings.Join(migration.Statements, "\n")
	}
	for _, table := range OwnedTables() {
		if !strings.Contains(statements, `CREATE TABLE IF NOT EXISTS "`+table+`"`) {
			t.Fatalf("canonical Agent migrations omit %s", table)
		}
	}
}
