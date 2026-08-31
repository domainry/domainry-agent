package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type InteractiveExecutionDependencies struct {
	State     agentpersistence.AgentInteractiveStateService
	Runner    agentsdk.InteractiveRunner
	Host      agentmodulehost.InteractiveHost
	Proposals *ProposalService
	Now       func() time.Time
	NewID     func() string
	WakeTask  func(context.Context, string, string)
}

// InteractiveExecutionService owns the conversation execution use case.
// Runtime contributes authorization, tool and workflow capabilities only via
// InteractiveHost; it does not own this orchestration or its HTTP endpoint.
type InteractiveExecutionService struct {
	state     agentpersistence.AgentInteractiveStateService
	runner    agentsdk.InteractiveRunner
	host      agentmodulehost.InteractiveHost
	proposals *ProposalService
	now       func() time.Time
	newID     func() string
	wakeTask  func(context.Context, string, string)
}

func NewInteractiveExecutionService(dependencies InteractiveExecutionDependencies) *InteractiveExecutionService {
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	newID := dependencies.NewID
	if newID == nil {
		newID = newAgentExecutionID
	}
	return &InteractiveExecutionService{
		state: dependencies.State, runner: dependencies.Runner, host: dependencies.Host, proposals: dependencies.Proposals,
		now: now, newID: newID, wakeTask: dependencies.WakeTask,
	}
}

func (s *InteractiveExecutionService) ResolveContext(ctx context.Context, request agentmodulehost.InteractiveContextRequest) (agentsdk.GlobalContext, error) {
	if s == nil || s.host == nil {
		return agentsdk.GlobalContext{}, unavailable("agent.interactive.context_unavailable")
	}
	return s.host.ResolveInteractiveContext(ctx, request)
}

type InteractiveExecutionRequest struct {
	SessionID      string
	IdempotencyKey string
	Message        string
	Context        agentsdk.GlobalContext
	Principal      agentmodulehost.Principal
}

type InteractiveExecutionResult struct {
	Run    agentmodel.AgentInteractiveRun `json:"run"`
	Result agentsdk.InteractiveResult     `json:"result"`
}

type interactiveToolInvocationResult struct {
	agentmodulehost.InteractiveToolInvocationResult
	CallRef   string
	CostUnits int
}

