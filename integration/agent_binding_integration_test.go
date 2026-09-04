package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentcontracttest "github.com/domainry/domainry-agent-sdk/contracttest"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent-sdk/saashost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	agentprovider "github.com/domainry/domainry-agent/internal/provider"
	agentmodule "github.com/domainry/domainry-agent/module"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	"github.com/domainry/domainry-foundation/modulecapability"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

const (
	providerAPIKey       = "provider-api-secret"
	callerCredential     = "caller-forged-credential"
	hostCredentialPrefix = "host-issued-credential:"
)

func TestPublicAgentBindingPersistsRecoversAndExecutesWithHostAuthority(t *testing.T) {
	semantics := map[agentsdk.DeploymentMode]taskSemantics{}
	for _, mode := range []agentsdk.DeploymentMode{agentsdk.DeploymentModeModule, agentsdk.DeploymentModeSaaS} {
		t.Run(string(mode), func(t *testing.T) {
			harness := newBindingHarness(t, mode)
			request := taskRequest("recover-run", "workspace-a", 2)

			first := harness.open(t)
			agentcontracttest.VerifyBinding(t, first, mode)
			if result, err := first.TaskRunner().Start(t.Context(), request); sdkErrorCode(err) != "agent.authorization.service_action_denied" || result.ErrorClass != "authorization" {
				t.Fatalf("unauthorized Start result=%+v err=%v", result, err)
			}
			result, err := first.TaskRunner().Start(serviceContext(t.Context(), agentsdk.ActionAgentTaskExecutionStart), request)
			if err != nil || result.Status != agentsdk.ProviderRunAccepted || result.ExternalRunID != "workspace-a/recover-run" {
				t.Fatalf("initial Start result=%+v err=%v", result, err)
			}
			assertStoredTask(t, first, "workspace-a", "recover-run", agentmodel.AgentTaskRunPending)
			if err := first.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls := harness.external.startCount("recover-run"); calls != 0 {
				t.Fatalf("provider started before a TaskHost was bound: %d", calls)
			}

			second := harness.open(t)
			t.Cleanup(func() { _ = second.Close(context.Background()) })
			state := second.(agentpersistence.ExecutionStateBinding).AgentTaskState()
			host := newTaskHost(state)
			bindApplicationHost(t, second, host)
			assertStoredTask(t, second, "workspace-a", "recover-run", agentmodel.AgentTaskRunPending)

			const submitters = 8
			var wait sync.WaitGroup
			errorsBySubmitter := make(chan error, submitters)
			for index := 0; index < submitters; index++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					result, err := second.TaskRunner().Start(serviceContext(context.Background(), agentsdk.ActionAgentTaskExecutionStart), request)
					if err != nil {
						errorsBySubmitter <- err
						return
					}
					if result.ExternalRunID != "workspace-a/recover-run" {
						errorsBySubmitter <- fmt.Errorf("duplicate locator=%q", result.ExternalRunID)
					}
				}()
			}
			wait.Wait()
			close(errorsBySubmitter)
			for err := range errorsBySubmitter {
				t.Error(err)
			}
			harness.external.releaseSuccess()

			result = waitForTaskResult(t, second.TaskRunner(), "workspace-a/recover-run", "command-recover-run")
			if result.Status != agentsdk.ProviderRunCompleted || result.Outcome != "success" || result.Output["result"] != "ok" {
				t.Fatalf("terminal result=%+v", result)
			}
			callback := host.waitForCompletion(t)
			if !callback.persistedBeforeCallback || callback.value.Status != string(agentmodel.AgentTaskRunSucceeded) {
				t.Fatalf("completion callback=%+v", callback)
			}

			run := waitForStoredTask(t, second, "workspace-a", "recover-run", func(run agentmodel.AgentTaskRun) bool {
				return run.Status == agentmodel.AgentTaskRunSucceeded && !run.Reconciliation.Required && run.Reconciliation.State == "workflow_callback_delivered"
			})
			if run.Status != agentmodel.AgentTaskRunSucceeded || run.Attempt != 1 || run.Revision < 4 || run.Reconciliation.Required || run.Reconciliation.State != "workflow_callback_delivered" {
				t.Fatalf("persisted terminal run=%+v", run)
			}
			if !containsAuthorizationRevision(run.Evidence.Authorization, "host-auth-revision") {
				t.Fatalf("host authorization evidence was not persisted: %+v", run.Evidence.Authorization)
			}
			raw, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), callerCredential) || strings.Contains(string(raw), hostCredentialPrefix) {
				t.Fatalf("credential leaked into Agent task state: %s", raw)
			}
			providerRequest := harness.external.requestFor("recover-run")
			metadata, _ := providerRequest.payload["metadata"].(map[string]any)
			if providerRequest.authorization != "Bearer "+providerAPIKey || metadata["execution_credential"] != hostCredentialPrefix+"workspace-a" || metadata["execution_credential"] == callerCredential {
				t.Fatalf("provider request crossed the wrong credential boundary: auth=%q metadata=%+v", providerRequest.authorization, metadata)
			}
			if authorizations, credentials := host.callCounts(); authorizations != 1 || credentials != 1 {
				t.Fatalf("host calls authorize=%d credential=%d", authorizations, credentials)
			}
			if calls := harness.external.startCount("recover-run"); calls != 1 {
				t.Fatalf("concurrent idempotent submissions started provider %d times", calls)
			}

			wrongWorkspace := waitForSinglePoll(t, second.TaskRunner(), "workspace-b/recover-run", "command-recover-run")
			if wrongWorkspace.Status != agentsdk.ProviderRunUnknown || wrongWorkspace.ErrorCode != "agent.task.not_found" {
				t.Fatalf("cross-workspace poll=%+v", wrongWorkspace)
			}
			if _, found, err := second.(agentpersistence.Binding).AgentTaskRunRepository().Get(t.Context(), "workspace-b", "recover-run"); err != nil || found {
				t.Fatalf("cross-workspace repository read found=%t err=%v", found, err)
			}

			semantics[mode] = taskSemantics{Status: result.Status, Outcome: result.Outcome, Output: result.Output, Attempts: run.Attempt, State: run.Status, CallbackStatus: callback.value.Status}
		})
	}
	if !reflect.DeepEqual(semantics[agentsdk.DeploymentModeModule], semantics[agentsdk.DeploymentModeSaaS]) {
		t.Fatalf("Module/SaaS task semantics differ: module=%+v saas=%+v", semantics[agentsdk.DeploymentModeModule], semantics[agentsdk.DeploymentModeSaaS])
	}
}

