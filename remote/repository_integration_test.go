package remote

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentstate "github.com/domainry/domainry-agent-sdk/state"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
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
	if err := agentinfra.EnsureSchema(t.Context(), db, "sqlite", ""); err != nil {
		t.Fatal(err)
	}
	renderer, err := agentinfra.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentinfra.NewAgentStore(db, renderer, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repositories := agentstore.NewRepositories(store)
	runner := repositoryRunner{}
	agentServer, err := server.New(server.Config{APIKey: "secret", Runner: runner, Interactive: runner, Repositories: repositories, Lifecycle: repositories.AgentLifecycleRepository()})
	if err != nil {
		t.Fatal(err)
	}
	repositoryHandler := agentServer.Handler()
	httpServer := httptest.NewServer(repositoryHandler)
	defer httpServer.Close()
	host := newRemoteHost(t, "runtime-a")
	bindingValue, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime-a"}, host)
	if err != nil {
		t.Fatal(err)
	}
	binding := bindingValue.(*binding)
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	definitions := binding.DefinitionRepository()
	snapshot := agentpersistence.DefinitionSnapshot{SchemaVersion: "1", SchemaHash: "hash", SourceKind: "manifest", SourceID: "app-a", Agents: []agentsdk.AgentSchema{{Key: "assistant", Name: "Assistant"}}}
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
	ctx := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience)
	result, err := binding.TaskRunner().Start(ctx, agentsdk.TaskRequest{
		TaskRunID: "run-a", WorkspaceID: "workspace-a", IdempotencyKey: "idem-a", MaxAttempts: 2,
		Task: agentsdk.AgentTaskDefinition{
			ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review",
			InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"},
			AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true,
		},
	})
	if err != nil || result.Status != agentsdk.ProviderRunAccepted {
		t.Fatalf("start=%#v err=%v", result, err)
	}
	run, found, err := tasks.Get(t.Context(), "workspace-a", "run-a")
	if err != nil || !found || run.Status != agentstate.AgentTaskRunPending {
		t.Fatalf("run=%#v found=%v err=%v", run, found, err)
	}
	now := time.Now().UTC().Add(time.Second)
	worker := tasks.(agentpersistence.AgentTaskRunSystemWorkerRepository)
	claim, found, err := worker.ClaimNextAgentTaskRunForWorker(t.Context(), agentpersistence.SystemScope{Kind: agentpersistence.AgentSystemScopeKindGlobal, Purpose: "test"}, "worker", now, time.Minute)
	if err != nil || !found || claim.Run.ID != "run-a" {
		t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
	}
	loadedRun, found, err := tasks.Get(t.Context(), run.WorkspaceID, run.ID)
	if err != nil || !found || loadedRun.ID != run.ID {
		t.Fatalf("loaded=%#v found=%v err=%v", loadedRun, found, err)
	}
}