func (s *InteractiveExecutionService) Execute(ctx context.Context, request InteractiveExecutionRequest) (InteractiveExecutionResult, error) {
	if s == nil || s.state == nil || s.runner == nil || s.host == nil {
		return InteractiveExecutionResult{}, unavailable("agent.interactive.runner_unavailable")
	}
	request.Message, request.IdempotencyKey = strings.TrimSpace(request.Message), strings.TrimSpace(request.IdempotencyKey)
	if request.Message == "" || request.IdempotencyKey == "" || !request.Principal.Known {
		return InteractiveExecutionResult{}, badRequest("agent.interactive.request_invalid")
	}
	authorized, err := s.host.AuthorizeInteractive(ctx, agentmodulehost.InteractiveAuthorizationRequest{Context: request.Context, Principal: request.Principal})
	if err != nil {
		s.state.ObservePermissionDenied(ctx)
		return InteractiveExecutionResult{}, err
	}
	run, replayed, err := s.state.Create(ctx, agentmodel.AgentInteractiveRun{
		SessionID: request.SessionID, EntrypointKey: authorized.Context.EntrypointKey,
		AgentKey: authorized.Context.AgentKey, Context: authorized.Context,
		CorrelationID: authorized.Principal.CorrelationID, IdempotencyKey: request.IdempotencyKey,
	}, interactiveAuthority(authorized.Principal))
	if err != nil {
		return InteractiveExecutionResult{}, err
	}
	if replayed && run.Status.Terminal() {
		return restoredInteractiveExecution(run), nil
	}
	limits := authorized.Agent.ExecutionLimits
	timeout := time.Duration(limits.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	workCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := s.runner.Run(workCtx, agentsdk.InteractiveRequest{
		RunID: run.ID, SessionID: run.SessionID, Context: authorized.Context,
		Message: request.Message, Candidates: append([]agentsdk.RouteCandidate(nil), authorized.Candidates...),
		IdempotencyKey: request.IdempotencyKey, MaxSteps: limits.MaxSteps,
		MaxToolCalls: limits.MaxToolCalls, Deadline: s.now().UTC().Add(timeout),
	})
	if runErr != nil {
		code := interactiveExecutionErrorCode(runErr, "agent.interactive.runner_failed")
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			code = "agent.interactive.timeout"
			runErr = unavailable(code)
		}
		failed, completeErr := s.state.Complete(ctx, run, agentmodel.AgentInteractiveRunFailed, nil, code)
		if completeErr != nil {
			return InteractiveExecutionResult{}, completeErr
		}
		return InteractiveExecutionResult{Run: failed, Result: result}, runErr
	}
	result.RunID = run.ID
	if result.Route != nil {
		route, routeErr := ValidateInteractiveRoute(*result.Route, authorized.Candidates)
		if routeErr != nil {
			return s.fail(ctx, run, result, routeErr)
		}
		result.Route = &route
		switch route.RouteType {
		case agentsdk.AgentRouteTask:
			return s.handoffTask(ctx, run, result, route, authorized)
		case agentsdk.AgentRouteWorkflow:
			return s.handoffWorkflow(ctx, run, result, route, authorized.Principal)
		case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal:
			updated, invocation, toolErr := s.invokeTool(ctx, run, route, authorized)
			if toolErr != nil {
				return s.fail(ctx, run, result, toolErr)
			}
			run = updated
			result.Structured = map[string]any{
				"tool": invocation.Tool, "status": invocation.Status, "output": invocation.Output,
				"proposal": invocation.Proposal, "call_ref": invocation.CallRef,
			}
		}
	}
	run.ExternalRunID, run.Model, run.Usage = strings.TrimSpace(result.ExternalRunID), strings.TrimSpace(result.Model), cloneTaskMap(result.Usage)
	completed, err := s.state.Complete(ctx, run, agentmodel.AgentInteractiveRunCompleted, result.Structured, "")
	return InteractiveExecutionResult{Run: completed, Result: result}, err
}

func (s *InteractiveExecutionService) handoffTask(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, route agentsdk.RouteResult, authorized agentmodulehost.InteractiveAuthorization) (InteractiveExecutionResult, error) {
	authorization, err := s.host.AuthorizeInteractiveTask(ctx, agentmodulehost.InteractiveTaskAuthorizationRequest{
		Principal: authorized.Principal, TaskKey: route.TargetKey, TaskVersion: route.TargetVersion,
	})
	if err != nil {
		return s.fail(ctx, run, result, err)
	}
	now := s.now().UTC()
	maxAttempts := 1
	task := agentmodel.AgentTaskRun{
		ID: "agent_task_" + s.newID(), WorkspaceID: run.WorkspaceID, InteractiveRunID: run.ID,
		TaskKey: authorization.Task.Key, TaskVersion: authorization.Task.Version,
		Status: agentmodel.AgentTaskRunPending, Identity: authorization.Identity, Input: cloneTaskMap(route.Input),
		MaxAttempts: maxAttempts, TimeoutSeconds: authorization.Task.ExecutionLimits.TimeoutSeconds,
		MaxToolCalls: agentTaskMaxToolCalls(authorization.Task.ExecutionLimits.MaxToolCalls), MaxCostUnits: agentTaskCostBudgetUnits(authorization.Task.ExecutionLimits.CostBudget),
		IdempotencyKey: run.ID + ":" + route.IdempotencyKey, CorrelationID: run.CorrelationID,
		Evidence: agentmodel.AgentTaskExecutionEvidence{
			TaskVersion: authorization.Task.Version, AgentKey: authorization.Task.AgentKey,
			Authorization: []agentmodel.AgentAuthorizationEvidence{authorization.Evidence},
		},
		CreatedAt: now, UpdatedAt: now, Revision: 1,
	}
	if !task.ValidForCreate() {
		return s.fail(ctx, run, result, badRequest("agent.task.dispatch_invalid"))
	}
	handedOff, _, err := s.state.HandoffTask(ctx, run, interactiveRoute(route), task)
	if err != nil {
		return InteractiveExecutionResult{}, err
	}
	if s.wakeTask != nil {
		s.wakeTask(ctx, task.WorkspaceID, task.ID)
	}
	result.Status = "handed_off"
	result.Handoff = &agentsdk.InteractiveAgentHandoff{
		ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: route.RouteType,
		TargetKey: route.TargetKey, Input: cloneTaskMap(route.Input), IdempotencyKey: route.IdempotencyKey,
		TaskRunID: handedOff.TaskRunID,
	}
	return InteractiveExecutionResult{Run: handedOff, Result: result}, nil
}

