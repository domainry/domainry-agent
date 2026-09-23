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
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentcomposition "github.com/domainry/domainry-agent/internal/composition"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	actioncontract "github.com/domainry/domainry-foundation/action"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	"github.com/domainry/domainry-foundation/modulehttp"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	todomodule "github.com/domainry/domainry-todo/module"
)

type ConversationOptions = agentapplication.ConversationOptions
type KnowledgeConfig = provider.KnowledgeConfig
type KnowledgeResponseMapping = provider.KnowledgeResponseMapping
type KnowledgeCitationMapping = provider.KnowledgeCitationMapping

type Options struct {
	AttachmentKnowledge                                    []KnowledgeConfig
	AttachmentKnowledgeBindingsJSON                        string
	KnowledgeDatasources                                   []KnowledgeDatasourceConfig
	KnowledgeDatasourcesJSON                               string
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
	ConversationContextTokenLimit                          int
	ConversationImageInput, ConversationStructuredOutput   bool
	ConversationDisableProtocolContinuation                bool
	ConversationReasoningEfforts                           []string
	ConversationDefaultReasoningEffort                     string
	ConversationProvider                                   agentsdk.ConversationModel
	ConversationOptions                                    ConversationOptions
	TaskModelBaseURL                                       string
	TaskModelURL, TaskModelAPIKey, TaskModelName           string
	TaskModelProviderName, TaskModelProtocol               string
	TaskModelContextTokenLimit                             int
	TaskModelImageInput, TaskModelStructuredOutput         bool
	TaskModelDisableProtocolContinuation                   bool
	TaskModelReasoningEfforts                              []string
	TaskModelDefaultReasoningEffort                        string
	TaskModelProvider                                      agentsdk.ConversationModel
	TaskAttachmentStorage                                  agentsdk.TaskAttachmentStorage
	Knowledge                                              KnowledgeConfig
}

