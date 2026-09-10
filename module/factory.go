package module

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentcapability "github.com/domainry/domainry-agent/capability"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentcomposition "github.com/domainry/domainry-agent/internal/composition"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
)

type ConversationOptions = agentapplication.ConversationOptions
type KnowledgeConfig = provider.KnowledgeConfig
type KnowledgeResponseMapping = provider.KnowledgeResponseMapping
type KnowledgeCitationMapping = provider.KnowledgeCitationMapping

type Options struct {
	KnowledgeLibraries                                     []KnowledgeLibraryConfig
	KnowledgeLibraryBindingsJSON                           string
	ConversationEnabled                                    bool
	ConversationTimezone                                   string
	ConversationBaseURL                                    string
	BaseURL, APIKey                                        string
	AgentID                                                int
	Timeout                                                time.Duration
	Client                                                 *http.Client
	ConversationURL, ConversationAPIKey, ConversationModel string
	ConversationProviderName, ConversationProtocol         string
	ConversationProvider                                   agentsdk.ConversationModel
	ConversationOptions                                    ConversationOptions
	Knowledge                                              KnowledgeConfig
}

func OptionsFromEnvironment() Options {
	id, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	model := provider.ConversationModelConfigFromEnvironment()
	return Options{KnowledgeLibraryBindingsJSON: os.Getenv("AGENT_KNOWLEDGE_LIBRARY_BINDINGS"), ConversationEnabled: strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_CONVERSATION_ENABLED")), "true"), BaseURL: os.Getenv("AGENT_HTTP_BASE_URL"), APIKey: os.Getenv("AGENT_HTTP_API_KEY"), AgentID: id, Timeout: 120 * time.Second, ConversationTimezone: os.Getenv("AGENT_CONVERSATION_TIMEZONE"), ConversationBaseURL: model.BaseURL, ConversationURL: model.URL, ConversationAPIKey: model.APIKey, ConversationModel: model.Model, ConversationProviderName: model.Provider, ConversationProtocol: model.Protocol, Knowledge: provider.KnowledgeConfigFromEnvironment()}
}

type Factory struct{ options Options }

func NewFactory(options Options) *Factory { return &Factory{options: options} }

func (f *Factory) ConversationEnabled() bool {
	if f == nil {
		return false
	}
	return f.options.ConversationEnabled || f.options.ConversationProvider != nil || (provider.ConversationModelConfig{Provider: f.options.ConversationProviderName, Protocol: f.options.ConversationProtocol, BaseURL: f.options.ConversationBaseURL, URL: f.options.ConversationURL, APIKey: f.options.ConversationAPIKey, Model: f.options.ConversationModel}).Configured()
}
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
	capabilityBinding, err := agentcapability.Open(agentcapability.Inputs{})
	if err != nil {
		return nil, fmt.Errorf("build Agent capability binding: %w", err)
	}
	binding := newBinding(runner, store, agentsdk.DeploymentModeModule)
	model := f.options.ConversationProvider
	modelConfig := provider.ConversationModelConfig{Provider: f.options.ConversationProviderName, Protocol: f.options.ConversationProtocol, BaseURL: f.options.ConversationBaseURL, URL: f.options.ConversationURL, APIKey: f.options.ConversationAPIKey, Model: f.options.ConversationModel, Client: f.options.Client}
	if model == nil && modelConfig.Configured() {
		model, err = provider.NewConversationModel(modelConfig)
		if err != nil {
			return nil, err
		}
	}
	conversationOptions := f.options.ConversationOptions
	deferConversations := false
	if deferred, ok := host.(modulehost.DeferredConversationHost); ok {
		deferConversations = deferred.DeferConversationHostBinding()
	}
	if conversationOptions.PersonalAuthorizer == nil {
		conversationOptions.PersonalAuthorizer, _ = host.(agentsdk.ConversationToolAuthorizer)
	}
	conversationRepository := agentstore.NewConversationStore(store)
	if conversationOptions.ToolHost == nil {
		if authorizer, ok := host.(agentsdk.ConversationToolAuthorizer); ok {
			if _, capable := model.(agentsdk.ConversationAgentModel); capable {
				conversationOptions.ToolHost, err = agentapplication.NewPersonalConversationHost(conversationRepository, authorizer, f.options.ConversationTimezone)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if conversationOptions.ToolHost != nil && conversationOptions.ToolAvailability == nil {
		conversationOptions.ToolAvailability, _ = host.(agentsdk.ConversationToolAvailability)
	}
	if f.options.Knowledge.Configured() {
		if conversationOptions.Knowledge != nil {
			return nil, fmt.Errorf("configure only one conversation knowledge source")
		}
		knowledge, err := provider.NewKnowledge(f.options.Knowledge)
		if err != nil {
			return nil, err
		}
		conversationOptions.Knowledge = knowledge
	}
	if err := assembleLibraryKnowledge(&conversationOptions, f.options.KnowledgeLibraries, f.options.KnowledgeLibraryBindingsJSON, f.options.Knowledge); err != nil {
		return nil, err
	}
	assembly := &conversationAssembly{repository: conversationRepository, model: model, runtimeID: app.RuntimeID, timezone: f.options.ConversationTimezone, options: conversationOptions}
	if deferConversations {
		binding.pendingConversations = assembly
	} else if err := binding.openConversations(assembly, nil); err != nil {
		return nil, err
	}
	binding.Binding = capabilityBinding
	binding.taskExecution.StartWorker(ctx)
	adapter, err := agenthttp.NewAdapter(binding)
	if err != nil {
		_ = binding.Close(ctx)
		return nil, err
	}
	binding.adapters = []modulehttp.Adapter{adapter}
	if binding.conversationAdapter != nil {
		binding.adapters = append(binding.adapters, binding.conversationAdapter)
	}
	return binding, nil
}

type binding struct {
	modulecapability.Binding
	assemblyMu           sync.Mutex
	runner               *provider.Runner
	taskExecution        *agentapplication.TaskExecutionService
	definitions          agentpersistence.DefinitionRepository
	state                agentpersistence.AgentStateRepository
	runs                 agentpersistence.AgentTaskRunRepository
	lifecycle            agentpersistence.AgentLifecycleRepository
	dialogState          agentsdk.AgentDialogStateService
	taskState            agentpersistence.AgentTaskStateService
	interactive          agentpersistence.AgentInteractiveStateService
	adapters             []modulehttp.Adapter
	mode                 agentsdk.DeploymentMode
	conversations        *agentapplication.ConversationService
	conversationAdapter  modulehttp.Adapter
	pendingConversations *conversationAssembly
	closed               bool
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
	descriptor := agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: b.mode, Capabilities: []string{agentsdk.CapabilityConversationV1, agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, "dialog.state", "execution.state", "structured_output", "usage", "tool_callback", agentsdk.CapabilityLifecycleExecute}}
	if b.conversations != nil && b.conversations.ConversationStreaming() {
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityConversationStreamV1)
	}
	if b.conversations != nil && b.conversations.ConversationExecutionEnabled() {
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityConversationExecutionV1)
	}
	return descriptor
}
func (b *binding) TaskRunner() agentsdk.TaskRunner                        { return b.taskExecution }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner          { return b.runner }
func (b *binding) DialogState() agentsdk.AgentDialogStateService          { return b.dialogState }
func (b *binding) AgentTaskState() agentpersistence.AgentTaskStateService { return b.taskState }
func (b *binding) AgentInteractiveState() agentpersistence.AgentInteractiveStateService {
	return b.interactive
}
func (b *binding) HTTPAdapters() []modulehttp.Adapter {
	return append([]modulehttp.Adapter(nil), b.adapters...)
}
func (*binding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	return agentsdk.AgentAuthorizationActions()
}
func (b *binding) BindApplicationHost(host modulehost.ApplicationHost) error {
	b.assemblyMu.Lock()
	defer b.assemblyMu.Unlock()
	if b.closed {
		return fmt.Errorf("Agent Module binding is closed")
	}
	var conversationHost modulehost.ConversationApplicationHost
	if b.pendingConversations != nil {
		var ok bool
		conversationHost, ok = host.(modulehost.ConversationApplicationHost)
		if !ok || conversationHost.ConversationAuthorizer() == nil {
			return fmt.Errorf("deferred Agent conversations require a current application authorizer")
		}
	}
	ledger, _ := b.runs.(agentpersistence.AgentToolCallLedger)
	adapter, err := agentcomposition.BindApplicationAdapter(agentcomposition.ApplicationAdapterDependencies{
		Binding: b, DialogState: b.dialogState, TaskState: b.taskState, InteractiveState: b.interactive,
		ToolLedger: ledger, TaskExecution: b.taskExecution, InteractiveRunner: b.runner, Host: host,
	})
	if err != nil {
		return err
	}
	if b.pendingConversations != nil {
		if err := b.openConversations(b.pendingConversations, conversationHost); err != nil {
			return err
		}
		b.pendingConversations = nil
	}
	b.adapters = []modulehttp.Adapter{adapter}
	if b.conversationAdapter != nil {
		b.adapters = append(b.adapters, b.conversationAdapter)
	}
	return nil
}
func (b *binding) AgentStateRepository() agentpersistence.AgentStateRepository     { return b.state }
func (b *binding) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository { return b.runs }
func (b *binding) DefinitionRepository() agentpersistence.DefinitionRepository     { return b.definitions }
func (b *binding) LifecycleExecutor(archives lifecyclecontract.ArchiveWriter) lifecyclecontract.OwnerLifecycleExecutor {
	return agentapplication.NewLifecycleExecutor(b.lifecycle, archives)
}
func (b *binding) Conversations() agentsdk.ConversationService {
	if b.conversations == nil {
		return nil
	}
	return b.conversations
}
func (b *binding) Close(context.Context) error {
	if b == nil {
		return nil
	}
	b.assemblyMu.Lock()
	defer b.assemblyMu.Unlock()
	b.closed = true
	b.pendingConversations = nil
	if b != nil && b.conversations != nil {
		b.conversations.Close()
	}
	if b != nil && b.taskExecution != nil {
		b.taskExecution.Close()
	}
	return nil
}

var _ agentsdk.Factory = (*Factory)(nil)
var _ agentsdk.Binding = (*binding)(nil)
var _ modulehost.Factory = (*Factory)(nil)
var _ modulehost.ApplicationHostBinder = (*binding)(nil)
var _ agentpersistence.Binding = (*binding)(nil)
var _ agentsdk.AgentDialogStateBinding = (*binding)(nil)
var _ agentpersistence.ExecutionStateBinding = (*binding)(nil)
var _ modulehttp.Provider = (*binding)(nil)
var _ actioncontract.Provider = (*binding)(nil)
var _ agentlifecycle.Binding = (*binding)(nil)