func (s *InteractiveExecutionService) handoffWorkflow(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, route agentsdk.RouteResult, principal agentmodulehost.Principal) (InteractiveExecutionResult, error) {
	processID, err := s.host.StartInteractiveWorkflow(ctx, agentmodulehost.InteractiveWorkflowHandoffRequest{
		WorkflowKey: route.TargetKey, Input: cloneTaskMap(route.Input), InteractiveID: run.ID,
		IdempotencyKey: route.IdempotencyKey, Principal: principal,
	})
	if err != nil {
		return s.fail(ctx, run, result, err)
	}
	handedOff, _, err := s.state.HandoffWorkflow(ctx, run, interactiveRoute(route), processID)
	if err != nil {
		return InteractiveExecutionResult{}, err
	}
	result.Status = "handed_off"
	result.Handoff = &agentsdk.InteractiveAgentHandoff{
		ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: route.RouteType,
		TargetKey: route.TargetKey, Input: cloneTaskMap(route.Input), IdempotencyKey: route.IdempotencyKey,
		ProcessID: processID,
	}
	return InteractiveExecutionResult{Run: handedOff, Result: result}, nil
}

func (s *InteractiveExecutionService) invokeTool(ctx context.Context, run agentmodel.AgentInteractiveRun, route agentsdk.RouteResult, authorized agentmodulehost.InteractiveAuthorization) (agentmodel.AgentInteractiveRun, interactiveToolInvocationResult, error) {
	tool := strings.TrimSpace(fmt.Sprint(route.Input["tool"]))
	if route.RouteType == agentsdk.AgentRouteProposal {
		tool = agentsdk.AgentToolInvokeAction
	}
	costUnits := agentToolCostUnits(tool)
	if run.ToolCallCount >= agentTaskMaxToolCalls(authorized.Agent.ExecutionLimits.MaxToolCalls) {
		return agentmodel.AgentInteractiveRun{}, interactiveToolInvocationResult{}, sdkError("rate_limited", "agent.task.tool_call_limit")
	}
	usedCost := 0
	for _, invocation := range run.ToolInvocations {
		usedCost += invocation.CostUnits
	}
	if usedCost+costUnits > agentTaskCostBudgetUnits(authorized.Agent.ExecutionLimits.CostBudget) {
		return agentmodel.AgentInteractiveRun{}, interactiveToolInvocationResult{}, sdkError("rate_limited", "agent.task.cost_budget_exceeded")
	}
	started := s.now().UTC()
	hostResult, err := s.host.InvokeInteractiveTool(ctx, agentmodulehost.InteractiveToolInvocationRequest{
		Context: run.Context, Principal: authorized.Principal, RunID: run.ID, SessionID: run.SessionID,
		EntrypointKey: run.EntrypointKey, RouteKey: run.RouteKey, CorrelationID: run.CorrelationID,
		Route: route, IdempotencyKey: route.IdempotencyKey,
	})
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, interactiveToolInvocationResult{InteractiveToolInvocationResult: hostResult}, err
	}
	invocation := interactiveToolInvocationResult{InteractiveToolInvocationResult: hostResult, CallRef: fmt.Sprintf("agent_interactive_tool_%s_%d", run.ID, run.ToolCallCount+1), CostUnits: costUnits}
	invocation.Tool = tool
	if invocation.Status == "proposal_required" {
		if s.proposals == nil {
			return agentmodel.AgentInteractiveRun{}, invocation, unavailable("agent.tool.proposal_unavailable")
		}
		proposal, proposalErr := s.proposals.CreateHostDraft(ctx, invocation.Proposal, authorized.Principal)
		if proposalErr != nil {
			return agentmodel.AgentInteractiveRun{}, invocation, proposalErr
		}
		invocation.Proposal = proposal
	}
	finished := s.now().UTC()
	evidence := agentmodel.AgentTaskToolInvocationEvidence{
		Ref: invocation.CallRef, Tool: invocation.Tool, Status: invocation.Status,
		InputHash: stableHash(route.Input), OutputHash: stableHash(map[string]any{"output": invocation.Output, "proposal": invocation.Proposal}),
		Authorization: invocation.Authorization, StartedAt: started, FinishedAt: &finished,
		DurationMilliseconds: finished.Sub(started).Milliseconds(), CostUnits: invocation.CostUnits,
	}
	updated, err := s.state.RecordToolInvocation(ctx, run, evidence)
	return updated, invocation, err
}