func TestPublicAgentBindingRetriesAndPreservesProviderFailureCodes(t *testing.T) {
	for _, mode := range []agentsdk.DeploymentMode{agentsdk.DeploymentModeModule, agentsdk.DeploymentModeSaaS} {
		t.Run(string(mode), func(t *testing.T) {
			harness := newBindingHarness(t, mode)
			binding := harness.open(t)
			t.Cleanup(func() { _ = binding.Close(context.Background()) })
			host := newTaskHost(binding.(agentpersistence.ExecutionStateBinding).AgentTaskState())
			bindApplicationHost(t, binding, host)

			retryRequest := taskRequest("retry-run", "workspace-a", 2)
			if result, err := binding.TaskRunner().Start(serviceContext(t.Context(), agentsdk.ActionAgentTaskExecutionStart), retryRequest); err != nil || result.Status != agentsdk.ProviderRunAccepted {
				t.Fatalf("retry Start result=%+v err=%v", result, err)
			}
			retryResult := waitForTaskResult(t, binding.TaskRunner(), "workspace-a/retry-run", retryRequest.IdempotencyKey)
			retryRun := loadStoredTask(t, binding, "workspace-a", "retry-run")
			if retryResult.Status != agentsdk.ProviderRunCompleted || retryRun.Status != agentmodel.AgentTaskRunSucceeded || retryRun.Attempt != 2 || len(retryRun.Attempts) != 2 || retryRun.Attempts[0].ErrorCode != "agent.runner.provider_http_429" || !retryRun.Attempts[0].Retryable {
				t.Fatalf("retry result=%+v run=%+v", retryResult, retryRun)
			}
			if calls := harness.external.startCount("retry-run"); calls != 2 {
				t.Fatalf("retry provider starts=%d", calls)
			}

			failureRequest := taskRequest("failure-run", "workspace-a", 1)
			if _, err := binding.TaskRunner().Start(serviceContext(t.Context(), agentsdk.ActionAgentTaskExecutionStart), failureRequest); err != nil {
				t.Fatal(err)
			}
			failureResult := waitForTaskResult(t, binding.TaskRunner(), "workspace-a/failure-run", failureRequest.IdempotencyKey)
			failureRun := loadStoredTask(t, binding, "workspace-a", "failure-run")
			if failureResult.Status != agentsdk.ProviderRunFailed || failureResult.ErrorCode != "agent.runner.provider_http_422" || failureRun.Status != agentmodel.AgentTaskRunDeadLetter || failureRun.LastErrorCode != failureResult.ErrorCode || failureRun.Attempt != 1 {
				t.Fatalf("failure result=%+v run=%+v", failureResult, failureRun)
			}
		})
	}
}

