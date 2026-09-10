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

type taskToolStateStub struct {
	agentpersistence.AgentTaskStateService
	run    agentmodel.AgentTaskRun
	marked bool
}

func (s taskToolStateStub) Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error) {
	return s.run, true, nil
}

func (s *taskToolStateStub) MarkWorkflowCompletionDelivered(context.Context, string, string) (agentmodel.AgentTaskRun, error) {
	s.marked = true
	return s.run, nil
}

type taskToolLedgerStub struct {
	start  agentpersistence.AgentToolCallStart
	finish agentpersistence.AgentToolCallFinish
}

func (s *taskToolLedgerStub) BeginAgentToolCall(_ context.Context, start agentpersistence.AgentToolCallStart) (string, int, error) {
	s.start = start
	return "agent_tool_task-1_1", 1, nil
}

func (s *taskToolLedgerStub) FinishAgentToolCall(_ context.Context, finish agentpersistence.AgentToolCallFinish) error {
	s.finish = finish
	return nil
}

type taskToolHostStub struct {
	request                     modulehost.TaskToolRequest
	result                      modulehost.TaskToolResult
	err                         error
	completion                  modulehost.WorkflowTaskCompletion
	completionErr               error
	authorization               *modulehost.TaskAuthorization
	authorizationErr            error
	authorizationCalls, invokes int
}

func (h *taskToolHostStub) AuthorizeTask(_ context.Context, in modulehost.TaskAuthorizationRequest) (modulehost.TaskAuthorization, error) {
	h.authorizationCalls++
	if h.authorizationErr != nil {
		return modulehost.TaskAuthorization{}, h.authorizationErr
	}
	if h.authorization != nil {
		return *h.authorization, nil
	}
	return modulehost.TaskAuthorization{Principal: modulehost.Principal{Known: true, UserID: "user", WorkspaceID: in.WorkspaceID}, Identity: in.Identity, Task: agentsdk.AgentTaskDefinition{Key: in.TaskKey, Version: in.TaskVersion}, AllowedTools: []string{agentsdk.AgentToolQueryRecords}, Evidence: agentmodel.AgentAuthorizationEvidence{AuthorizationRevision: "fresh-auth", AllowedTools: []string{agentsdk.AgentToolQueryRecords}}}, nil
}
func (taskToolHostStub) IssueTaskCredential(context.Context, modulehost.TaskCredentialRequest) (string, error) {
	return "", nil
}
func (s *taskToolHostStub) InvokeTaskTool(_ context.Context, request modulehost.TaskToolRequest) (modulehost.TaskToolResult, error) {
	s.request = request
	s.invokes++
	return s.result, s.err
}
func (s *taskToolHostStub) CompleteWorkflowTask(_ context.Context, completion modulehost.WorkflowTaskCompletion) error {
	s.completion = completion
	return s.completionErr
}

