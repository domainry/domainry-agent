package module

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/contracttest"
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/artifactkernel"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedsubjectlifecycle "github.com/domainry/domainry-foundation/subjectlifecycle"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	todomodule "github.com/domainry/domainry-todo/module"
	_ "modernc.org/sqlite"
	"testing"
)

type host struct {
	runtimeID      string
	database       *sql.DB
	dialect        modulehost.Dialect
	applied        []modulehost.SchemaMigration
	owners         []string
	appliedByOwner map[string]bool
	content        *artifactkernel.ContentFiles
}

func testFactory(options Options) *Factory {
	options.KnowledgeFactory = knowledgemodule.NewFactory()
	options.TodoFactory = todomodule.NewFactory()
	return NewFactory(options)
}

func TestModuleMigrationCreatesUnifiedAgentTablesAndIndexes(t *testing.T) {
	host := newHost(t, "unified-runs")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"run_id":"provider","status":"accepted"}}`))
	}))
	defer upstream.Close()
	if _, err := testFactory(Options{BaseURL: upstream.URL, APIKey: "key", AgentID: 1, Client: upstream.Client()}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "unified-runs"}, host); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_agent_runs'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unified run table=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name LIKE 'idx_agent_run_%'`).Scan(&count); err != nil || count != 7 {
		t.Fatalf("unified run indexes=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('_agent_task_runs','_agent_interactive_runs','_agent_conversation_runs')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired run tables=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_agent_run_steps'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unified run-step table=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('_agent_conversation_steps','_agent_conversation_tool_calls','_agent_conversation_step_sources')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired run-step tables=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_agent_tasks'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unified task table=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('_agent_conversation_tasks','_agent_conversation_task_plans','_agent_conversation_follow_up_states','_agent_conversation_follow_up_events')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired task tables=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_agent_peer_links'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unified peer-link table=%d err=%v", count, err)
	}
	if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('_agent_peer_instances','_agent_delegations','_agent_delegation_agreements','_agent_delegation_assignments','_agent_delegation_deliveries','_agent_delegation_disagreements','_agent_delegation_participants','_agent_peer_use_grants')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired peer tables=%d err=%v", count, err)
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
	renderer := dialect.WithSchema("")
	content, err := artifactkernel.NewContentFiles(filepath.Join(t.TempDir(), "shared-artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = content.Close() })
	return &host{runtimeID: runtimeID, database: database, dialect: renderer, content: content}
}

func (h *host) RuntimeID() string                                   { return h.runtimeID }
func (h *host) Database() modulehost.Database                       { return h.database }
func (h *host) Dialect() modulehost.Dialect                         { return h.dialect }
func (h *host) Migrations() modulehost.MigrationRegistrar           { return h }
func (h *host) ArtifactContentStore() sharedartifact.ContentStore   { return h.content }
func (h *host) ArtifactContentWriter() sharedartifact.ContentWriter { return h.content }
func (*host) Driver() string                                        { return "sqlite" }
func (*host) Schema() string                                        { return "" }
func (h *host) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	if owner != "agent" && owner != sharedartifact.MigrationOwner && owner != shareddefinition.MigrationOwner && owner != sharedoperation.MigrationOwner && owner != sharedsubjectlifecycle.MigrationOwner && owner != sharedworkerscope.MigrationOwner && owner != todomodule.MigrationOwner && owner != knowledgemodule.MigrationOwner {
		return &agentsdk.Error{Code: "wrong_owner", Message: owner}
	}
	h.owners = append(h.owners, owner)
	if h.appliedByOwner == nil {
		h.appliedByOwner = map[string]bool{}
	}
	for _, migration := range migrations {
		key := fmt.Sprintf("%s/%d", owner, migration.Version)
		if h.appliedByOwner[key] {
			continue
		}
		for _, statement := range migration.Statements {
			if _, err := h.database.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		h.appliedByOwner[key] = true
		if owner == "agent" {
			h.applied = append(h.applied, migration)
		}
	}
	return nil
}

func TestModuleDescriptor(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"run_id":"provider-1","status":"accepted"}}`))
	}))
	defer upstream.Close()
	host := newHost(t, "runtime")
	b, err := testFactory(Options{BaseURL: upstream.URL, APIKey: "key", AgentID: 1, Client: upstream.Client()}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host)
	if err != nil {
		t.Fatal(err)
	}
	if b.Descriptor().Mode != agentsdk.DeploymentModeModule {
		t.Fatalf("descriptor=%+v", b.Descriptor())
	}
	if len(host.applied) != 14 {
		t.Fatalf("Agent migrations=%d", len(host.applied))
	}
	for _, owner := range []string{sharedoperation.MigrationOwner, sharedworkerscope.MigrationOwner, sharedsubjectlifecycle.MigrationOwner, todomodule.MigrationOwner, knowledgemodule.MigrationOwner, agentstore.MigrationOwner} {
		if !slices.Contains(host.owners, owner) {
			t.Fatalf("module migration owner %q missing from %v", owner, host.owners)
		}
	}
	for _, table := range []string{sharedoperation.TableName, sharedworkerscope.TableName, "_subject_requests", "_subject_steps", "_agent_user_todos", "_agent_knowledge_libraries", "_agent_knowledge_library_members", "_agent_knowledge_documents", "_agent_knowledge_sources", "_agent_knowledge_document_jobs"} {
		var count int
		if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("module-owned table %s count=%d err=%v", table, count, err)
		}
	}
	for _, retired := range []string{"_agent_document_parses", "_agent_owner_operation_receipts", "_agent_collaboration_mutations", "_agent_todo_mutations", "_agent_artifact_mutations", "_knowledge_owner_operation_receipts", "_agent_knowledge_document_sources", "_agent_knowledge_datasource_bindings", "_agent_attachment_knowledge_sources"} {
		var count int
		if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired Knowledge table %s count=%d err=%v", retired, count, err)
		}
	}
	for _, table := range []string{shareddefinition.TableName, shareddefinition.VersionTableName} {
		var count int
		if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("shared Definition table %s count=%d err=%v", table, count, err)
		}
	}
	for _, table := range []string{"_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions", "_agent_entrypoint_definitions", "_agent_service_principal_definitions"} {
		var count int
		if err := host.database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("private Agent Definition table %s count=%d err=%v", table, count, err)
		}
	}
	if latest := host.applied[len(host.applied)-1]; latest.Version != 36 || latest.Name != "agent_scoped_memories" {
		t.Fatalf("Agent latest owned migration=%+v", latest)
	}
	contracttest.VerifyBinding(t, b, agentsdk.DeploymentModeModule)
	subjects, ok := b.(agentlifecycle.SubjectBinding)
	if !ok {
		t.Fatal("Agent module omitted subject lifecycle binding")
	}
	owners := []string{}
	for _, handler := range subjects.LifecycleSubjectHandlers() {
		owners = append(owners, handler.Owner(t.Context()))
	}
	if strings.Join(owners, ",") != "agent,todo,knowledge" {
		t.Fatalf("subject lifecycle owners=%v", owners)
	}
	ctx := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience)
	result, err := b.TaskRunner().Start(ctx, agentsdk.TaskRequest{TaskRunID: "task", WorkspaceID: "workspace", IdempotencyKey: "key", Deadline: time.Now().Add(time.Minute), Input: map[string]any{}, Task: agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true}})
	if err != nil || result.ExternalRunID != "workspace/task" || result.Status != agentsdk.ProviderRunAccepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestModuleCanPublishDefinitionsBeforeProviderConfiguration(t *testing.T) {
	host := newHost(t, "runtime-unconfigured")
	binding, err := testFactory(Options{}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime-unconfigured"}, host)
	if err != nil {
		t.Fatalf("open unconfigured Agent module: %v", err)
	}
	t.Cleanup(func() { _ = binding.Close(t.Context()) })
	if _, err := binding.InteractiveRunner().Run(t.Context(), agentsdk.InteractiveRequest{}); err == nil || !strings.Contains(err.Error(), "provider is not configured") {
		t.Fatalf("unconfigured provider invocation error=%v", err)
	}
}