func OptionsFromEnvironment() Options {
	id, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	model := provider.ConversationModelConfigFromEnvironment()
	taskContextLimit, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_TASK_MODEL_CONTEXT_TOKENS")))
	taskEfforts := []string{}
	for _, value := range strings.Split(os.Getenv("AGENT_TASK_MODEL_REASONING_EFFORTS"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			taskEfforts = append(taskEfforts, value)
		}
	}
	taskAPIKey := os.Getenv("AGENT_TASK_MODEL_API_KEY")
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_TASK_MODEL_PROVIDER")), provider.ConversationProviderGateway) && strings.TrimSpace(taskAPIKey) == "" {
		taskAPIKey = os.Getenv("AGENT_PROVIDER_API_KEY")
	}
	conversation := ConversationOptions{
		Workers: positiveEnvironmentInteger("AGENT_CONVERSATION_WORKERS"), MaxParallelTools: positiveEnvironmentInteger("AGENT_CONVERSATION_MAX_PARALLEL_TOOLS"),
		MaxQueuedPerUser: positiveEnvironmentInteger("AGENT_CONVERSATION_MAX_QUEUED_PER_USER"), MaxQueuedPerWorkspace: positiveEnvironmentInteger("AGENT_CONVERSATION_MAX_QUEUED_PER_WORKSPACE"),
		MaxRunningPerUser: positiveEnvironmentInteger("AGENT_CONVERSATION_MAX_RUNNING_PER_USER"), MaxRunningPerWorkspace: positiveEnvironmentInteger("AGENT_CONVERSATION_MAX_RUNNING_PER_WORKSPACE"),
		RunTimeout: positiveEnvironmentDuration("AGENT_CONVERSATION_RUN_TIMEOUT"), ExternalCallTimeout: positiveEnvironmentDuration("AGENT_CONVERSATION_EXTERNAL_CALL_TIMEOUT"),
		MaxModelAttempts: positiveEnvironmentInteger("AGENT_CONVERSATION_MODEL_MAX_ATTEMPTS"), ModelRetryBaseDelay: positiveEnvironmentDuration("AGENT_CONVERSATION_MODEL_RETRY_BASE_DELAY"), ModelRetryMaxDelay: positiveEnvironmentDuration("AGENT_CONVERSATION_MODEL_RETRY_MAX_DELAY"),
	}
	return Options{
		AttachmentKnowledgeBindingsJSON:         os.Getenv("AGENT_ATTACHMENT_KNOWLEDGE_BINDINGS"),
		KnowledgeDatasourcesJSON:                os.Getenv("AGENT_KNOWLEDGE_DATASOURCES"),
		KnowledgeLibraryBindingsJSON:            os.Getenv("AGENT_KNOWLEDGE_LIBRARY_BINDINGS"),
		ConversationEnabled:                     strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_CONVERSATION_ENABLED")), "true"),
		BaseURL:                                 os.Getenv("AGENT_HTTP_BASE_URL"),
		APIKey:                                  os.Getenv("AGENT_HTTP_API_KEY"),
		AgentID:                                 id,
		Timeout:                                 120 * time.Second,
		ConversationTimezone:                    os.Getenv("AGENT_CONVERSATION_TIMEZONE"),
		ConversationBaseURL:                     model.BaseURL,
		ConversationURL:                         model.URL,
		ConversationAPIKey:                      model.APIKey,
		ConversationModel:                       model.Model,
		ConversationProviderName:                model.Provider,
		ConversationProtocol:                    model.Protocol,
		ConversationContextTokenLimit:           model.ContextTokenLimit,
		ConversationImageInput:                  model.ImageInput,
		ConversationStructuredOutput:            model.StructuredOutput,
		ConversationDisableProtocolContinuation: model.DisableProtocolContinuation,
		ConversationReasoningEfforts:            append([]string(nil), model.ReasoningEfforts...),
		ConversationDefaultReasoningEffort:      model.DefaultReasoningEffort,
		ConversationOptions:                     conversation,
		TaskModelBaseURL:                        os.Getenv("AGENT_TASK_MODEL_BASE_URL"),
		TaskModelURL:                            os.Getenv("AGENT_TASK_MODEL_URL"),
		TaskModelAPIKey:                         taskAPIKey,
		TaskModelName:                           os.Getenv("AGENT_TASK_MODEL"),
		TaskModelProviderName:                   os.Getenv("AGENT_TASK_MODEL_PROVIDER"),
		TaskModelProtocol:                       os.Getenv("AGENT_TASK_MODEL_PROTOCOL"),
		TaskModelContextTokenLimit:              taskContextLimit,
		TaskModelImageInput:                     strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_TASK_MODEL_IMAGE_INPUT")), "true"),
		TaskModelStructuredOutput:               strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_TASK_MODEL_STRUCTURED_OUTPUT")), "true"),
		TaskModelDisableProtocolContinuation:    strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_TASK_MODEL_PROTOCOL_CONTINUATION")), "false"),
		TaskModelReasoningEfforts:               taskEfforts,
		TaskModelDefaultReasoningEffort:         os.Getenv("AGENT_TASK_MODEL_REASONING_EFFORT"),
		Knowledge:                               provider.KnowledgeConfigFromEnvironment(),
	}
}

func positiveEnvironmentInteger(name string) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return -1
	}
	return value
}

