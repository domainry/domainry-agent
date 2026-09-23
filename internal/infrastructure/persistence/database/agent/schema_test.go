package agent

import (
	"strings"
	"testing"
)

func TestSchemaMigrationsExcludeSharedDefinitionsAndOwnRuntimeStateForAllDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			migrations, err := SchemaMigrations(driver, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) != 34 || migrations[0].Version != SchemaVersion || migrations[len(migrations)-1].Version != 36 {
				t.Fatalf("unexpected migration versions/count: %d", len(migrations))
			}
			byVersion := map[uint]string{}
			for _, migration := range migrations {
				byVersion[migration.Version] = strings.Join(migration.Statements, "\n")
			}
			if _, found := byVersion[7]; found {
				t.Fatal("retired private attachment migration v7 remains")
			}
			if _, found := byVersion[8]; found {
				t.Fatal("retired private attachment cleanup migration v8 remains")
			}
			if memories := byVersion[36]; !strings.Contains(memories, conversationMemoryChangeTable) || !strings.Contains(memories, "operation") || strings.Contains(memories, "_schema_migrations") {
				t.Fatal("invalid scoped memory migration", memories)
			}
			if forks := byVersion[34]; !strings.Contains(forks, conversationForkTable) || !strings.Contains(forks, "source_event_seq") || strings.Contains(forks, "_schema_migrations") {
				t.Fatal("invalid conversation fork migration", forks)
			}
			plans := byVersion[32]
			if !strings.Contains(plans, conversationTaskPlanTable) || strings.Contains(plans, "_schema_migrations") {
				t.Fatal("invalid task plan migration", plans)
			}
			taskAgreements := byVersion[31]
			if !strings.Contains(taskAgreements, conversationTaskAgreementUpdateTable) || strings.Contains(taskAgreements, "_schema_migrations") {
				t.Fatal("invalid task agreement migration", taskAgreements)
			}
			contract := byVersion[29]
			if !strings.Contains(contract, conversationContractPublicationTable) || strings.Contains(contract, "_schema_migrations") {
				t.Fatal("invalid contract publication migration", contract)
			}
			releases := byVersion[28]
			for _, fragment := range []string{conversationSourceReleaseTable, "idx_agent_source_release_v28", "idx_agent_source_producer_v28", "before_step", "producer_key"} {
				if !strings.Contains(releases, fragment) {
					t.Fatalf("missing release migration field %s", fragment)
				}
			}
			subjects := byVersion[27]
			if !strings.Contains(subjects, conversationDelegationSubjectTable) || !strings.Contains(subjects, "idx_agent_delegation_subject_v27") || strings.Contains(subjects, "_schema_migrations") {
				t.Fatal("invalid delegation subject migration", subjects)
			}
			participants := byVersion[26]
			if !strings.Contains(participants, conversationParticipantTable) || !strings.Contains(participants, "idx_agent_participant_v26") || strings.Contains(participants, "_schema_migrations") {
				t.Fatal("invalid participant migration", participants)
			}
			sharing := byVersion[25]
			for _, fragment := range []string{conversationAgentGrantTable, "viewer_key", "owner_user_id", "idx_agent_peer_use_grant_v25"} {
				if !strings.Contains(sharing, fragment) {
					t.Fatalf("missing sharing migration field %s", fragment)
				}
			}
			if strings.Contains(sharing, "_schema_migrations") {
				t.Fatal("sharing migration owns a private ledger")
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range []string{"_agent_runtime_states", "_agent_task_runs", "_agent_interactive_runs", "_worker_scopes"} {
				if !strings.Contains(joined, table) {
					t.Errorf("%s migration does not own %s", driver, table)
				}
			}
			for _, table := range []string{"_definitions", "_definition_versions", "_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions", "_agent_entrypoint_definitions", "_agent_service_principal_definitions"} {
				if strings.Contains(joined, table) {
					t.Errorf("%s Agent migration still owns Definition table %s", driver, table)
				}
			}
			if strings.Contains(joined, "_schema_migrations") {
				t.Fatal("Agent migration attempted to create a private ledger")
			}
			all := make([]string, 0, len(migrations))
			for _, migration := range migrations {
				all = append(all, migration.Statements...)
			}
			joinedAll := strings.Join(all, "\n")
			for _, retired := range []string{"_agent_artifact_exports", "_agent_conversation_attachments", "_agent_attachment_cleanup", "_agent_subject_erasure_receipts", "_todo_subject_erasure_receipts", "_knowledge_subject_erasure_receipts"} {
				if strings.Contains(joinedAll, retired) {
					t.Fatalf("Agent migration still owns retired table %s", retired)
				}
			}
		})
	}
}
