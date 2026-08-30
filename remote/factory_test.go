package remote

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/contracttest"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/server"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

type host struct {
	runtimeID string
	database  *sql.DB
	dialect   modulehost.Dialect
}

func newRemoteHost(t *testing.T, id string) *host {
	t.Helper()
	db, err := sql.Open("sqlite", "file:agent-remote-host-"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	return &host{runtimeID: id, database: db, dialect: dialect.WithSchema("")}
}
func (h *host) RuntimeID() string                         { return h.runtimeID }
func (h *host) Database() modulehost.Database             { return h.database }
func (h *host) Dialect() modulehost.Dialect               { return h.dialect }
func (h *host) Migrations() modulehost.MigrationRegistrar { return h }
func (*host) Driver() string                              { return "sqlite" }
func (*host) Schema() string                              { return "" }
func (h *host) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	if owner != "agent-saas-adapter" {
		return fmt.Errorf("owner=%s", owner)
	}
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err := h.database.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

type runner struct {
	started     agentsdk.TaskRequest
	interactive agentsdk.InteractiveRequest
}

func (r *runner) Start(_ context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	r.started = request
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunAccepted}, nil
}
func (*runner) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunRunning}, nil
}
func (*runner) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunCancelled}, nil
}
func (r *runner) Run(_ context.Context, request agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	r.interactive = request
	return agentsdk.InteractiveResult{RunID: request.RunID, Status: "completed", Message: "ok", Route: &agentsdk.RouteResult{RouteType: agentsdk.AgentRouteTask, TargetKey: "review"}, Handoff: &agentsdk.Handoff{ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: agentsdk.AgentRouteTask, TargetKey: "review", IdempotencyKey: request.IdempotencyKey, TaskRunID: "task-2"}}, nil
}

func TestSaaSFactoryValidatesDescriptorAndRunsProtocol(t *testing.T) {
	provider := &runner{}
	service := httptest.NewServer(server.New(server.Config{APIKey: "secret", Runner: provider, Interactive: provider}).Handler())
	defer service.Close()
	binding, err := NewFactory(Options{BaseURL: service.URL, APIKey: "secret", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	contracttest.VerifyBinding(t, binding, agentsdk.DeploymentModeSaaS)
	result, err := binding.TaskRunner().Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task-1", WorkspaceID: "workspace", Task: agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true}, IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider-1" || provider.started.TaskRunID != "task-1" || provider.started.Task.AgentKey != "reviewer" || len(provider.started.Task.AllowedOutcomes) != 1 {
		t.Fatalf("result=%+v request=%+v err=%v", result, provider.started, err)
	}
	interactive, err := binding.InteractiveRunner().Run(t.Context(), agentsdk.InteractiveRequest{RunID: "interactive-1", SessionID: "session-1", IdempotencyKey: "interactive-key", Message: "review", Context: agentsdk.GlobalContext{ContractVersion: agentsdk.GlobalAgentContextContractVersion, ContextRevision: "context-1", EntrypointKey: "assistant", AgentKey: "reviewer", Principal: agentsdk.PrincipalReference{WorkspaceID: "workspace", UserID: "user"}}, Candidates: []agentsdk.RouteCandidate{{RouteType: agentsdk.AgentRouteTask, TargetKey: "review"}}})
	if err != nil || interactive.Handoff == nil || interactive.Handoff.TargetKey != "review" || provider.interactive.Context.ContextRevision != "context-1" || provider.interactive.Candidates[0].TargetKey != "review" {
		t.Fatalf("interactive=%+v request=%+v err=%v", interactive, provider.interactive, err)
	}
}

func TestSaaSFactoryFailsClosed(t *testing.T) {
	if _, err := NewFactory(Options{}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime")); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	service := httptest.NewServer(server.New(server.Config{APIKey: "right"}).Handler())
	defer service.Close()
	if _, err := NewFactory(Options{BaseURL: service.URL, APIKey: "wrong", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime")); err == nil {
		t.Fatal("invalid credential accepted")
	}
}
