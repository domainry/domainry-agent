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
			if len(migrations) != 17 || migrations[0].Version != SchemaVersion || migrations[1].Version != 2 || migrations[2].Version != 3 || migrations[3].Version != 4 || migrations[4].Version != 5 || migrations[5].Version != 6 || migrations[6].Version != 7 || migrations[7].Version != 8 || migrations[8].Version != 9 || migrations[9].Version != 10 || migrations[10].Version != 11 || migrations[11].Version != 12 || migrations[12].Version != 13 || migrations[13].Version != 14 || migrations[14].Version != 15 || migrations[15].Version != 16 || migrations[16].Version != 17 {
				t.Fatalf("unexpected migration versions/count: %d", len(migrations))
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