func positiveEnvironmentDuration(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return -1
	}
	return value
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
	if _, err := sharedoperation.Open(ctx, host.Database(), sharedoperation.AdaptDialect(host.Dialect()), host.Migrations()); err != nil {
		return nil, err
	}
	if _, err := sharedworkerscope.Open(ctx, host.Database(), host.Dialect(), host.Migrations()); err != nil {
		return nil, err
	}
	definitionDialect, ok := host.Dialect().(shareddefinition.Dialect)
	if !ok {
		return nil, fmt.Errorf("Agent Module dialect does not support shared Definitions")
	}
	definitionMigrations, err := shareddefinition.SchemaMigrationsForDialect(definitionDialect)
	if err != nil {
		return nil, err
	}
	if err := host.Migrations().ApplyOwnedMigrations(ctx, shareddefinition.MigrationOwner, definitionMigrations); err != nil {
		return nil, fmt.Errorf("apply shared Definition migrations: %w", err)
	}
	migrations, err := agentstore.SchemaMigrations(host.Migrations().Driver(), host.Migrations().Schema())
	if err != nil {
		return nil, err
	}
	if err := host.Migrations().ApplyOwnedMigrations(ctx, "agent", migrations); err != nil {
		return nil, fmt.Errorf("apply Agent Module migrations: %w", err)
	}
	store, err := agentinfra.NewAgentStore(host.Database(), host.Dialect(), host.Migrations().Driver(), app.RuntimeID)
	if err != nil {
		return nil, err
	}
	if err := knowledgemodule.EnsureSchema(ctx, store, host.Migrations()); err != nil {
		return nil, fmt.Errorf("open Knowledge persistence: %w", err)
	}
	runner := provider.New(provider.Config{BaseURL: f.options.BaseURL, APIKey: f.options.APIKey, AgentID: f.options.AgentID, Timeout: f.options.Timeout, Client: f.options.Client})
	conversationModel := f.options.ConversationProvider
	conversationModelConfig := provider.ConversationModelConfig{Provider: f.options.ConversationProviderName, Protocol: f.options.ConversationProtocol, BaseURL: f.options.ConversationBaseURL, URL: f.options.ConversationURL, APIKey: f.options.ConversationAPIKey, Model: f.options.ConversationModel, ContextTokenLimit: f.options.ConversationContextTokenLimit, ImageInput: f.options.ConversationImageInput, StructuredOutput: f.options.ConversationStructuredOutput, DisableProtocolContinuation: f.options.ConversationDisableProtocolContinuation, ReasoningEfforts: append([]string(nil), f.options.ConversationReasoningEfforts...), DefaultReasoningEffort: f.options.ConversationDefaultReasoningEffort, Client: f.options.Client}
	if conversationModel == nil && conversationModelConfig.Configured() {
		conversationModel, err = provider.NewConversationModel(conversationModelConfig)
		if err != nil {
			return nil, err
		}
	}
	taskModel := f.options.TaskModelProvider
	taskModelConfig := provider.ConversationModelConfig{Provider: f.options.TaskModelProviderName, Protocol: f.options.TaskModelProtocol, BaseURL: f.options.TaskModelBaseURL, URL: f.options.TaskModelURL, APIKey: f.options.TaskModelAPIKey, Model: f.options.TaskModelName, ContextTokenLimit: f.options.TaskModelContextTokenLimit, ImageInput: f.options.TaskModelImageInput, StructuredOutput: f.options.TaskModelStructuredOutput, DisableProtocolContinuation: f.options.TaskModelDisableProtocolContinuation, ReasoningEfforts: append([]string(nil), f.options.TaskModelReasoningEfforts...), DefaultReasoningEffort: f.options.TaskModelDefaultReasoningEffort, Client: f.options.Client}
	if taskModel == nil && taskModelConfig.Configured() {
		taskModel, err = provider.NewConversationModel(taskModelConfig)
		if err != nil {
			return nil, err
		}
	}
	taskRunner := agentsdk.TaskRunner(runner)
	if taskModel != nil {
		taskRunner = provider.NewModelTaskRunner(taskModel)
	}
	binding := newBinding(runner, taskRunner, store, agentsdk.DeploymentModeModule)
	conversationOptions := f.options.ConversationOptions
	if f.options.TaskAttachmentStorage != nil {
		if err := binding.taskExecution.ConfigureAttachments(f.options.TaskAttachmentStorage, app.RuntimeID); err != nil {
			return nil, err
		}
	}
	deferConversations := false
	if deferred, ok := host.(modulehost.DeferredConversationHost); ok {
		deferConversations = deferred.DeferConversationHostBinding()
	}
	if !deferConversations && conversationOptions.PersonalAuthorizer == nil {
		conversationOptions.PersonalAuthorizer, _ = host.(agentsdk.ConversationToolAuthorizer)
	}
	if !deferConversations && conversationOptions.ExecutionAuthorizer == nil {
		conversationOptions.ExecutionAuthorizer, _ = host.(agentsdk.ConversationExecutionAuthorizer)
	}
	if !deferConversations && conversationOptions.CollaborationAuthorizer == nil {
		conversationOptions.CollaborationAuthorizer, _ = host.(agentsdk.ConversationCollaborationAuthorizer)
	}
	conversationRepository := agentstore.NewConversationStore(store)
	artifactHost, artifactHosted := host.(modulehost.ArtifactHost)
	if f.ConversationEnabled() && !artifactHosted {
		return nil, fmt.Errorf("Agent conversations require Artifact content storage")
	}
	if artifactHosted {
		artifacts, openErr := sharedartifact.Open(ctx, host.Database(), host.Dialect(), host.Migrations())
		if openErr != nil {
			return nil, fmt.Errorf("open Agent Artifact persistence: %w", openErr)
		}
		if err := store.BindArtifactPersistence(artifacts, artifactHost.ArtifactContentStore(), artifactHost.ArtifactContentWriter()); err != nil {
			return nil, err
		}
		conversationRepository = agentstore.NewConversationStore(store)
	}
	todoStore, err := todomodule.Open(ctx, store.Database(), store.Renderer(), store.Profile(), host.Migrations(), nil)
	if err != nil {
		return nil, fmt.Errorf("open Todo lifecycle store: %w", err)
	}
	binding.lifecycleSubjects = []lifecyclecontract.SubjectExecutionHandler{
		agentstore.NewSubjectLifecycle(store, app.RuntimeID),
		todomodule.NewSubjectLifecycle(todoStore, app.RuntimeID),
		knowledgemodule.NewSubjectLifecycle(store, app.RuntimeID, knowledgemodule.Options{
			ArtifactStorage: conversationOptions.ArtifactStorage,
			DocumentStorage: conversationOptions.DocumentStorage,
		}),
	}
	if !deferConversations && conversationOptions.ToolHost == nil {
		if authorizer, ok := host.(agentsdk.ConversationToolAuthorizer); ok {
			if _, capable := conversationModel.(agentsdk.ConversationAgentModel); capable {
				conversationOptions.ToolHost, err = agentapplication.NewPersonalConversationHost(conversationRepository, authorizer, f.options.ConversationTimezone)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if !deferConversations && conversationOptions.ToolHost != nil && conversationOptions.ToolAvailability == nil {
		conversationOptions.ToolAvailability, _ = host.(agentsdk.ConversationToolAvailability)
	}
	if f.options.Knowledge.Configured() {
		if conversationOptions.Knowledge != nil {
			return nil, fmt.Errorf("configure only one conversation knowledge source")
		}
		f.options.Knowledge.RuntimeID = app.RuntimeID
		knowledge, err := provider.NewKnowledge(f.options.Knowledge)
		if err != nil {
			return nil, err
		}
		conversationOptions.Knowledge = knowledge
	}
	if err := assembleLibraryKnowledge(&conversationOptions, f.options.KnowledgeLibraries, f.options.KnowledgeLibraryBindingsJSON, f.options.Knowledge, app.RuntimeID); err != nil {
		return nil, err
	}
	if err := assembleKnowledgeDatasources(&conversationOptions, f.options.KnowledgeDatasources, f.options.KnowledgeDatasourcesJSON, app.RuntimeID); err != nil {
		return nil, err
	}
	if err := assembleAttachmentKnowledge(&conversationOptions, f.options.AttachmentKnowledge, f.options.AttachmentKnowledgeBindingsJSON, app.RuntimeID); err != nil {
		return nil, err
	}
	for _, attachment := range conversationOptions.AttachmentKnowledge {
		if attachment.Knowledge == nil {
			return nil, fmt.Errorf("invalid attachment knowledge binding")
		}
		if err := conversationRepository.ActivateAttachmentKnowledgeSource(ctx, app.RuntimeID, attachment.WorkspaceID, attachment.Knowledge.AttachmentKnowledgeSourceIdentity()); err != nil {
			return nil, err
		}
	}
	if f.ConversationEnabled() {
		assembly := &conversationAssembly{repository: conversationRepository, model: conversationModel, runtimeID: app.RuntimeID, timezone: f.options.ConversationTimezone, options: conversationOptions}
		if deferConversations {
			binding.pendingConversations = assembly
		} else if err := binding.openConversations(assembly, nil); err != nil {
			return nil, err
		}
	}
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
	assemblyMu           sync.Mutex
	runner               *provider.Runner
	taskExecution        *agentapplication.TaskExecutionService
	definitions          agentpersistence.DefinitionRepository
	state                agentpersistence.AgentStateRepository
	runs                 agentpersistence.AgentTaskRunRepository
	lifecycle            agentpersistence.AgentLifecycleRepository
	lifecycleSubjects    []lifecyclecontract.SubjectExecutionHandler
	dialogState          agentsdk.AgentDialogStateService
	taskState            agentpersistence.AgentTaskStateService
	interactive          agentpersistence.AgentInteractiveStateService
	adapters             []modulehttp.Adapter
	mode                 agentsdk.DeploymentMode
	conversations        *agentapplication.ConversationService
	conversationAdapter  modulehttp.Adapter
	pendingConversations *conversationAssembly
	applicationHostBound bool
	closed               bool
}

func newBinding(r *provider.Runner, taskRunner agentsdk.TaskRunner, store *agentstore.Store, m agentsdk.DeploymentMode) *binding {
	repositories := agentstore.NewRepositories(store)
	state := repositories.AgentStateRepository()
	runs := repositories.AgentTaskRunRepository()
	interactiveRuns, _ := runs.(agentpersistence.AgentInteractiveRunRepository)
	taskState := agentapplication.NewTaskStateService(runs)
	return &binding{runner: r, taskExecution: agentapplication.NewTaskExecutionService(taskState, taskRunner, ""), definitions: repositories.DefinitionRepository(), state: state, runs: runs, lifecycle: repositories.AgentLifecycleRepository(), dialogState: agentapplication.NewDialogStateService(state), taskState: taskState, interactive: agentapplication.NewInteractiveStateService(interactiveRuns), mode: m}
}
func (b *binding) Descriptor() agentsdk.Descriptor {
	descriptor := agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: b.mode, Capabilities: []string{agentsdk.CapabilityTaskStart, agentsdk.CapabilityTaskPoll, agentsdk.CapabilityTaskCancel, agentsdk.CapabilityInteractiveRun, "dialog.state", "execution.state", "structured_output", "usage", "tool_callback", agentsdk.CapabilityLifecycleExecute}}
	if b.conversations != nil {
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityConversationV1)
	}
	if b.conversations != nil && b.conversations.ConversationStreaming() {
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityConversationStreamV1)
	}
	if b.conversations != nil && b.conversations.ConversationExecutionEnabled() {
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityConversationExecutionV1, agentsdk.CapabilityConversationCollaborationV1)
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityScheduledConversationTask)
		descriptor.Capabilities = append(descriptor.Capabilities, agentsdk.CapabilityBusinessEventConversationTask)
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
func (b *binding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions, err := agentsdk.AgentAuthorizationActions()
	if err != nil {
		return nil, err
	}
	b.assemblyMu.Lock()
	adapters := append([]modulehttp.Adapter(nil), b.adapters...)
	applicationHostBound := b.applicationHostBound
	b.assemblyMu.Unlock()
	mounted := map[string]bool{}
	for _, adapter := range adapters {
		for _, route := range adapter.Routes() {
			mounted[route.Action.Key] = true
		}
	}
	result := make([]actioncontract.ActionDefinition, 0, len(definitions))
	for _, definition := range definitions {
		// The authenticated direct-task ingress is assembled only after Runtime
		// supplies its application authorization host. Keep that Action visible
		// before binding so Runtime cannot mistake the persistence-only adapter
		// for the complete product adapter and skip BindApplicationHost.
		if definition.HTTP == nil || mounted[definition.Key] || (!applicationHostBound && definition.Key == agentsdk.ActionAgentTaskRunsStart) {
			result = append(result, definition)
		}
	}
	return result, nil
}
func (b *binding) BindApplicationHost(host modulehost.ApplicationHost) error {
	b.assemblyMu.Lock()
	defer b.assemblyMu.Unlock()
	if b.closed {
		return fmt.Errorf("Agent Module binding is closed")
	}
	if b.applicationHostBound {
		return nil
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
	b.applicationHostBound = true
	return nil
}
func (b *binding) AgentStateRepository() agentpersistence.AgentStateRepository     { return b.state }
func (b *binding) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository { return b.runs }
func (b *binding) DefinitionRepository() agentpersistence.DefinitionRepository     { return b.definitions }
func (b *binding) LifecycleExecutor(archives lifecyclecontract.ArchiveWriter) lifecyclecontract.OwnerLifecycleExecutor {
	return agentapplication.NewLifecycleExecutor(b.lifecycle, archives)
}
func (b *binding) LifecycleSubjectHandlers() []lifecyclecontract.SubjectExecutionHandler {
	return append([]lifecyclecontract.SubjectExecutionHandler(nil), b.lifecycleSubjects...)
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
var _ agentlifecycle.SubjectBinding = (*binding)(nil)
