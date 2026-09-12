package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentcontracttest "github.com/domainry/domainry-agent-sdk/contracttest"
	agentcapability "github.com/domainry/domainry-agent/internal/capability"
	"github.com/domainry/domainry-agent/server"
	"github.com/domainry/domainry-foundation/modulecapability"
	capabilitycontracttest "github.com/domainry/domainry-foundation/modulecapability/contracttest"
)

type host struct{ runtimeID string }

func newRemoteHost(t *testing.T, id string) *host {
	t.Helper()
	return &host{runtimeID: id}
}
func (h *host) RuntimeID() string { return h.runtimeID }

type runner struct {
	started     agentsdk.TaskRequest
	interactive agentsdk.InteractiveRequest
}

type scheduledConversationStub struct {
	agentsdk.ConversationService
	request    agentsdk.ScheduledConversationTaskRequest
	authorized bool
	calls      int
}

func (s *scheduledConversationStub) StartScheduledConversationTask(ctx context.Context, request agentsdk.ScheduledConversationTaskRequest) (agentsdk.ScheduledConversationTaskReceipt, error) {
	s.calls++
	s.request = request
	s.authorized = agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience)
	return agentsdk.ScheduledConversationTaskReceipt{Task: agentsdk.ConversationTask{ID: "scheduled-task", Status: agentsdk.ConversationTaskStatusQueued}}, nil
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
	agentServer, err := server.New(server.Config{APIKey: "secret", Runner: provider, Interactive: provider})
	if err != nil {
		t.Fatal(err)
	}
	service := httptest.NewServer(agentServer.Handler())
	defer service.Close()
	opened, err := NewFactory(Options{BaseURL: service.URL, APIKey: "secret", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	agentcontracttest.VerifyBinding(t, opened, agentsdk.DeploymentModeSaaS)
	capabilitycontracttest.VerifyBinding(t, opened)
	directCapability, err := agentcapability.NewBinding()
	if err != nil {
		t.Fatal(err)
	}
	directSummary, err := directCapability.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	remoteSummary, err := opened.CapabilitySummary(t.Context())
	if err != nil || remoteSummary.Identity.ContractSHA256 != directSummary.Identity.ContractSHA256 {
		t.Fatalf("Agent Module/SaaS capability digest direct=%q remote=%q err=%v", directSummary.Identity.ContractSHA256, remoteSummary.Identity.ContractSHA256, err)
	}
	remoteBinding := opened.(*binding)
	if opened.TaskRunner() == agentsdk.TaskRunner(remoteBinding.client) {
		t.Fatal("SaaS Binding exposes the synchronous provider client as its task capability")
	}
	result, err := remoteBinding.client.Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task-1", WorkspaceID: "workspace", Task: agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true}, IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider-1" || provider.started.TaskRunID != "task-1" || provider.started.Task.AgentKey != "reviewer" || len(provider.started.Task.AllowedOutcomes) != 1 {
		t.Fatalf("result=%+v request=%+v err=%v", result, provider.started, err)
	}
	interactive, err := opened.InteractiveRunner().Run(t.Context(), agentsdk.InteractiveRequest{RunID: "interactive-1", SessionID: "session-1", IdempotencyKey: "interactive-key", Message: "review", Context: agentsdk.GlobalContext{ContractVersion: agentsdk.GlobalAgentContextContractVersion, ContextRevision: "context-1", EntrypointKey: "assistant", AgentKey: "reviewer", Principal: agentsdk.PrincipalReference{WorkspaceID: "workspace", UserID: "user"}}, Candidates: []agentsdk.RouteCandidate{{RouteType: agentsdk.AgentRouteTask, TargetKey: "review"}}})
	if err != nil || interactive.Handoff == nil || interactive.Handoff.TargetKey != "review" || provider.interactive.Context.ContextRevision != "context-1" || provider.interactive.Candidates[0].TargetKey != "review" {
		t.Fatalf("interactive=%+v request=%+v err=%v", interactive, provider.interactive, err)
	}
}

func TestSaaSScheduledTaskUsesServiceCredentialAndExactAction(t *testing.T) {
	conversations := &scheduledConversationStub{}
	agentServer, err := server.New(server.Config{APIKey: "runtime-secret", Conversations: conversations, ConversationRuntimeID: "runtime", ConversationWorkspaceID: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(agentServer.Handler())
	defer httpServer.Close()
	opened, err := NewFactory(Options{BaseURL: httpServer.URL, APIKey: "runtime-secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(context.Background())
	if !opened.Descriptor().HasCapability(agentsdk.CapabilityScheduledConversationTask) {
		t.Fatalf("descriptor=%+v", opened.Descriptor())
	}
	tasks, ok := opened.(agentsdk.ConversationBinding).Conversations().(agentsdk.ScheduledConversationTaskService)
	if !ok {
		t.Fatal("remote binding omitted scheduled task service")
	}
	request := agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion,
		PlanID:          "plan", SchedulerRunID: "scheduler-run", IdempotencyKey: "window", ScheduledFor: time.Now().UTC(),
		Authority: agentsdk.ConversationAuthority{Known: true, WorkspaceID: "workspace", UserID: "user"}, ConversationID: "conversation",
		Input: agentsdk.ConversationTaskStart{Goal: "run", Input: `{}`},
	}
	if _, err = tasks.StartScheduledConversationTask(t.Context(), request); err == nil || conversations.calls != 0 {
		t.Fatalf("missing service Action reached SaaS: calls=%d err=%v", conversations.calls, err)
	}
	ctx := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience)
	receipt, err := tasks.StartScheduledConversationTask(ctx, request)
	if err != nil || receipt.Task.ID != "scheduled-task" || conversations.calls != 1 || !conversations.authorized || conversations.request.Authority.RuntimeID != "runtime" {
		t.Fatalf("receipt=%+v calls=%d authorized=%t request=%+v err=%v", receipt, conversations.calls, conversations.authorized, conversations.request, err)
	}
	request.Authority.WorkspaceID = "other"
	if _, err = tasks.StartScheduledConversationTask(ctx, request); err == nil || conversations.calls != 1 {
		t.Fatalf("cross-workspace request reached Agent: calls=%d err=%v", conversations.calls, err)
	}
}

func TestSaaSFactoryFailsClosed(t *testing.T) {
	if _, err := NewFactory(Options{}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime")); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	agentServer, err := server.New(server.Config{APIKey: "right"})
	if err != nil {
		t.Fatal(err)
	}
	service := httptest.NewServer(agentServer.Handler())
	defer service.Close()
	if _, err := NewFactory(Options{BaseURL: service.URL, APIKey: "wrong", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime")); err == nil {
		t.Fatal("invalid credential accepted")
	}
	different, err := capabilitycontracttest.NewFixtureBinding("agent")
	if err != nil {
		t.Fatal(err)
	}
	differentHandler, err := modulecapability.NewHTTPHandler(different, func(*http.Request) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	operationalHandler := agentServer.Handler()
	staleService := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == modulecapability.SummaryPath || request.URL.Path == modulecapability.ValidationPath || strings.HasPrefix(request.URL.Path, modulecapability.CategoriesPath) {
			differentHandler.ServeHTTP(response, request)
			return
		}
		operationalHandler.ServeHTTP(response, request)
	}))
	defer staleService.Close()
	if _, err := NewFactory(Options{BaseURL: staleService.URL, APIKey: "right", Client: staleService.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, newRemoteHost(t, "runtime")); err == nil {
		t.Fatal("Agent Remote accepted a different source capability digest")
	}
}

func TestRemoteClientPreservesStructuredAndGenericHTTPErrors(t *testing.T) {
	service := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/agent/v1/task-runs" {
			response.WriteHeader(http.StatusBadGateway)
			_, _ = response.Write([]byte(`{"status":"failed","error_class":"provider_http","error_code":"agent.runner.provider_http_429","retryable":true}`))
			return
		}
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(`{"code":"agent.saas.overloaded","message":"temporarily unavailable"}`))
	}))
	defer service.Close()
	client := newClient(Options{BaseURL: service.URL, APIKey: "secret", Client: service.Client()})

	result, err := client.Start(t.Context(), agentsdk.TaskRequest{IdempotencyKey: "key"})
	var structured *agentsdk.Error
	if !errors.As(err, &structured) || structured.Class != "provider_http" || structured.Code != "agent.runner.provider_http_429" || !structured.Retryable || result.ErrorCode != structured.Code {
		t.Fatalf("structured result=%+v error=%+v", result, structured)
	}

	_, err = client.descriptor(t.Context())
	var generic *agentsdk.Error
	if !errors.As(err, &generic) || generic.Class != "saas_http" || generic.Code != "agent.saas.overloaded" || generic.Message != "temporarily unavailable" || !generic.Retryable {
		t.Fatalf("generic error=%+v", generic)
	}
}
