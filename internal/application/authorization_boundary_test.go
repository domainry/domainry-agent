package application

import (
	"context"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type authorizationTaskStateStub struct {
	agentpersistence.AgentTaskStateService
	listCalls    int
	getCalls     int
	operationKey string
}

func (s *authorizationTaskStateStub) List(context.Context, string, agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	s.listCalls++
	return []agentmodel.AgentTaskRun{{ID: "task-1"}}, nil
}

func (s *authorizationTaskStateStub) Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error) {
	s.getCalls++
	return agentmodel.AgentTaskRun{ID: "task-1"}, true, nil
}

func (s *authorizationTaskStateStub) Operate(_ context.Context, _, _, operationKey, _, _ string, _ agentpersistence.AgentTaskActor, _ agentpersistence.AgentTaskAuditAppender) (agentmodel.AgentTaskRun, bool, error) {
	s.operationKey = operationKey
	return agentmodel.AgentTaskRun{ID: "task-1"}, false, nil
}

type authorizationAuditHostStub struct {
	appendCalls int
	listCalls   int
}

func (s *authorizationAuditHostStub) AppendAgentAudit(context.Context, modulehost.AuditRequest) error {
	s.appendCalls++
	return nil
}

func (s *authorizationAuditHostStub) ListAgentAudit(context.Context, modulehost.Principal, int) ([]modulehost.AuditEvent, error) {
	s.listCalls++
	return nil, nil
}

func TestTaskOperationsRequireTheCurrentExactAction(t *testing.T) {
	state := &authorizationTaskStateStub{}
	service := NewTaskOperationsService(state, &authorizationAuditHostStub{}, nil)

	listPrincipal := authorizedApplicationPrincipal(agentsdk.ActionAgentTasksList)
	if _, err := service.List(t.Context(), "workspace-1", agentpersistence.AgentTaskRunFilter{}, listPrincipal); err != nil || state.listCalls != 1 {
		t.Fatalf("list calls=%d err=%v", state.listCalls, err)
	}
	if _, _, err := service.Get(t.Context(), "workspace-1", "task-1", listPrincipal); executionErrorCode(err) != "agent.authorization.action_denied" || state.getCalls != 0 {
		t.Fatalf("sibling get calls=%d err=%v", state.getCalls, err)
	}

	resolvePrincipal := authorizedApplicationPrincipal(agentsdk.ActionAgentTasksResolve)
	if _, _, err := service.Operate(t.Context(), "workspace-1", "task-1", "retry", "retry-1", "retry", resolvePrincipal); executionErrorCode(err) != "agent.authorization.action_denied" || state.operationKey != "" {
		t.Fatalf("sibling retry operation=%q err=%v", state.operationKey, err)
	}
	retryPrincipal := authorizedApplicationPrincipal(agentsdk.ActionAgentTasksRetry)
	if _, _, err := service.Operate(t.Context(), "workspace-1", "task-1", "retry", "retry-1", "retry", retryPrincipal); err != nil || state.operationKey != "retry" {
		t.Fatalf("authorized retry operation=%q err=%v", state.operationKey, err)
	}
}

type authorizationDialogStateStub struct {
	agentsdk.AgentDialogStateService
	proposalListCalls int
}

func (s *authorizationDialogStateStub) ListProposals(context.Context, string, agentsdk.AgentAuthority) ([]agentsdk.AgentProposal, error) {
	s.proposalListCalls++
	return nil, nil
}

func TestDiagnosticsRequiresItsCurrentExactAction(t *testing.T) {
	state := &authorizationDialogStateStub{}
	audit := &authorizationAuditHostStub{}
	service := NewDiagnosticsService(state, audit)

	request := DiagnosticsRequest{Principal: authorizedApplicationPrincipal(agentsdk.ActionAgentTasksList)}
	if _, err := service.Inspect(t.Context(), request); executionErrorCode(err) != "agent.authorization.action_denied" || state.proposalListCalls != 0 || audit.listCalls != 0 {
		t.Fatalf("sibling action state_calls=%d audit_calls=%d err=%v", state.proposalListCalls, audit.listCalls, err)
	}
	request.Principal = authorizedApplicationPrincipal(agentsdk.ActionAgentDiagnosticsRead)
	if _, err := service.Inspect(t.Context(), request); err != nil || state.proposalListCalls != 1 || audit.listCalls != 1 {
		t.Fatalf("diagnostics state_calls=%d audit_calls=%d err=%v", state.proposalListCalls, audit.listCalls, err)
	}
}

func TestTaskExecutionRequiresTheCurrentServiceAction(t *testing.T) {
	service := NewTaskExecutionService(nil, nil, "")
	request := agentsdk.TaskRequest{}
	if _, err := service.Start(t.Context(), request); executionErrorCode(err) != "agent.authorization.service_action_denied" {
		t.Fatalf("missing Start authorization err=%v", err)
	}
	pollContext := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionPoll, agentsdk.AgentRuntimeServiceAudience)
	if _, err := service.Start(pollContext, request); executionErrorCode(err) != "agent.authorization.service_action_denied" {
		t.Fatalf("Poll authorization reached Start err=%v", err)
	}
	startContext := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience)
	if _, err := service.Start(startContext, request); executionErrorCode(err) != "agent.task.capability_unavailable" {
		t.Fatalf("exact Start authorization did not reach service validation err=%v", err)
	}
	if _, err := service.Poll(startContext, "invalid", ""); executionErrorCode(err) != "agent.authorization.service_action_denied" {
		t.Fatalf("Start authorization reached Poll err=%v", err)
	}
	pollContext = agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionPoll, agentsdk.AgentRuntimeServiceAudience)
	if _, err := service.Poll(pollContext, "invalid", ""); executionErrorCode(err) != "agent.task.locator_invalid" {
		t.Fatalf("exact Poll authorization did not reach service validation err=%v", err)
	}
	cancelContext := agentsdk.WithAuthorizedServiceAction(t.Context(), agentsdk.ActionAgentTaskExecutionCancel, agentsdk.AgentRuntimeServiceAudience)
	if _, err := service.Cancel(cancelContext, "invalid", ""); executionErrorCode(err) != "agent.task.locator_invalid" {
		t.Fatalf("exact Cancel authorization did not reach service validation err=%v", err)
	}
}

func authorizedApplicationPrincipal(actionKey string) modulehost.Principal {
	return modulehost.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator-1", RoleKey: "operator", AuthorizedActionKey: actionKey}
}
