package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	actioncontract "github.com/domainry/domainry-foundation/action"
)

type incompleteExecutionRepositoryStub struct {
	agentpersistence.AgentTaskRunRepository
}

type completeExecutionRepositoryStub struct {
	agentpersistence.AgentTaskRunRepository
	agentpersistence.AgentTaskRunSystemWorkerRepository
	agentpersistence.AgentTaskRunDirectClaimRepository
	agentpersistence.AgentInteractiveRunRepository
	agentpersistence.AgentToolCallLedger
}

type readyDialogStateStub struct {
	agentsdk.AgentDialogStateService
}
type readyStateRepositoryStub struct {
	agentpersistence.AgentStateRepository
}
type readyDefinitionRepositoryStub struct {
	agentpersistence.DefinitionRepository
}
type readyLifecycleRepositoryStub struct {
	agentpersistence.AgentLifecycleRepository
}

type readyRepositoriesStub struct {
	state       agentpersistence.AgentStateRepository
	tasks       agentpersistence.AgentTaskRunRepository
	definitions agentpersistence.DefinitionRepository
}

func (s readyRepositoriesStub) AgentStateRepository() agentpersistence.AgentStateRepository {
	return s.state
}
func (s readyRepositoriesStub) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository {
	return s.tasks
}
func (s readyRepositoriesStub) DefinitionRepository() agentpersistence.DefinitionRepository {
	return s.definitions
}

type runnerStub struct{}

func (runnerStub) Start(context.Context, agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunAccepted}, nil
}
func (runnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunRunning}, nil
}
func (runnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunCancelled}, nil
}
func (runnerStub) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{Status: "completed"}, nil
}

type serviceActionRunnerStub struct{ startAuthorized bool }

func (runner *serviceActionRunnerStub) Start(ctx context.Context, _ agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	runner.startAuthorized = agentsdk.HasAuthorizedServiceAction(ctx, actionAgentSaaSTaskProviderStart, agentsdk.AgentRuntimeServiceAudience)
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunAccepted}, nil
}
func (*serviceActionRunnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}
func (*serviceActionRunnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{}, nil
}

func TestSaaSManifestOwnsEveryServiceRouteAndInjectsCurrentAction(t *testing.T) {
	actions, err := SaaSAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 95 {
		t.Fatalf("Agent SaaS actions=%d, want 95", len(actions))
	}
	patterns := map[string]bool{}
	for _, action := range actions {
		if action.Owner != agentsdk.AgentAuthorizationOwner || action.SourceKind != "service_protocol" || action.HTTP == nil || action.Permission != nil || action.Authorization.Strategy != actioncontract.AuthorizationSigned || action.Authorization.PolicyKey != "agent.saas_api_key" || len(action.Authorization.Audiences) != 1 || action.Authorization.Audiences[0] != agentsdk.AgentRuntimeServiceAudience {
			t.Fatalf("invalid Agent SaaS Action: %+v", action)
		}
		pattern := action.HTTP.Method + " " + action.HTTP.RouteTemplate
		if patterns[pattern] {
			t.Fatalf("duplicate Agent SaaS route %q", pattern)
		}
		if route := action.HTTP.RouteTemplate; route != "/readyz" && !strings.HasPrefix(route, "/.well-known/") && !strings.HasPrefix(route, "/agent/") {
			t.Fatalf("Agent SaaS route escapes the Agent namespace: %q", route)
		}
		patterns[pattern] = true
	}

	runner := &serviceActionRunnerStub{}
	service, err := New(Config{APIKey: "secret", Runner: runner, Interactive: runnerStub{}})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"task_run_id":"task","workspace_id":"workspace","task":{"contract_version":"agent-task-v1","key":"task","version":"1","agent_key":"runner","instruction":"run","input_schema":{"type":"object"},"output_schema":{"type":"object"},"allowed_outcomes":["success"],"side_effect_mode":"analysis_only","enabled":true},"identity":{},"idempotency_key":"key","deadline":"0001-01-01T00:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/agent/v1/task-runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Idempotency-Key", "key")
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !runner.startAuthorized {
		t.Fatalf("status=%d action_authorized=%t body=%s", response.Code, runner.startAuthorized, response.Body.String())
	}
}

func TestServerAuthenticationAndIdempotency(t *testing.T) {
	service, err := New(Config{APIKey: "secret", Runner: runnerStub{}, Interactive: runnerStub{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	request := httptest.NewRequest(http.MethodGet, "/agent/v1/descriptor", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	body := `{"task_run_id":"task","workspace_id":"workspace","task":{"contract_version":"agent-task-v1","key":"task","version":"1","agent_key":"runner","instruction":"run","input_schema":{"type":"object"},"output_schema":{"type":"object"},"allowed_outcomes":["success"],"side_effect_mode":"analysis_only","enabled":true},"identity":{},"idempotency_key":"key","deadline":"0001-01-01T00:00:00Z"}`
	request = httptest.NewRequest(http.MethodPost, "/agent/v1/task-runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Idempotency-Key", "other")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/agent/v1/task-runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Idempotency-Key", "key")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"external_run_id":"run"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestServerDoesNotExposeGenericRepositoryOperationRoute(t *testing.T) {
	service, err := New(Config{APIKey: "secret", Runner: runnerStub{}, Interactive: runnerStub{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Handler()
	request := httptest.NewRequest(http.MethodPost, "/agent/v1/repository/task.get", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("generic repository route status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExecutionRepositoryReadinessMatchesAdvertisedCapability(t *testing.T) {
	if agentExecutionRepositoriesReady(nil) || agentExecutionRepositoriesReady(&incompleteExecutionRepositoryStub{}) {
		t.Fatal("incomplete execution repositories reported ready")
	}
	if !agentExecutionRepositoriesReady(&completeExecutionRepositoryStub{}) {
		t.Fatal("complete execution repositories reported unavailable")
	}
}

func TestReadyRequiresEveryAdvertisedAgentCapability(t *testing.T) {
	repositories := readyRepositoriesStub{
		state:       &readyStateRepositoryStub{},
		tasks:       &completeExecutionRepositoryStub{},
		definitions: &readyDefinitionRepositoryStub{},
	}
	config := Config{APIKey: "secret", Runner: runnerStub{}, Interactive: runnerStub{}, Repositories: repositories, Lifecycle: &readyLifecycleRepositoryStub{}}
	call := func(dialog agentsdk.AgentDialogStateService) int {
		config.DialogState = dialog
		request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		service, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		service.Handler().ServeHTTP(response, request)
		return response.Code
	}
	if status := call(nil); status != http.StatusServiceUnavailable {
		t.Fatalf("missing dialog state status=%d", status)
	}
	if status := call(&readyDialogStateStub{}); status != http.StatusOK {
		t.Fatalf("complete capability status=%d", status)
	}
}
