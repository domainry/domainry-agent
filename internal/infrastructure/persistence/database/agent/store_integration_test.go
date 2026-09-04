package agent

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
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
	dialect, _ := ormdialect.New(ormdialect.SQLite)
	store, err := NewStore(database, dialect.WithSchema(""), sqlite.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	return store, database
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
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_worker_scopes WHERE workspace_id=?`, run.WorkspaceID).Scan(&scopes); err != nil || scopes != 1 {
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

func TestAgentDefinitionStoreOwnsSyncRestoreAndDisable(t *testing.T) {
	store, database := openAgentStore(t)
	repository := NewDefinitionStore(store)
	first := agentpersistence.DefinitionSnapshot{SchemaVersion: "1", SchemaHash: "hash-1", SourceKind: "manifest", SourceID: "crm", Skills: []agentsdk.SkillSchema{{Key: "lookup", Name: "Lookup"}}, Agents: []agentsdk.AgentSchema{{Key: "assistant", Name: "Assistant"}}}
	if err := repository.SyncDefinitions(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.DefinitionSnapshot(t.Context())
	if err != nil || len(loaded.Skills) != 1 || len(loaded.Agents) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	second := first
	second.SchemaHash = "hash-2"
	second.Skills = nil
	if err := repository.SyncDefinitions(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	loaded, err = repository.DefinitionSnapshot(t.Context())
	if err != nil || len(loaded.Skills) != 0 || len(loaded.Agents) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	var disabled int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_skill_definitions WHERE disabled_at IS NOT NULL`).Scan(&disabled); err != nil || disabled != 1 {
		t.Fatalf("disabled=%d err=%v", disabled, err)
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
