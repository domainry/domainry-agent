package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/artifactkernel"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

func openAgentStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	dialect, _ := ormdialect.New(ormdialect.SQLite)
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err := database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	definitionMigrations, err := shareddefinition.SchemaMigrationsForDialect(dialect.WithSchema(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range definitionMigrations {
		for _, statement := range migration.Statements {
			if _, err := database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = database.ExecContext(t.Context(), `CREATE TABLE _subject_steps (workspace_id TEXT NOT NULL, request_id TEXT NOT NULL, owner TEXT NOT NULL, operation TEXT NOT NULL, payload_json TEXT NOT NULL, completed_at TEXT NOT NULL, PRIMARY KEY(workspace_id,request_id,owner,operation))`); err != nil {
		t.Fatal(err)
	}
	artifactMigration, err := sharedartifact.SchemaMigrationForDialect(dialect.WithSchema(""))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range artifactMigration.Statements {
		if _, err = database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewStore(database, dialect.WithSchema(""), sqlite.NewEngine(), "agent-store-test")
	if err != nil {
		t.Fatal(err)
	}
	content, err := artifactkernel.NewContentFiles(filepath.Join(t.TempDir(), "shared-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = content.Close() })
	if err = store.BindArtifactPersistence(sharedartifact.NewSQLStore(database, dialect.WithSchema("")), content, content); err != nil {
		t.Fatal(err)
	}
	return store, database
}

func TestConversationItemsReplaceSixPrivateHistoryTables(t *testing.T) {
	_, database := openAgentStore(t)
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, conversationItemTable).Scan(&count); err != nil || count != 1 {
		t.Fatalf("conversation item table count=%d err=%v", count, err)
	}
	for _, table := range []string{"_agent_conversation_messages", "_agent_conversation_inputs", "_agent_conversation_summaries", "_agent_conversation_events", "_agent_conversation_task_agreement_updates", "_agent_conversation_task_completions"} {
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired conversation table %s count=%d err=%v", table, count, err)
		}
	}
	for _, index := range []string{"idx_agent_conversation_item_sequence_v2", "idx_agent_conversation_item_run_v2", "idx_agent_conversation_item_subject_v2"} {
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil || count != 1 {
			t.Fatalf("conversation item index %s count=%d err=%v", index, count, err)
		}
	}
}

func TestPeerLinksReplaceCurrentMicroTablesAndIsolateKinds(t *testing.T) {
	_, database := openAgentStore(t)
	const owner = "owner-shared"
	const id = "link-shared"
	for _, row := range []struct {
		kind string
		peer string
	}{
		{conversationPeerLinkKindInstance, ""},
		{conversationPeerLinkKindDelegation, ""},
		{conversationPeerLinkKindParticipant, "viewer-shared"},
		{conversationPeerLinkKindUseGrant, "viewer-shared"},
	} {
		if _, err := database.ExecContext(t.Context(), `INSERT INTO _agent_peer_links (link_kind,owner_key,link_id,peer_key,scope_key,payload_json) VALUES (?,?,?,?,?,?)`, row.kind, owner, id, row.peer, id, `{}`); err != nil {
			t.Fatalf("insert peer-link kind %s: %v", row.kind, err)
		}
	}
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_peer_links WHERE owner_key=? AND link_id=?`, owner, id).Scan(&count); err != nil || count != 4 {
		t.Fatalf("peer-link kind isolation count=%d err=%v", count, err)
	}
	for _, table := range []string{"_agent_peer_instances", "_agent_delegations", "_agent_delegation_agreements", "_agent_delegation_assignments", "_agent_delegation_deliveries", "_agent_delegation_disagreements", "_agent_delegation_participants", "_agent_peer_use_grants"} {
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired peer table %s count=%d err=%v", table, count, err)
		}
	}
}

func TestAgentStateStorePersistsAndComparesRevision(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := NewAgentStateStore(store)
	value := agentmodel.AgentStateRecord{Kind: "session", Key: "one", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "operator", Payload: []byte(`{"status":"open"}`), UpdatedAt: 1}
	if err := repository.Put(t.Context(), value.WorkspaceID, value); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.Get(t.Context(), value.WorkspaceID, value.Kind, value.Key)
	if err != nil || !found || loaded.UpdatedAt != 1 {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	value.UpdatedAt = 2
	if updated, err := repository.CompareAndSwap(t.Context(), value.WorkspaceID, value, 0); err != nil || updated {
		t.Fatalf("stale CAS updated=%v err=%v", updated, err)
	}
	if updated, err := repository.CompareAndSwap(t.Context(), value.WorkspaceID, value, 1); err != nil || !updated {
		t.Fatalf("CAS updated=%v err=%v", updated, err)
	}
}

func TestAgentTaskStoreOwnsIdempotencyClaimFenceAndWorkerScope(t *testing.T) {
	store, database := openAgentStore(t)
	repository := NewAgentTaskRunStore(store)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	run := agentmodel.AgentTaskRun{ID: "run-a", WorkspaceID: "workspace-a", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "idem-a", MaxAttempts: 3, CreatedAt: now, UpdatedAt: now}
	created, replay, err := repository.Create(t.Context(), run)
	if err != nil || replay || created.ID != run.ID {
		t.Fatalf("created=%+v replay=%v err=%v", created, replay, err)
	}
	created, replay, err = repository.Create(t.Context(), run)
	if err != nil || !replay || created.ID != run.ID {
		t.Fatalf("replay=%+v replay=%v err=%v", created, replay, err)
	}
	scope := agentpersistence.SystemScope{Kind: agentpersistence.AgentSystemScopeKindGlobal, Purpose: "test worker"}
	claim, found, err := repository.ClaimNextAgentTaskRunForWorker(t.Context(), scope, "worker-a", now, time.Minute)
	if err != nil || !found || claim.Lease.FencingToken != 1 {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	if _, found, err := repository.ClaimNextAgentTaskRunForWorker(t.Context(), scope, "worker-b", now, time.Minute); err != nil || found {
		t.Fatalf("second found=%v err=%v", found, err)
	}
	var scopes int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _worker_scopes WHERE owner=? AND scope_key=?`, agentTaskWorkerQueueKind, run.WorkspaceID).Scan(&scopes); err != nil || scopes != 1 {
		t.Fatalf("scopes=%d err=%v", scopes, err)
	}
}

func TestAgentTaskStoreSerializesConcurrentClaimsAndTerminalTransitions(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := NewAgentTaskRunStore(store)
	now := time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC)
	run := agentmodel.AgentTaskRun{ID: "run-concurrent", WorkspaceID: "workspace-a", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "idem-concurrent", MaxAttempts: 3, CreatedAt: now, UpdatedAt: now, Revision: 1}
	if _, replay, err := repository.Create(t.Context(), run); err != nil || replay {
		t.Fatalf("create replay=%v err=%v", replay, err)
	}

	const contenders = 12
	claims := make(chan agentpersistence.AgentTaskClaim, contenders)
	errors := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			claim, found, err := repository.ClaimAgentTaskRun(context.Background(), run.WorkspaceID, run.ID, "worker-"+strconv.Itoa(index), now, time.Minute)
			if err != nil {
				errors <- err
				return
			}
			if found {
				claims <- claim
			}
		}(index)
	}
	wait.Wait()
	close(claims)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	claimed := make([]agentpersistence.AgentTaskClaim, 0, contenders)
	for claim := range claims {
		claimed = append(claimed, claim)
	}
	if len(claimed) != 1 || claimed[0].Run.Attempt != 1 || claimed[0].Lease.FencingToken != 1 {
		t.Fatalf("claims=%+v", claimed)
	}

	claim := claimed[0]
	transitionErrors := make(chan error, contenders)
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			candidate := claim.Run
			candidate.Status = agentmodel.AgentTaskRunSucceeded
			candidate.Outcome = "success"
			candidate.UpdatedAt = now.Add(time.Duration(index+1) * time.Millisecond)
			completedAt := candidate.UpdatedAt
			candidate.CompletedAt = &completedAt
			transitionErrors <- repository.SaveRunning(context.Background(), candidate, claim.Lease.Owner, claim.Lease.FencingToken)
		}(index)
	}
	wait.Wait()
	close(transitionErrors)
	succeeded, rejected := 0, 0
	for err := range transitionErrors {
		if err == nil {
			succeeded++
		} else {
			rejected++
		}
	}
	if succeeded != 1 || rejected != contenders-1 {
		t.Fatalf("terminal transitions succeeded=%d rejected=%d", succeeded, rejected)
	}
	stored, found, err := repository.Get(t.Context(), run.WorkspaceID, run.ID)
	if err != nil || !found || stored.Status != agentmodel.AgentTaskRunSucceeded || stored.Outcome != "success" {
		t.Fatalf("stored=%+v found=%v err=%v", stored, found, err)
	}
}