func TestTaskToolServiceOwnsFencedLedgerAndBudget(t *testing.T) {
	previous := agentmodel.AgentAuthorizationEvidence{AuthorizationRevision: "auth-1", AllowedTools: []string{agentsdk.AgentToolQueryRecords}}
	current := agentmodel.AgentAuthorizationEvidence{AuthorizationRevision: "auth-2", AllowedTools: []string{agentsdk.AgentToolQueryRecords}}
	state := &taskToolStateStub{run: agentmodel.AgentTaskRun{
		ID: "task-1", WorkspaceID: "workspace-1", ProcessID: "process-1", TaskKey: "review", TaskVersion: "1",
		Status: agentmodel.AgentTaskRunRunning, MaxToolCalls: 7, MaxCostUnits: 9,
		Lease: agentmodel.AgentTaskLease{Owner: "worker-1", FencingToken: 3}, Evidence: agentmodel.AgentTaskExecutionEvidence{Authorization: []agentmodel.AgentAuthorizationEvidence{previous}},
	}}
	ledger := &taskToolLedgerStub{}
	host := &taskToolHostStub{result: modulehost.TaskToolResult{Status: "executed", Tool: agentsdk.AgentToolQueryRecords, Output: map[string]any{"count": 1}, Authorization: current}}
	service := NewTaskToolService(state, ledger, host)
	result, err := service.Invoke(t.Context(), TaskToolInvocation{Credential: "credential", WorkspaceID: "workspace-1", TaskRunID: "task-1", Tool: agentsdk.AgentToolQueryRecords, Input: map[string]any{"object_key": "customer"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.CallRef != "agent_tool_task-1_1" || host.request.Owner != "worker-1" || host.request.FencingToken != 3 {
		t.Fatalf("result=%#v host request=%#v", result, host.request)
	}
	if ledger.start.MaxToolCalls != 7 || ledger.start.MaxCostUnits != 9 || ledger.start.CostUnits != 2 || ledger.start.Authorization.AuthorizationRevision != "fresh-auth" || ledger.start.InputHash == "" {
		t.Fatalf("ledger start=%#v", ledger.start)
	}
	if ledger.finish.CallRef != result.CallRef || ledger.finish.Status != "executed" || ledger.finish.Authorization.AuthorizationRevision != "auth-2" || ledger.finish.Evidence["output_hash"] == "" {
		t.Fatalf("ledger finish=%#v", ledger.finish)
	}
}

func TestTaskToolServiceRequiresAgentOwnedLedger(t *testing.T) {
	service := NewTaskToolService(&taskToolStateStub{}, nil, &taskToolHostStub{})
	if _, err := service.Invoke(t.Context(), TaskToolInvocation{}); errorCode(err, "") != "agent.tool.gateway_unavailable" {
		t.Fatalf("error=%v", err)
	}
}

func TestTaskExecutionNotifiesWorkflowAfterAgentTerminalState(t *testing.T) {
	state := &taskToolStateStub{}
	host := &taskToolHostStub{}
	service := NewTaskExecutionService(state, nil, "worker-1")
	if err := service.BindHost(host); err != nil {
		t.Fatal(err)
	}
	run := agentmodel.AgentTaskRun{
		ID: "task-1", WorkspaceID: "workspace-1", ProcessID: "process-1", NodeInstanceID: "node-1",
		TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunSucceeded, Outcome: "success",
		Identity:       agentsdk.ExecutionIdentity{Execution: agentsdk.PrincipalReference{UserID: "agent-user", RoleKey: "agent-role"}},
		Reconciliation: agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending"},
	}
	state.run = run
	service.notifyWorkflow(t.Context(), run)
	if host.completion.TaskRunID != "task-1" || host.completion.ProcessID != "process-1" || host.completion.NodeInstanceID != "node-1" {
		t.Fatalf("completion=%#v", host.completion)
	}
	if !state.marked {
		t.Fatal("Agent callback delivery was not recorded after Runtime acknowledgement")
	}
}

func TestTaskToolChecksLiveScopeBeforeReservingOrInvoking(t *testing.T) {
	for _, mode := range []string{"revoked", "wrong workspace", "wrong version", "unknown principal", "cancel requested", "cancelled context"} {
		t.Run(mode, func(t *testing.T) {
			current := modulehost.TaskAuthorization{Principal: modulehost.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}, Task: agentsdk.AgentTaskDefinition{Key: "review", Version: "1"}, AllowedTools: []string{agentsdk.AgentToolQueryRecords}}
			state := &taskToolStateStub{run: agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "workspace", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunRunning, Lease: agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 1}, Evidence: agentmodel.AgentTaskExecutionEvidence{Authorization: []agentmodel.AgentAuthorizationEvidence{{AllowedTools: []string{agentsdk.AgentToolQueryRecords}}}}}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "revoked":
				current.AllowedTools = nil
			case "wrong workspace":
				current.Principal.WorkspaceID = "other"
			case "wrong version":
				current.Task.Version = "2"
			case "unknown principal":
				current.Principal.Known = false
			case "cancel requested":
				now := time.Now()
				state.run.CancelRequestedAt = &now
			case "cancelled context":
				cancel()
			}
			host := &taskToolHostStub{authorization: &current}
			ledger := &taskToolLedgerStub{}
			_, err := NewTaskToolService(state, ledger, host).Invoke(ctx, TaskToolInvocation{WorkspaceID: "workspace", TaskRunID: "run", Tool: agentsdk.AgentToolQueryRecords})
			if err == nil || ledger.start.TaskRunID != "" || host.invokes != 0 {
				t.Fatalf("invalid scope reached budget/effect: err=%v invokes=%d", err, host.invokes)
			}
		})
	}
}