func (s *InteractiveExecutionService) fail(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, cause error) (InteractiveExecutionResult, error) {
	failed, err := s.state.Complete(ctx, run, agentmodel.AgentInteractiveRunFailed, nil, interactiveExecutionErrorCode(cause, "agent.interactive.failed"))
	if err != nil {
		return InteractiveExecutionResult{}, err
	}
	return InteractiveExecutionResult{Run: failed, Result: result}, cause
}

func restoredInteractiveExecution(run agentmodel.AgentInteractiveRun) InteractiveExecutionResult {
	result := agentsdk.InteractiveResult{RunID: run.ID, Status: string(run.Status), Structured: cloneTaskMap(run.StructuredResult)}
	if run.Status == agentmodel.AgentInteractiveRunHandedOff {
		result.Handoff = &agentsdk.InteractiveAgentHandoff{
			ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: run.RouteType,
			TargetKey: run.RoutedTargetKey, IdempotencyKey: run.IdempotencyKey,
			ProcessID: run.ProcessID, TaskRunID: run.TaskRunID,
		}
	}
	return InteractiveExecutionResult{Run: run, Result: result}
}

func interactiveAuthority(principal agentmodulehost.Principal) agentpersistence.AgentInteractiveAuthority {
	return agentpersistence.AgentInteractiveAuthority{
		Known: principal.Known, WorkspaceID: strings.TrimSpace(principal.WorkspaceID),
		UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey),
		AuthorizationRevision: strings.TrimSpace(principal.AuthorizationRevision),
	}
}

func interactiveRoute(route agentsdk.RouteResult) agentpersistence.AgentInteractiveRoute {
	return agentpersistence.AgentInteractiveRoute{
		RouteType: strings.TrimSpace(route.RouteType), TargetKey: strings.TrimSpace(route.TargetKey),
		TargetVersion: strings.TrimSpace(route.TargetVersion), IdempotencyKey: strings.TrimSpace(route.IdempotencyKey),
	}
}

func interactiveExecutionErrorCode(err error, fallback string) string {
	if err == nil {
		return strings.TrimSpace(fallback)
	}
	if coded, ok := err.(interface{ ErrorCode() string }); ok {
		if code := strings.TrimSpace(coded.ErrorCode()); code != "" {
			return code
		}
	}
	var sdkErr *agentsdk.Error
	if errors.As(err, &sdkErr) && strings.TrimSpace(sdkErr.Code) != "" {
		return strings.TrimSpace(sdkErr.Code)
	}
	return strings.TrimSpace(fallback)
}

func newAgentExecutionID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}