func TestAgentInteractiveHandoffCommitsTaskAtomically(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := NewAgentTaskRunStore(store)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	interactive := agentmodel.AgentInteractiveRun{ID: "interactive-a", SessionID: "session-a", WorkspaceID: "workspace-a", UserID: "user-a", RoleKey: "operator", AgentKey: "assistant", EntrypointKey: "chat", ContextRevision: "ctx-1", Status: agentmodel.AgentInteractiveRunRunning, IdempotencyKey: "interactive-idem", CreatedAt: now, UpdatedAt: now, Context: agentsdk.GlobalContext{ContextRevision: "ctx-1", EntrypointKey: "chat", AgentKey: "assistant"}}
	if _, _, err := repository.CreateInteractiveRun(t.Context(), interactive); err != nil {
		t.Fatal(err)
	}
	task := agentmodel.AgentTaskRun{ID: "task-a", WorkspaceID: interactive.WorkspaceID, InteractiveRunID: interactive.ID, TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "task-idem", MaxAttempts: 2, CreatedAt: now, UpdatedAt: now}
	handedOff, replay, err := repository.CommitInteractiveTaskHandoff(t.Context(), interactive, 0, task)
	if err != nil || replay || handedOff.TaskRunID != task.ID || handedOff.Status != agentmodel.AgentInteractiveRunHandedOff {
		t.Fatalf("handoff=%+v replay=%v err=%v", handedOff, replay, err)
	}
	if _, found, err := repository.Get(t.Context(), task.WorkspaceID, task.ID); err != nil || !found {
		t.Fatalf("task found=%v err=%v", found, err)
	}
}

