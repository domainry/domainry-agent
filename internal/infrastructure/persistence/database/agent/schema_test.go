package agent

import (
	"strings"
	"testing"
)

func TestSchemaMigrationsOwnDefinitionsAndRuntimeStateForAllDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			migrations, err := SchemaMigrations(driver, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) != 1 || migrations[0].Version != SchemaVersion {
				t.Fatalf("migrations=%+v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range append(append([]string(nil), schemaDefinitionTables...), "_agent_runtime_states", "_agent_task_runs", "_agent_interactive_runs", "_agent_worker_scopes") {
				if !strings.Contains(joined, table) {
					t.Errorf("%s migration does not own %s", driver, table)
				}
			}
			if strings.Contains(joined, "_schema_migrations") {
				t.Fatal("Agent migration attempted to create a private ledger")
			}
		})
	}
}
