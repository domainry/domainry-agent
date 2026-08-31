package application

import (
	"context"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type interactiveExecutionStateStub struct {
	agentpersistence.AgentInteractiveStateService
	evidence agentmodel.AgentTaskToolInvocationEvidence
}

func (s *interactiveExecutionStateStub) RecordToolInvocation(_ context.Context, run agentmodel.AgentInteractiveRun, evidence agentmodel.AgentTaskToolInvocationEvidence) (agentmodel.AgentInteractiveRun, error) {
	s.evidence = evidence
	run.ToolCallCount++
	run.ToolInvocations = append(run.ToolInvocations, evidence)
	return run, nil
}

type interactiveExecutionHostStub struct {
	request modulehost.InteractiveToolInvocationRequest
	result  modulehost.InteractiveToolInvocationResult
	calls   int
}

func (interactiveExecutionHostStub) ResolveInteractiveContext(context.Context, modulehost.InteractiveContextRequest) (agentsdk.GlobalContext, error) {
	return agentsdk.GlobalContext{}, nil
}
func (interactiveExecutionHostStub) AuthorizeInteractive(context.Context, modulehost.InteractiveAuthorizationRequest) (modulehost.InteractiveAuthorization, error) {
	return modulehost.InteractiveAuthorization{}, nil
}
func (interactiveExecutionHostStub) AuthorizeInteractiveTask(context.Context, modulehost.InteractiveTaskAuthorizationRequest) (modulehost.InteractiveTaskAuthorization, error) {
	return modulehost.InteractiveTaskAuthorization{}, nil
}
func (s *interactiveExecutionHostStub) InvokeInteractiveTool(_ context.Context, request modulehost.InteractiveToolInvocationRequest) (modulehost.InteractiveToolInvocationResult, error) {
	s.request, s.calls = request, s.calls+1
	return s.result, nil
}
func (interactiveExecutionHostStub) StartInteractiveWorkflow(context.Context, modulehost.InteractiveWorkflowHandoffRequest) (string, error) {
	return "", nil
}
func (interactiveExecutionHostStub) WakeAgentTask(context.Context, string, string) {}

func TestInteractiveExecutionOwnsToolBudgetAndEvidence(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	state := &interactiveExecutionStateStub{}
	host := &interactiveExecutionHostStub{result: modulehost.InteractiveToolInvocationResult{
		Status: "executed", Tool: "host-tool", Output: map[string]any{"count": 1},
		Authorization: agentmodel.AgentAuthorizationEvidence{AuthorizationRevision: "auth-2"},
	}}
	service := NewInteractiveExecutionService(InteractiveExecutionDependencies{State: state, Host: host, Now: func() time.Time { return now }})
	run := agentmodel.AgentInteractiveRun{
		ID: "interactive-1", WorkspaceID: "workspace-1", SessionID: "session-1", EntrypointKey: "assistant", RouteKey: "workspace.customer", CorrelationID: "correlation-1",
		Context: agentsdk.GlobalContext{ContextRevision: "context-1", ObjectKey: "customer"}, Status: agentmodel.AgentInteractiveRunRunning,
	}
	authorized := modulehost.InteractiveAuthorization{
		Principal: modulehost.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator"},
		Agent:     agentsdk.AgentSchema{ExecutionLimits: agentsdk.AgentExecutionLimits{MaxToolCalls: 1, CostBudget: "low"}},
	}
	updated, result, err := service.invokeTool(t.Context(), run, agentsdk.RouteResult{
		RouteType: agentsdk.AgentRouteInteractiveQuery, IdempotencyKey: "tool-1", Input: map[string]any{"tool": agentsdk.AgentToolQueryRecords, "object_key": "customer"},
	}, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if result.CallRef != "agent_interactive_tool_interactive-1_1" || result.CostUnits != 2 || updated.ToolCallCount != 1 {
		t.Fatalf("result=%#v updated=%#v", result, updated)
	}
	if host.request.RunID != run.ID || host.request.Context.ContextRevision != "context-1" || host.request.Principal.UserID != "user-1" {
		t.Fatalf("narrow Host request=%#v", host.request)
	}
	if state.evidence.Ref != result.CallRef || state.evidence.CostUnits != 2 || state.evidence.Authorization.AuthorizationRevision != "auth-2" {
		t.Fatalf("Agent evidence=%#v", state.evidence)
	}
	run.ToolCallCount = 1
	if _, _, err := service.invokeTool(t.Context(), run, agentsdk.RouteResult{RouteType: agentsdk.AgentRouteInteractiveQuery, Input: map[string]any{"tool": agentsdk.AgentToolQueryRecords}}, authorized); errorCode(err, "") != "agent.task.tool_call_limit" {
		t.Fatalf("limit error=%v", err)
	}
	if host.calls != 1 {
		t.Fatalf("Host called after Agent limit rejection: %d", host.calls)
	}
}
