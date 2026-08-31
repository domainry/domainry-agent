package module

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/contracttest"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	schemastore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
	"testing"
)

type host struct {
	runtimeID string
	database  *sql.DB
	dialect   modulehost.Dialect
	applied   []modulehost.SchemaMigration
}

func TestModuleMigrationAdoptsLegacyRuntimeAgentIndexes(t *testing.T) {
	host := newHost(t, "legacy-runtime")
	migrations, err := schemastore.SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations[0].Statements {
		if strings.Contains(statement, `CREATE TABLE IF NOT EXISTS "_agent_task_runs"`) {
			if _, err := host.database.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, statement := range []string{
		`CREATE INDEX idx_agent_task_claim ON _agent_task_runs (workspace_id,status,next_attempt_at,lease_expires_at,created_at)`,
		`CREATE INDEX idx_agent_task_process ON _agent_task_runs (workspace_id,process_id,status)`,
		`CREATE INDEX idx_agent_task_key ON _agent_task_runs (workspace_id,task_key,status)`,
	} {
		if _, err := host.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"run_id":"provider","status":"accepted"}}`))
	}))
	defer upstream.Close()
	if _, err := NewFactory(Options{BaseURL: upstream.URL, APIKey: "key", AgentID: 1, Client: upstream.Client()}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "legacy-runtime"}, host); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_agent_owned_task_%_v1'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("owned indexes=%d err=%v", count, err)
	}
}

func newHost(t *testing.T, runtimeID string) *host {
	t.Helper()
	database, err := sql.Open("sqlite", "file:agent-module-"+runtimeID+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	return &host{runtimeID: runtimeID, database: database, dialect: dialect.WithSchema("")}
}

func (h *host) RuntimeID() string                         { return h.runtimeID }
func (h *host) Database() modulehost.Database             { return h.database }
func (h *host) Dialect() modulehost.Dialect               { return h.dialect }
func (h *host) Migrations() modulehost.MigrationRegistrar { return h }
func (*host) Driver() string                              { return "sqlite" }
func (*host) Schema() string                              { return "" }
func (h *host) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	if owner != "agent" {
		return &agentsdk.Error{Code: "wrong_owner", Message: owner}
	}
	for _, migration := range migrations {
		for _, statement := range migration.Statements {
			if _, err := h.database.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	h.applied = append(h.applied, migrations...)
	return nil
}

func TestModuleDescriptor(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"run_id":"provider-1","status":"accepted"}}`))
	}))
	defer upstream.Close()
	host := newHost(t, "runtime")
	b, err := NewFactory(Options{BaseURL: upstream.URL, APIKey: "key", AgentID: 1, Client: upstream.Client()}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host)
	if err != nil {
		t.Fatal(err)
	}
	if b.Descriptor().Mode != agentsdk.DeploymentModeModule {
		t.Fatalf("descriptor=%+v", b.Descriptor())
	}
	if len(host.applied) != 1 {
		t.Fatalf("Agent migrations=%d", len(host.applied))
	}
	contracttest.VerifyBinding(t, b, agentsdk.DeploymentModeModule)
	result, err := b.TaskRunner().Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task", WorkspaceID: "workspace", IdempotencyKey: "key", Deadline: time.Now().Add(time.Minute), Task: agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true}})
	if err != nil || result.ExternalRunID != "workspace/task" || result.Status != agentsdk.ProviderRunAccepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
