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
			if len(migrations) != 16 || migrations[0].Version != SchemaVersion || migrations[len(migrations)-1].Version != 36 {
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
			for _, folded := range []uint{16, 17, 21, 22, 23, 24, 25, 26, 31, 32, 33} {
				if _, found := byVersion[folded]; found {
					t.Fatalf("folded Agent migration v%d remains", folded)
				}
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
			peerLinks := byVersion[20]
			for _, fragment := range []string{conversationPeerLinkTable, "link_kind", "link_id", "peer_key", "scope_key", "idx_agent_peer_link_delegation_source_v20", "idx_agent_peer_link_participant_v20", "idx_agent_peer_link_use_grant_v20"} {
				if !strings.Contains(peerLinks, fragment) {
					t.Fatalf("missing peer-link migration field/index %s", fragment)
				}
			}
			if strings.Contains(peerLinks, "_schema_migrations") {
				t.Fatal("peer-link migration owns a private ledger")
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, table := range []string{"_agent_runtime_states", agentRunTable, "_worker_scopes"} {
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
			conversationItems := byVersion[2]
			for _, fragment := range []string{conversationItemTable, "item_kind", "subject_id", "idx_agent_conversation_item_sequence_v2", "idx_agent_conversation_item_run_v2", "idx_agent_conversation_item_subject_v2"} {
				if !strings.Contains(conversationItems, fragment) {
					t.Fatalf("missing typed conversation item field/index %s", fragment)
				}
			}
			for _, retired := range []string{"_agent_conversation_messages", "_agent_conversation_inputs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_conversation_task_agreement_updates", "_agent_conversation_task_completions"} {
				if strings.Contains(conversationItems, retired) {
					t.Fatalf("private conversation item table remains: %s", retired)
				}
			}
			for _, retired := range []string{"_agent_peer_instances", "_agent_delegations", "_agent_delegation_agreements", "_agent_delegation_assignments", "_agent_delegation_deliveries", "_agent_delegation_disagreements", "_agent_delegation_participants", "_agent_peer_use_grants"} {
				if strings.Contains(peerLinks+"\n"+conversationItems, retired) {
					t.Fatalf("retired Agent peer table remains: %s", retired)
				}
			}
			tasks := byVersion[15]
			for _, fragment := range []string{conversationTaskTable, "record_kind", "scheduled_plan_id", "observation_hash", "fencing_token", "idx_agent_task_claim_v15", "idx_agent_task_follow_up_claim_v15"} {
				if !strings.Contains(tasks, fragment) {
					t.Fatalf("missing unified Agent task field/index %s", fragment)
				}
			}
			for _, retired := range []string{"_agent_conversation_tasks", "_agent_conversation_task_plans", "_agent_conversation_follow_up_states", "_agent_conversation_follow_up_events", " plan_id ", " event_id ", " fence "} {
				if strings.Contains(tasks, retired) {
					t.Fatalf("retired Agent task schema remains: %s", retired)
				}
			}
			allRunDDL := strings.Join(append([]string{}, migrations[0].Statements...), "\n") + "\n" + conversationItems + "\n" + byVersion[19]
			for _, fragment := range []string{"run_kind", "scope_key", "idempotency_key", "fencing_token", "idx_agent_run_task_claim_v1", "idx_agent_run_interactive_list_v1", "idx_agent_run_conversation_claim_v2", "idx_agent_run_conversation_capacity_v19"} {
				if !strings.Contains(allRunDDL, fragment) {
					t.Fatalf("missing unified Agent run field/index %s", fragment)
				}
			}
			for _, retired := range []string{"_agent_task_runs", "_agent_interactive_runs", "_agent_conversation_runs", "client_message_id", "workspace_key IS NULL"} {
				if strings.Contains(allRunDDL, retired) {
					t.Fatalf("retired Agent run schema remains: %s", retired)
				}
			}
			runSteps := byVersion[3]
			for _, fragment := range []string{conversationRunStepTable, "record_kind", "step_no", "call_key"} {
				if !strings.Contains(runSteps, fragment) {
					t.Fatalf("missing unified Agent run-step field %s", fragment)
				}
			}
			for _, retired := range []string{"_agent_conversation_steps", "_agent_conversation_tool_calls", "_agent_conversation_step_sources"} {
				if strings.Contains(runSteps+"\n"+byVersion[24], retired) {
					t.Fatalf("retired Agent run-step table remains: %s", retired)
				}
			}
			all := make([]string, 0, len(migrations))
			for _, migration := range migrations {
				all = append(all, migration.Statements...)
			}
			joinedAll := strings.Join(all, "\n")
			for _, foreign := range []string{"_agent_user_todos", "_agent_todo_mutations"} {
				if strings.Contains(joinedAll, foreign) {
					t.Fatalf("Agent migration still owns Todo table %s", foreign)
				}
			}
			for _, foreign := range []string{"_agent_artifacts", "_agent_artifact_versions", "_agent_artifact_mutations", "_agent_knowledge_libraries", "_agent_knowledge_library_members", "_agent_knowledge_documents", "_agent_knowledge_sources", "_agent_knowledge_document_sources", "_agent_knowledge_document_jobs", "_agent_knowledge_datasource_bindings", "_agent_attachment_knowledge_sources", "_agent_attachment_index_jobs", "_knowledge_owner_operation_receipts"} {
				if strings.Contains(joinedAll, foreign) {
					t.Fatalf("Agent migration still owns Knowledge table %s", foreign)
				}
			}
			for _, retired := range []string{"_agent_artifact_exports", "_agent_conversation_attachments", "_agent_attachment_cleanup", "_agent_subject_erasure_receipts", "_todo_subject_erasure_receipts", "_knowledge_subject_erasure_receipts"} {
				if strings.Contains(joinedAll, retired) {
					t.Fatalf("Agent migration still owns retired table %s", retired)
				}
			}
			for _, sharedOrRetired := range []string{"_operations", "_operation_controls", "_operation_break_glass_grants", "_agent_owner_operation_receipts", "_agent_collaboration_mutations"} {
				if strings.Contains(joinedAll, sharedOrRetired) {
					t.Fatalf("Agent migration owns shared or retired Operations table %s", sharedOrRetired)
				}
			}
		})
	}
}
