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
			if len(migrations) != 28 || migrations[0].Version != SchemaVersion || migrations[1].Version != 2 || migrations[2].Version != 3 || migrations[3].Version != 4 || migrations[4].Version != 5 || migrations[5].Version != 6 || migrations[6].Version != 7 || migrations[7].Version != 8 || migrations[8].Version != 9 || migrations[9].Version != 10 || migrations[10].Version != 11 || migrations[11].Version != 12 || migrations[12].Version != 13 || migrations[13].Version != 14 || migrations[14].Version != 15 || migrations[15].Version != 16 || migrations[16].Version != 17 || migrations[17].Version != 18 || migrations[18].Version != 19 || migrations[19].Version != 20 || migrations[20].Version != 21 || migrations[21].Version != 22 || migrations[22].Version != 23 || migrations[23].Version != 24 || migrations[24].Version != 25 || migrations[25].Version != 26 || migrations[26].Version != 27 || migrations[27].Version != 28 {
				t.Fatalf("unexpected migration versions/count: %d", len(migrations))
			}
			releases := strings.Join(migrations[27].Statements, "\n")
			for _, fragment := range []string{conversationSourceReleaseTable, "idx_agent_source_release_v28", "idx_agent_source_producer_v28", "before_step", "producer_key"} {
				if !strings.Contains(releases, fragment) {
					t.Fatalf("missing release migration field %s", fragment)
				}
			}
			subjects := strings.Join(migrations[26].Statements, "\n")
			if !strings.Contains(subjects, conversationDelegationSubjectTable) || !strings.Contains(subjects, "idx_agent_delegation_subject_v27") || strings.Contains(subjects, "_schema_migrations") {
				t.Fatal("invalid delegation subject migration", subjects)
			}
			participants := strings.Join(migrations[25].Statements, "\n")
			if !strings.Contains(participants, conversationParticipantTable) || !strings.Contains(participants, "idx_agent_participant_v26") || strings.Contains(participants, "_schema_migrations") {
				t.Fatal("invalid participant migration", participants)
			}
			sharing := strings.Join(migrations[24].Statements, "\n")
			for _, fragment := range []string{conversationAgentGrantTable, "viewer_key", "owner_user_id", "idx_agent_peer_use_grant_v25"} {
				if !strings.Contains(sharing, fragment) {
					t.Fatalf("missing sharing migration field %s", fragment)
				}
			}
			if strings.Contains(sharing, "_schema_migrations") {
				t.Fatal("sharing migration owns a private ledger")
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
