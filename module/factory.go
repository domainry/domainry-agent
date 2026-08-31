package module

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentcomposition "github.com/domainry/domainry-agent/internal/composition"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/provider"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

type Options struct {
	BaseURL, APIKey string
	AgentID         int
	Timeout         time.Duration
	Client          *http.Client
}

func OptionsFromEnvironment() Options {
	id, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	return Options{BaseURL: os.Getenv("AGENT_HTTP_BASE_URL"), APIKey: os.Getenv("AGENT_HTTP_API_KEY"), AgentID: id, Timeout: 120 * time.Second}
}

type Factory struct{ options Options }

func NewFactory(options Options) *Factory { return &Factory{options: options} }
func (f *Factory) Open(context.Context, agentsdk.ApplicationRef) (agentsdk.Binding, error) {
	return nil, fmt.Errorf("Agent Module host is required")
}
func (f *Factory) OpenModule(ctx context.Context, app agentsdk.ApplicationRef, host modulehost.Host) (agentsdk.Binding, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	if host == nil || host.RuntimeID() != app.RuntimeID {
		return nil, fmt.Errorf("Agent Module host identity mismatch")
	}
	if host.Database() == nil || host.Dialect() == nil || host.Migrations() == nil {
		return nil, fmt.Errorf("Agent Module persistence host is incomplete")
	}
	migrations, err := agentstore.SchemaMigrations(host.Migrations().Driver(), host.Migrations().Schema())
	if err != nil {
		return nil, err
	}
	if err := host.Migrations().ApplyOwnedMigrations(ctx, "agent", migrations); err != nil {
		return nil, fmt.Errorf("apply Agent Module migrations: %w", err)
	}
	store, err := agentinfra.NewAgentStore(host.Database(), host.Dialect(), host.Migrations().Driver())
	if err != nil {
		return nil, err
	}
	runner := provider.New(provider.Config{BaseURL: f.options.BaseURL, APIKey: f.options.APIKey, AgentID: f.options.AgentID, Timeout: f.options.Timeout, Client: f.options.Client})
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	binding := newBinding(runner, store, agentsdk.DeploymentModeModule)
	binding.taskExecution.StartWorker(ctx)
	surface, err := agenthttp.NewSurface(binding)
	if err != nil {
		return nil, err
	}
	binding.surfaces = []modulehttp.Surface{surface}
	return binding, nil
}

type binding struct {
	runner        *provider.Runner
	taskExecution *agentapplication.TaskExecutionService
	definitions   agentpersistence.DefinitionRepository
	state         agentpersistence.AgentStateRepository
	runs          agentpersistence.AgentTaskRunRepository
	lifecycle     agentpersistence.AgentLifecycleRepository
	dialogState   agentsdk.AgentDialogStateService
	taskState     agentpersistence.AgentTaskStateService
	interactive   agentpersistence.AgentInteractiveStateService
	surfaces      []modulehttp.Surface
	mode          agentsdk.DeploymentMode
}

func newBinding(r *provider.Runner, store *agentstore.Store, m agentsdk.DeploymentMode) *binding {
	repositories := agentstore.NewRepositories(store)
	state := repositories.AgentStateRepository()
	runs := repositories.AgentTaskRunRepository()
	interactiveRuns, _ := runs.(agentpersistence.AgentInteractiveRunRepository)
	taskState := agentapplication.NewTaskStateService(runs)
	return &binding{runner: r, taskExecution: agentapplication.NewTaskExecutionService(taskState, r, ""), definitions: repositories.DefinitionRepository(), state: state, runs: runs, lifecycle: repositories.AgentLifecycleRepository(), dialogState: agentapplication.NewDialogStateService(state), taskState: taskState, interactive: agentapplication.NewInteractiveStateService(interactiveRuns), mode: m}
}
func (b *binding) Descriptor() agentsdk.Descriptor {
	return agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: b.mode, Capabilities: []string{agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, "dialog.state", "execution.state", "structured_output", "usage", "tool_callback", agentsdk.CapabilityLifecycleExecute}}
}
func (b *binding) TaskRunner() agentsdk.TaskRunner                        { return b.taskExecution }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner          { return b.runner }
func (b *binding) DialogState() agentsdk.AgentDialogStateService          { return b.dialogState }
func (b *binding) AgentTaskState() agentpersistence.AgentTaskStateService { return b.taskState }
func (b *binding) AgentInteractiveState() agentpersistence.AgentInteractiveStateService {
	return b.interactive
}
func (b *binding) HTTPSurfaces() []modulehttp.Surface {
	return append([]modulehttp.Surface(nil), b.surfaces...)
}
func (b *binding) BindApplicationHost(host modulehost.ApplicationHost) error {
	ledger, _ := b.runs.(agentpersistence.AgentToolCallLedger)
	surface, err := agentcomposition.BindApplicationSurface(agentcomposition.ApplicationSurfaceDependencies{
		Binding: b, DialogState: b.dialogState, TaskState: b.taskState, InteractiveState: b.interactive,
		ToolLedger: ledger, TaskExecution: b.taskExecution, InteractiveRunner: b.runner, Host: host,
	})
	if err != nil {
		return err
	}
	b.surfaces = []modulehttp.Surface{surface}
	return nil
}
func (b *binding) AgentStateRepository() agentpersistence.AgentStateRepository     { return b.state }
func (b *binding) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository { return b.runs }
func (b *binding) DefinitionRepository() agentpersistence.DefinitionRepository     { return b.definitions }
func (b *binding) LifecycleExecutor(archives lifecyclecontract.ArchiveWriter) lifecyclecontract.OwnerLifecycleExecutor {
	return agentapplication.NewLifecycleExecutor(b.lifecycle, archives)
}
func (b *binding) Close(context.Context) error {
	if b != nil && b.taskExecution != nil {
		b.taskExecution.Close()
	}
	return nil
}

var _ agentsdk.Factory = (*Factory)(nil)
var _ modulehost.Factory = (*Factory)(nil)
var _ modulehost.ApplicationHostBinder = (*binding)(nil)
var _ agentpersistence.Binding = (*binding)(nil)
var _ agentsdk.AgentDialogStateBinding = (*binding)(nil)
var _ agentpersistence.ExecutionStateBinding = (*binding)(nil)
var _ modulehttp.Provider = (*binding)(nil)
var _ agentlifecycle.Binding = (*binding)(nil)