func TestUnifiedAgentRunTableIsolatesTaskInteractiveAndConversationKinds(t *testing.T) {
	store, database := openAgentStore(t)
	runs := NewAgentTaskRunStore(store)
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	const workspaceID = "workspace-shared"
	const sharedRunID = "run-shared"
	const sharedIdempotencyKey = "idempotency-shared"

	task := agentmodel.AgentTaskRun{ID: sharedRunID, WorkspaceID: workspaceID, TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: sharedIdempotencyKey, MaxAttempts: 2, CreatedAt: now, UpdatedAt: now}
	if _, replay, err := runs.Create(t.Context(), task); err != nil || replay {
		t.Fatalf("create task replay=%v err=%v", replay, err)
	}
	interactive := agentmodel.AgentInteractiveRun{ID: sharedRunID, SessionID: "session-shared", WorkspaceID: workspaceID, UserID: "user-shared", RoleKey: "operator", AgentKey: "assistant", EntrypointKey: "chat", ContextRevision: "ctx-1", Status: agentmodel.AgentInteractiveRunRunning, IdempotencyKey: sharedIdempotencyKey, CreatedAt: now, UpdatedAt: now, Context: agentsdk.GlobalContext{ContextRevision: "ctx-1", EntrypointKey: "chat", AgentKey: "assistant"}}
	if _, replay, err := runs.CreateInteractiveRun(t.Context(), interactive); err != nil || replay {
		t.Fatalf("create interactive replay=%v err=%v", replay, err)
	}

	conversations := NewConversationStore(store)
	authority := conversationTestAuthority()
	authority.WorkspaceID = workspaceID
	conversation, err := conversations.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "unified-run-table", Title: "Unified runs"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conversations.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: sharedIdempotencyKey, Message: "verify run isolation"}, authority); err != nil {
		t.Fatal(err)
	}

	if loaded, found, err := runs.Get(t.Context(), workspaceID, sharedRunID); err != nil || !found || loaded.TaskKey != task.TaskKey {
		t.Fatalf("task loaded=%+v found=%v err=%v", loaded, found, err)
	}
	if loaded, found, err := runs.GetInteractiveRun(t.Context(), workspaceID, sharedRunID); err != nil || !found || loaded.SessionID != interactive.SessionID {
		t.Fatalf("interactive loaded=%+v found=%v err=%v", loaded, found, err)
	}

	rows, err := database.QueryContext(t.Context(), `SELECT run_kind, COUNT(*) FROM _agent_runs GROUP BY run_kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			t.Fatal(err)
		}
		counts[kind] = count
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{agentRunKindTask, agentRunKindInteractive, agentRunKindConversation} {
		if counts[kind] != 1 {
			t.Fatalf("run kind %s count=%d all=%v", kind, counts[kind], counts)
		}
	}
	for _, retired := range []string{"_agent_task_runs", "_agent_interactive_runs", "_agent_conversation_runs"} {
		var count int
		if err = database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired table %s count=%d err=%v", retired, count, err)
		}
	}
}

func TestAgentDefinitionStoreOwnsSyncRestoreAndDisable(t *testing.T) {
	store, database := openAgentStore(t)
	repository := NewDefinitionStore(store)
	first := agentpersistence.DefinitionSnapshot{
		SchemaVersion: "1", SchemaHash: "hash-1", SourceKind: "manifest", SourceID: "crm",
		Skills: []agentsdk.SkillSchema{{Key: "lookup", Name: "Lookup"}},
		Agents: []agentsdk.AgentSchema{{Key: "assistant", Name: "Assistant"}},
		Tasks: []agentsdk.AgentTaskDefinition{{
			ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", Name: "Review", AgentKey: "assistant", Instruction: "Review",
			InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true,
		}},
		Entrypoints: []agentsdk.AgentEntrypointAssignment{{Key: "default"}},
		Principals:  []agentsdk.AgentServicePrincipalBinding{{Key: "worker"}},
	}
	if err := repository.SyncDefinitions(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.DefinitionSnapshot(t.Context())
	if err != nil || len(loaded.Skills) != 1 || len(loaded.Agents) != 1 || len(loaded.Tasks) != 1 || len(loaded.Entrypoints) != 1 || len(loaded.Principals) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	second := first
	second.SchemaHash = "hash-2"
	second.Skills = nil
	if err := repository.SyncDefinitions(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	loaded, err = repository.DefinitionSnapshot(t.Context())
	if err != nil || len(loaded.Skills) != 0 || len(loaded.Agents) != 1 || len(loaded.Tasks) != 1 || len(loaded.Entrypoints) != 1 || len(loaded.Principals) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	var rows int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _definitions WHERE owner = 'agent' AND kind = 'skill'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("removed shared skill rows=%d err=%v", rows, err)
	}
	for _, table := range []string{"_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions", "_agent_entrypoint_definitions", "_agent_service_principal_definitions"} {
		if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("private Agent Definition table %s rows=%d err=%v", table, rows, err)
		}
	}
}

func TestAgentLifecycleStoreOwnsEligibilityReferencesAndDeleteFence(t *testing.T) {
	store, _ := openAgentStore(t)
	states := NewAgentStateStore(store)
	lifecycle := NewLifecycleStore(store)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour).UnixNano()
	values := []agentmodel.AgentStateRecord{
		{Kind: "session", Key: "archived", WorkspaceID: "workspace-a", Payload: []byte(`{"archived":true}`), UpdatedAt: old},
		{Kind: "session", Key: "active", WorkspaceID: "workspace-a", Payload: []byte(`{"archived":false}`), UpdatedAt: old},
		{Kind: "proposal", Key: "decided", WorkspaceID: "workspace-a", Payload: []byte(`{"status":"approved"}`), UpdatedAt: old},
	}
	if err := states.PutBatch(t.Context(), "workspace-a", values); err != nil {
		t.Fatal(err)
	}
	candidates, err := lifecycle.ListLifecycleCandidates(t.Context(), "workspace-a", agentpersistence.LifecycleQuery{PolicyKey: "agent.dialog.v1", Now: now, Retention: time.Hour})
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	query := candidates[0]
	query.State.UpdatedAt++
	if deleted, err := lifecycle.DeleteLifecycleCandidate(t.Context(), "workspace-a", query); err != nil || deleted {
		t.Fatalf("stale deleted=%v err=%v", deleted, err)
	}
}