func TestModuleAndSaaSBindingsExposeIdenticalCapabilityAndValidation(t *testing.T) {
	moduleHarness := newBindingHarness(t, agentsdk.DeploymentModeModule)
	remoteHarness := newBindingHarness(t, agentsdk.DeploymentModeSaaS)
	moduleBinding, remoteBinding := moduleHarness.open(t), remoteHarness.open(t)
	t.Cleanup(func() {
		_ = moduleBinding.Close(context.Background())
		_ = remoteBinding.Close(context.Background())
	})

	moduleSummary, err := moduleBinding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	remoteSummary, err := remoteBinding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalEqual(t, moduleSummary, remoteSummary)
	if moduleSummary.Identity.ContractSHA256 == "" || moduleSummary.Identity.ContractSHA256 != remoteSummary.Identity.ContractSHA256 {
		t.Fatalf("capability digests module=%q remote=%q", moduleSummary.Identity.ContractSHA256, remoteSummary.Identity.ContractSHA256)
	}
	for _, category := range moduleSummary.Categories {
		direct, err := moduleBinding.CapabilityCategory(t.Context(), category.Key)
		if err != nil {
			t.Fatal(err)
		}
		viaSaaS, err := remoteBinding.CapabilityCategory(t.Context(), category.Key)
		if err != nil {
			t.Fatal(err)
		}
		assertCanonicalEqual(t, direct, viaSaaS)
	}

	request := modulecapability.ValidationRequest{
		ContractVersion: modulecapability.ValidationContractVersion,
		ModuleKey:       "agent",
		CategoryKey:     "agent.authoring",
		ContractSHA256:  moduleSummary.Identity.ContractSHA256,
		Kind:            "agent.skill",
		Candidate: modulecapability.AuthoringFragment{
			Collection: "skills",
			Key:        "customer_reader",
			Value:      json.RawMessage(`{"key":"customer_reader","name":"Customer reader","allowed_tools":["query_records"]}`),
		},
	}
	directResult, directErr := moduleBinding.ValidateCapabilityCandidate(t.Context(), request)
	remoteResult, remoteErr := remoteBinding.ValidateCapabilityCandidate(t.Context(), request)
	if fmt.Sprint(directErr) != fmt.Sprint(remoteErr) {
		t.Fatalf("validation errors differ: module=%v remote=%v", directErr, remoteErr)
	}
	assertCanonicalEqual(t, directResult, remoteResult)
	if len(directResult.Diagnostics) != 1 || directResult.Diagnostics[0].RuleKey != "agent.skill.invalid" {
		t.Fatalf("validation diagnostics=%+v", directResult.Diagnostics)
	}
}

type taskSemantics struct {
	Status         agentsdk.ProviderRunStatus
	Outcome        string
	Output         map[string]any
	Attempts       int
	State          agentmodel.AgentTaskRunStatus
	CallbackStatus string
}

type bindingHarness struct {
	mode       agentsdk.DeploymentMode
	external   *externalProvider
	moduleHost *sqliteModuleHost
	remoteURL  string
	remoteHTTP *http.Client
}

