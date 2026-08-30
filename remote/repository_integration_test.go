package remote

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/server"
	_ "modernc.org/sqlite"
)

type repositoryRunner struct{}

func (repositoryRunner) Start(context.Context, agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}, nil
}
func (repositoryRunner) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunRunning}, nil
}
func (repositoryRunner) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunCancelled}, nil
}
func (repositoryRunner) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{}, nil
}

func TestSaaSBindingPersistsDefinitionsStateAndWorkerRunsRemotely(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := agentstore.EnsureSchema(t.Context(), db, "sqlite", ""); err != nil {
		t.Fatal(err)
	}
	renderer, err := agentstore.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentstore.NewStore(db, renderer, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repositories := agentstore.NewRepositories(store)
	runner := repositoryRunner{}
	repositoryHandler := server.New(server.Config{APIKey: "secret", Runner: runner, Interactive: runner, Repositories: repositories}).Handler()
	var mutationAttempts atomic.Int32
	httpServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/repository/task.mutation_insert" && mutationAttempts.Add(1) == 1 {
			http.Error(response, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		repositoryHandler.ServeHTTP(response, request)
	}))
	defer httpServer.Close()
	host := newRemoteHost(t, "runtime-a")
	bindingValue, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime-a"}, host)
	if err != nil {
		t.Fatal(err)
	}
	binding := bindingValue.(*binding)
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	definitions := binding.DefinitionRepository()
	snapshot := agentrepository.DefinitionSnapshot{SchemaVersion: "1", SchemaHash: "hash", SourceKind: "manifest", SourceID: "app-a", Agents: []agentsdk.AgentSchema{{Key: "assistant", Name: "Assistant"}}}
	if err := definitions.SyncDefinitions(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := definitions.DefinitionSnapshot(t.Context())
	if err != nil || len(loaded.Agents) != 1 {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	state := binding.AgentStateRepository()
	record := agentstate.AgentStateRecord{WorkspaceID: "workspace-a", Kind: "session", Key: "one", Payload: []byte(`{"archived":false}`), UpdatedAt: 1}
	if err := state.Put(t.Context(), record.WorkspaceID, record); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.Get(t.Context(), record.WorkspaceID, record.Kind, record.Key); err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	tasks := binding.AgentTaskRunRepository()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	run := agentstate.AgentTaskRun{ID: "run-a", WorkspaceID: "workspace-a", TaskKey: "review", TaskVersion: "1", Status: agentstate.AgentTaskRunPending, IdempotencyKey: "idem-a", MaxAttempts: 2, CreatedAt: now, UpdatedAt: now}
	if _, replay, err := tasks.Create(t.Context(), run); err != nil || replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	worker := tasks.(agentrepository.AgentTaskRunSystemWorkerRepository)
	claim, found, err := worker.ClaimNextAgentTaskRunForWorker(t.Context(), agentrepository.SystemScope{Kind: "runtime_global", Purpose: "test"}, "worker", now, time.Minute)
	if err != nil || !found || claim.Run.ID != "run-a" {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	transactional := tasks.(agentrepository.AgentTaskTransactionRepository)
	workflowRun := agentstate.AgentTaskRun{ID: "workflow-run", WorkspaceID: "workspace-a", TaskKey: "workflow.review", TaskVersion: "1", Status: agentstate.AgentTaskRunPending, IdempotencyKey: "workflow-idem", MaxAttempts: 2, CreatedAt: now, UpdatedAt: now}
	payload, _ := json.Marshal(workflowRun)
	mutation := agentrepository.AgentTaskMutation{WorkspaceID: workflowRun.WorkspaceID, RunID: workflowRun.ID, IdempotencyKey: workflowRun.IdempotencyKey, TaskKey: workflowRun.TaskKey, ProcessID: "process-a", Status: string(workflowRun.Status), Payload: payload, CreatedAtMillis: now.UnixMilli(), UpdatedAtMillis: now.UnixMilli()}
	rolledBack := mutation
	rolledBack.RunID = "rolled-back-run"
	rolledBack.IdempotencyKey = "rolled-back-idem"
	rollbackTx, err := host.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transactional.InsertAgentTask(t.Context(), rollbackTx, rolledBack); err != nil {
		_ = rollbackTx.Rollback()
		t.Fatal(err)
	}
	if err := rollbackTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var rolledBackPublications int
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_task_publications WHERE run_id=?`, rolledBack.RunID).Scan(&rolledBackPublications); err != nil || rolledBackPublications != 0 {
		t.Fatalf("rolled back publications=%d err=%v", rolledBackPublications, err)
	}
	tx, err := host.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := transactional.InsertAgentTask(t.Context(), tx, mutation); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, found, err := tasks.Get(t.Context(), workflowRun.WorkspaceID, workflowRun.ID); err != nil || found {
		_ = tx.Rollback()
		t.Fatalf("uncommitted remote task found=%v err=%v", found, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		_, found, err = tasks.Get(t.Context(), workflowRun.WorkspaceID, workflowRun.ID)
		if err == nil && found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("published task found=%v err=%v", found, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	var delivered, attempts int
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*), MAX(attempts) FROM _agent_task_publications WHERE status='delivered'`).Scan(&delivered, &attempts); err != nil || delivered != 1 || attempts < 2 || mutationAttempts.Load() < 2 {
		t.Fatalf("delivered=%d attempts=%d remote attempts=%d err=%v", delivered, attempts, mutationAttempts.Load(), err)
	}
}
