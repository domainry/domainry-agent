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
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentrepository "github.com/domainry/domainry-agent-sdk/repository"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/provider"
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
	store, err := agentstore.NewStore(host.Database(), host.Dialect(), host.Migrations().Driver())
	if err != nil {
		return nil, err
	}
	runner := provider.New(provider.Config{BaseURL: f.options.BaseURL, APIKey: f.options.APIKey, AgentID: f.options.AgentID, Timeout: f.options.Timeout, Client: f.options.Client})
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	return newBinding(runner, store, agentsdk.DeploymentModeModule), nil
}

type binding struct {
	runner      *provider.Runner
	definitions agentrepository.DefinitionRepository
	state       agentrepository.AgentStateRepository
	runs        agentrepository.AgentTaskRunRepository
	lifecycle   agentrepository.AgentLifecycleRepository
	mode        agentsdk.DeploymentMode
}

func newBinding(r *provider.Runner, store *agentstore.Store, m agentsdk.DeploymentMode) *binding {
	repositories := agentstore.NewRepositories(store)
	return &binding{runner: r, definitions: repositories.DefinitionRepository(), state: repositories.AgentStateRepository(), runs: repositories.AgentTaskRunRepository(), lifecycle: repositories.AgentLifecycleRepository(), mode: m}
}
func (b *binding) Descriptor() agentsdk.Descriptor {
	return agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: b.mode, Capabilities: []string{"task.start", "task.poll", "task.cancel", "interactive.run", "structured_output", "usage", "tool_callback"}}
}
func (b *binding) TaskRunner() agentsdk.TaskRunner                                { return b.runner }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner                  { return b.runner }
func (b *binding) AgentStateRepository() agentrepository.AgentStateRepository     { return b.state }
func (b *binding) AgentTaskRunRepository() agentrepository.AgentTaskRunRepository { return b.runs }
func (b *binding) DefinitionRepository() agentrepository.DefinitionRepository     { return b.definitions }
func (b *binding) AgentLifecycleRepository() agentrepository.AgentLifecycleRepository {
	return b.lifecycle
}
func (*binding) Close(context.Context) error { return nil }

var _ agentsdk.Factory = (*Factory)(nil)
var _ modulehost.Factory = (*Factory)(nil)
var _ agentrepository.Binding = (*binding)(nil)