func newBindingHarness(t *testing.T, mode agentsdk.DeploymentMode) *bindingHarness {
	t.Helper()
	external := newExternalProvider(t)
	harness := &bindingHarness{mode: mode, external: external}
	if mode == agentsdk.DeploymentModeModule {
		harness.moduleHost = newSQLiteModuleHost(t, "runtime-integration")
		return harness
	}

	database := openSQLite(t, "saas")
	if err := agentinfra.EnsureSchema(t.Context(), database, "sqlite", ""); err != nil {
		t.Fatal(err)
	}
	renderer, err := agentinfra.Renderer("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := agentinfra.NewAgentStore(database, renderer, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repositories := agentstore.NewRepositories(store)
	runner := agentprovider.New(agentprovider.Config{BaseURL: external.url(), APIKey: providerAPIKey, AgentID: 41, Timeout: 10 * time.Second, Client: external.client()})
	service, err := agentserver.New(agentserver.Config{
		APIKey: "saas-api-secret", Runner: runner, Interactive: runner,
		DialogState:  agentapplication.NewDialogStateService(repositories.AgentStateRepository()),
		Repositories: repositories, Lifecycle: repositories.AgentLifecycleRepository(),
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(service.Handler())
	t.Cleanup(httpServer.Close)
	harness.remoteURL, harness.remoteHTTP = httpServer.URL, httpServer.Client()
	return harness
}

func (h *bindingHarness) open(t *testing.T) agentsdk.Binding {
	t.Helper()
	var (
		binding agentsdk.Binding
		err     error
	)
	if h.mode == agentsdk.DeploymentModeModule {
		binding, err = agentmodule.NewFactory(agentmodule.Options{BaseURL: h.external.url(), APIKey: providerAPIKey, AgentID: 41, Timeout: 10 * time.Second, Client: h.external.client()}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime-integration"}, h.moduleHost)
	} else {
		binding, err = agentremote.NewFactory(agentremote.Options{BaseURL: h.remoteURL, APIKey: "saas-api-secret", Timeout: 10 * time.Second, Client: h.remoteHTTP}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime-integration"}, saasRuntimeHost("runtime-integration"))
	}
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

type saasRuntimeHost string

func (h saasRuntimeHost) RuntimeID() string { return string(h) }

var _ saashost.Host = saasRuntimeHost("")

type sqliteModuleHost struct {
	runtimeID string
	database  *sql.DB
	dialect   modulehost.Dialect
	mu        sync.Mutex
	applied   map[string]struct{}
}

func newSQLiteModuleHost(t *testing.T, runtimeID string) *sqliteModuleHost {
	t.Helper()
	database := openSQLite(t, "module")
	dialect, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	return &sqliteModuleHost{runtimeID: runtimeID, database: database, dialect: dialect.WithSchema(""), applied: map[string]struct{}{}}
}

func (h *sqliteModuleHost) RuntimeID() string                         { return h.runtimeID }
func (h *sqliteModuleHost) Database() modulehost.Database             { return h.database }
func (h *sqliteModuleHost) Dialect() modulehost.Dialect               { return h.dialect }
func (h *sqliteModuleHost) Migrations() modulehost.MigrationRegistrar { return h }
func (*sqliteModuleHost) Driver() string                              { return "sqlite" }
func (*sqliteModuleHost) Schema() string                              { return "" }
func (h *sqliteModuleHost) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	if owner != "agent" {
		return fmt.Errorf("unexpected migration owner %q", owner)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, migration := range migrations {
		key := fmt.Sprintf("%s:%d", owner, migration.Version)
		if _, found := h.applied[key]; found {
			continue
		}
		for _, statement := range migration.Statements {
			if _, err := h.database.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		h.applied[key] = struct{}{}
	}
	return nil
}

func openSQLite(t *testing.T, suffix string) *sql.DB {
	t.Helper()
	name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	database, err := sql.Open("sqlite", "file:"+name+"-"+suffix+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

type capturedProviderRequest struct {
	authorization string
	payload       map[string]any
}

type externalProvider struct {
	mu          sync.Mutex
	starts      map[string]int
	requests    map[string]capturedProviderRequest
	successGate chan struct{}
	releaseOnce sync.Once
	server      *httptest.Server
}

func newExternalProvider(t *testing.T) *externalProvider {
	t.Helper()
	fixture := &externalProvider{starts: map[string]int{}, requests: map[string]capturedProviderRequest{}, successGate: make(chan struct{})}
	fixture.server = httptest.NewServer(fixture)
	t.Cleanup(func() {
		fixture.releaseSuccess()
		fixture.server.Close()
	})
	return fixture
}

func (p *externalProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && r.URL.Path == "/agent/v1/agent-runs" {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		metadata, _ := payload["metadata"].(map[string]any)
		runID := strings.TrimSpace(fmt.Sprint(metadata["task_run_id"]))
		p.mu.Lock()
		p.starts[runID]++
		attempt := p.starts[runID]
		p.requests[runID] = capturedProviderRequest{authorization: r.Header.Get("Authorization"), payload: payload}
		p.mu.Unlock()
		switch runID {
		case "recover-run":
			select {
			case <-p.successGate:
			case <-r.Context().Done():
				return
			}
		case "retry-run":
			if attempt == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"data":{"run_id":"provider-retry-1","status":"failed"}}`))
				return
			}
		case "failure-run":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"data":{"run_id":"provider-failure-1","status":"failed"}}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":{"run_id":"provider-%s-%d","status":"accepted"}}`, runID, attempt)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/agent/v1/agent-runs/provider-") {
		_, _ = w.Write([]byte(`{"data":{"status":"completed","outcome":"success","output":{"result":"ok"},"model":"integration-model","usage":{"tokens":3}}}`))
		return
	}
	http.NotFound(w, r)
}

func (p *externalProvider) releaseSuccess() { p.releaseOnce.Do(func() { close(p.successGate) }) }
func (p *externalProvider) url() string     { return p.server.URL }
func (p *externalProvider) client() *http.Client {
	return p.server.Client()
}
func (p *externalProvider) startCount(runID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts[runID]
}
func (p *externalProvider) requestFor(runID string) capturedProviderRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[runID]
}

type taskHost struct {
	mu                 sync.Mutex
	state              agentpersistence.AgentTaskStateService
	authorizationCalls int
	credentialCalls    int
	completions        chan completionObservation
}

type completionObservation struct {
	value                   modulehost.WorkflowTaskCompletion
	persistedBeforeCallback bool
	err                     error
}

func newTaskHost(state agentpersistence.AgentTaskStateService) *taskHost {
	return &taskHost{state: state, completions: make(chan completionObservation, 8)}
}

func (h *taskHost) AuthorizeTask(_ context.Context, request modulehost.TaskAuthorizationRequest) (modulehost.TaskAuthorization, error) {
	h.mu.Lock()
	h.authorizationCalls++
	h.mu.Unlock()
	if request.WorkspaceID == "" || request.Identity.Initiator.WorkspaceID != request.WorkspaceID || request.Identity.Execution.WorkspaceID != request.WorkspaceID {
		return modulehost.TaskAuthorization{}, &agentsdk.Error{Class: "forbidden", Code: "host.workspace_denied"}
	}
	identity := request.Identity
	identity.Execution.AuthorizationRevision = "host-auth-revision"
	evidence := agentmodel.AgentAuthorizationEvidence{
		Decision: "allowed", Code: "host.task.authorized", PolicyRevision: "policy-2", AuthorizationRevision: "host-auth-revision",
		AllowedObjects: []string{"customer"}, AllowedOutcomes: []string{"success"}, AllowedTools: []string{agentsdk.AgentToolQueryRecords},
	}
	return modulehost.TaskAuthorization{
		Principal: modulehost.Principal{Known: true, WorkspaceID: request.WorkspaceID, UserID: identity.Execution.UserID, RoleKey: identity.Execution.RoleKey, AuthorizationRevision: identity.Execution.AuthorizationRevision},
		Identity:  identity, Task: taskDefinition(request.TaskKey), Evidence: evidence, AllowedTools: append([]string(nil), evidence.AllowedTools...),
	}, nil
}

func (h *taskHost) IssueTaskCredential(_ context.Context, request modulehost.TaskCredentialRequest) (string, error) {
	h.mu.Lock()
	h.credentialCalls++
	h.mu.Unlock()
	if !request.Principal.Known || request.Principal.WorkspaceID != request.WorkspaceID || len(request.AllowedTools) != 1 {
		return "", &agentsdk.Error{Class: "forbidden", Code: "host.credential_scope_denied"}
	}
	return hostCredentialPrefix + request.WorkspaceID, nil
}

func (*taskHost) InvokeTaskTool(context.Context, modulehost.TaskToolRequest) (modulehost.TaskToolResult, error) {
	return modulehost.TaskToolResult{}, errors.New("unexpected task tool invocation")
}

func (h *taskHost) CompleteWorkflowTask(ctx context.Context, completion modulehost.WorkflowTaskCompletion) error {
	run, found, err := h.state.Get(ctx, completion.WorkspaceID, completion.TaskRunID)
	observation := completionObservation{value: completion, persistedBeforeCallback: err == nil && found && run.Status.Terminal(), err: err}
	select {
	case h.completions <- observation:
	default:
	}
	return nil
}

func (h *taskHost) waitForCompletion(t *testing.T) completionObservation {
	t.Helper()
	select {
	case observation := <-h.completions:
		if observation.err != nil {
			t.Fatal(observation.err)
		}
		return observation
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for workflow completion callback")
		return completionObservation{}
	}
}

func (h *taskHost) callCounts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authorizationCalls, h.credentialCalls
}

type applicationHost struct {
	task  *taskHost
	ports *unusedApplicationPorts
}

func (h applicationHost) InteractiveAgent() modulehost.InteractiveHost { return h.ports }
func (h applicationHost) TaskAgent() modulehost.TaskHost               { return h.task }
func (h applicationHost) ProposalAgent() modulehost.ProposalHost       { return h.ports }
func (h applicationHost) AuditAgent() modulehost.AuditHost             { return h.ports }
func (h applicationHost) AnalysisAgent() modulehost.AnalysisHost       { return h.ports }

type unusedApplicationPorts struct{}

func (*unusedApplicationPorts) ResolveInteractiveContext(context.Context, modulehost.InteractiveContextRequest) (agentsdk.GlobalContext, error) {
	return agentsdk.GlobalContext{}, nil
}
func (*unusedApplicationPorts) AuthorizeInteractive(context.Context, modulehost.InteractiveAuthorizationRequest) (modulehost.InteractiveAuthorization, error) {
	return modulehost.InteractiveAuthorization{}, nil
}
func (*unusedApplicationPorts) AuthorizeInteractiveTask(context.Context, modulehost.InteractiveTaskAuthorizationRequest) (modulehost.InteractiveTaskAuthorization, error) {
	return modulehost.InteractiveTaskAuthorization{}, nil
}
func (*unusedApplicationPorts) InvokeInteractiveTool(context.Context, modulehost.InteractiveToolInvocationRequest) (modulehost.InteractiveToolInvocationResult, error) {
	return modulehost.InteractiveToolInvocationResult{}, nil
}
func (*unusedApplicationPorts) StartInteractiveWorkflow(context.Context, modulehost.InteractiveWorkflowHandoffRequest) (string, error) {
	return "", nil
}
func (*unusedApplicationPorts) WakeAgentTask(context.Context, string, string) {}
func (*unusedApplicationPorts) GuardedWrites(context.Context, modulehost.Principal) []modulehost.GuardedWriteContract {
	return nil
}
func (*unusedApplicationPorts) ResolveProposalPrincipal(context.Context, string, string) (modulehost.Principal, error) {
	return modulehost.Principal{}, nil
}
func (*unusedApplicationPorts) InvokeProposalAction(context.Context, modulehost.ProposalActionRequest) (modulehost.ProposalActionResult, error) {
	return modulehost.ProposalActionResult{}, nil
}
func (*unusedApplicationPorts) RunProposalWorkflow(context.Context, modulehost.ProposalWorkflowRequest) (any, error) {
	return nil, nil
}
func (*unusedApplicationPorts) AppendAgentAudit(context.Context, modulehost.AuditRequest) error {
	return nil
}
func (*unusedApplicationPorts) ListAgentAudit(context.Context, modulehost.Principal, int) ([]modulehost.AuditEvent, error) {
	return nil, nil
}
func (*unusedApplicationPorts) ResolveAnalysisCatalog(context.Context, modulehost.Principal) (modulehost.AnalysisCatalog, error) {
	return modulehost.AnalysisCatalog{}, nil
}
func (*unusedApplicationPorts) ListAnalysisRecords(context.Context, string, map[string]any, int, modulehost.Principal) (modulehost.AnalysisRecordPage, error) {
	return modulehost.AnalysisRecordPage{}, nil
}

func bindApplicationHost(t *testing.T, binding agentsdk.Binding, task *taskHost) {
	t.Helper()
	binder, ok := binding.(modulehost.ApplicationHostBinder)
	if !ok {
		t.Fatal("Agent Binding does not expose the public ApplicationHost binder")
	}
	if err := binder.BindApplicationHost(applicationHost{task: task, ports: &unusedApplicationPorts{}}); err != nil {
		t.Fatal(err)
	}
}

func taskRequest(runID, workspaceID string, maxAttempts int) agentsdk.TaskRequest {
	identity := agentsdk.ExecutionIdentity{
		Mode:      "service_principal",
		Initiator: agentsdk.PrincipalReference{WorkspaceID: workspaceID, UserID: "initiator", RoleKey: "operator", AuthorizationRevision: "command-auth-revision"},
		Execution: agentsdk.PrincipalReference{WorkspaceID: workspaceID, UserID: "agent-user", RoleKey: "agent-role", AuthorizationRevision: "command-auth-revision"},
	}
	return agentsdk.TaskRequest{
		TaskRunID: runID, ProcessID: "process-" + runID, NodeInstanceID: "node-" + runID, WorkspaceID: workspaceID,
		Task: taskDefinition("review"), Identity: identity, Input: map[string]any{"record_id": "customer-1"},
		AllowedObjects: []string{"customer"}, AllowedOutcomes: []string{"success"}, AllowedTools: []string{agentsdk.AgentToolQueryRecords},
		ExecutionCredential: callerCredential, CorrelationID: "correlation-" + runID, IdempotencyKey: "command-" + runID,
		MaxAttempts: maxAttempts, Deadline: time.Now().UTC().Add(10 * time.Second),
	}
}

func taskDefinition(key string) agentsdk.AgentTaskDefinition {
	return agentsdk.AgentTaskDefinition{
		ContractVersion: agentsdk.AgentTaskContractVersion, Key: key, Version: "1", AgentKey: "reviewer", Instruction: "Review the customer record.",
		InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object", "required": []string{"result"}},
		AllowedObjects: []string{"customer"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly,
		ExecutionLimits: agentsdk.AgentExecutionLimits{TimeoutSeconds: 10, MaxToolCalls: 2, CostBudget: "low"}, Enabled: true,
	}
}

func serviceContext(ctx context.Context, action string) context.Context {
	return agentsdk.WithAuthorizedServiceAction(ctx, action, agentsdk.AgentRuntimeServiceAudience)
}

func waitForTaskResult(t *testing.T, runner agentsdk.TaskRunner, locator, key string) agentsdk.TaskResult {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		result := waitForSinglePoll(t, runner, locator, key)
		switch result.Status {
		case agentsdk.ProviderRunCompleted, agentsdk.ProviderRunFailed, agentsdk.ProviderRunCancelled:
			return result
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s", locator)
	return agentsdk.TaskResult{}
}

func waitForSinglePoll(t *testing.T, runner agentsdk.TaskRunner, locator, key string) agentsdk.TaskResult {
	t.Helper()
	result, err := runner.Poll(serviceContext(t.Context(), agentsdk.ActionAgentTaskExecutionPoll), locator, key)
	if err != nil {
		t.Fatalf("Poll %s: %v", locator, err)
	}
	return result
}

func assertStoredTask(t *testing.T, binding agentsdk.Binding, workspaceID, runID string, status agentmodel.AgentTaskRunStatus) {
	t.Helper()
	run := loadStoredTask(t, binding, workspaceID, runID)
	if run.Status != status {
		t.Fatalf("stored task status=%q want=%q", run.Status, status)
	}
}

func loadStoredTask(t *testing.T, binding agentsdk.Binding, workspaceID, runID string) agentmodel.AgentTaskRun {
	t.Helper()
	repositories, ok := binding.(agentpersistence.Binding)
	if !ok {
		t.Fatal("Agent Binding does not expose persistence contract")
	}
	run, found, err := repositories.AgentTaskRunRepository().Get(t.Context(), workspaceID, runID)
	if err != nil || !found {
		t.Fatalf("load task workspace=%q run=%q found=%t err=%v", workspaceID, runID, found, err)
	}
	return run
}

func waitForStoredTask(t *testing.T, binding agentsdk.Binding, workspaceID, runID string, ready func(agentmodel.AgentTaskRun) bool) agentmodel.AgentTaskRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run := loadStoredTask(t, binding, workspaceID, runID)
		if ready(run) {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for persisted task %s/%s", workspaceID, runID)
	return agentmodel.AgentTaskRun{}
}

func containsAuthorizationRevision(values []agentmodel.AgentAuthorizationEvidence, revision string) bool {
	for _, value := range values {
		if value.AuthorizationRevision == revision {
			return true
		}
	}
	return false
}

func sdkErrorCode(err error) string {
	var sdkError *agentsdk.Error
	if errors.As(err, &sdkError) {
		return sdkError.ErrorCode()
	}
	return ""
}

func assertCanonicalEqual(t *testing.T, left, right any) {
	t.Helper()
	leftJSON, err := modulecapability.CanonicalJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := modulecapability.CanonicalJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("canonical values differ\nleft: %s\nright: %s", leftJSON, rightJSON)
	}
}
