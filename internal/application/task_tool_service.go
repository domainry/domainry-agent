package application

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-agent/internal/execution"
)

type TaskToolInvocation struct {
	Credential, WorkspaceID, TaskRunID, Tool, IdempotencyKey string
	Input                                                    map[string]any
}

type TaskToolResult struct {
	Status        string                                `json:"status"`
	Tool          string                                `json:"tool"`
	CallRef       string                                `json:"call_ref"`
	Output        any                                   `json:"output,omitempty"`
	Proposal      any                                   `json:"proposal,omitempty"`
	Authorization agentmodel.AgentAuthorizationEvidence `json:"authorization"`
}

type TaskToolService struct {
	state     agentpersistence.AgentTaskStateService
	ledger    agentpersistence.AgentToolCallLedger
	host      modulehost.TaskHost
	proposals *ProposalService
}

func NewTaskToolService(state agentpersistence.AgentTaskStateService, ledger agentpersistence.AgentToolCallLedger, host modulehost.TaskHost, proposals ...*ProposalService) *TaskToolService {
	var proposalService *ProposalService
	if len(proposals) > 0 {
		proposalService = proposals[0]
	}
	return &TaskToolService{state: state, ledger: ledger, host: host, proposals: proposalService}
}

func (s *TaskToolService) Invoke(ctx context.Context, request TaskToolInvocation) (TaskToolResult, error) {
	if s == nil || s.state == nil || s.ledger == nil || s.host == nil {
		return TaskToolResult{}, unavailable("agent.tool.gateway_unavailable")
	}
	run, found, err := s.state.Get(ctx, request.WorkspaceID, request.TaskRunID)
	if err != nil {
		return TaskToolResult{}, err
	}
	if !found || run.Status != agentmodel.AgentTaskRunRunning || run.CancelRequestedAt != nil || strings.TrimSpace(run.Lease.Owner) == "" || run.Lease.FencingToken <= 0 {
		return TaskToolResult{}, forbidden("agent.tool.task_scope_denied")
	}
	tool := strings.TrimSpace(request.Tool)
	current, err := authorizeTaskRun(ctx, s.host, run)
	if err != nil {
		return TaskToolResult{}, err
	}
	if !containsString(current.AllowedTools, tool) {
		return TaskToolResult{}, forbidden("agent.tool.tool_scope_denied")
	}
	authorization := current.Evidence
	callRef, _, err := s.ledger.BeginAgentToolCall(ctx, agentpersistence.AgentToolCallStart{
		WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID,
		Tool: tool, InputHash: stableHash(request.Input), Owner: run.Lease.Owner, FencingToken: run.Lease.FencingToken,
		MaxToolCalls: agentTaskMaxToolCalls(run.MaxToolCalls), CostUnits: agentToolCostUnits(tool), MaxCostUnits: agentTaskMaxCostUnits(run.MaxCostUnits),
		Authorization: authorization,
	})
	if err != nil {
		return TaskToolResult{}, err
	}
	hostResult, err := s.host.InvokeTaskTool(ctx, modulehost.TaskToolRequest{
		Credential: request.Credential, WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID,
		Owner: run.Lease.Owner, FencingToken: run.Lease.FencingToken, Identity: current.Identity,
		TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, Tool: tool, Input: cloneTaskMap(request.Input),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), PreviousEvidence: append(append([]agentmodel.AgentAuthorizationEvidence(nil), run.Evidence.Authorization...), current.Evidence),
	})
	result := TaskToolResult{Status: hostResult.Status, Tool: hostResult.Tool, CallRef: callRef, Output: hostResult.Output, Proposal: hostResult.Proposal, Authorization: hostResult.Authorization}
	if err != nil {
		finishErr := s.finishToolCall(ctx, run, result, callRef, err)
		if finishErr != nil {
			return TaskToolResult{}, finishErr
		}
		return result, err
	}
	if result.Status != "proposal_required" {
		if err := s.finishToolCall(ctx, run, result, callRef, nil); err != nil {
			return TaskToolResult{}, err
		}
		return result, nil
	}
	if s.proposals == nil {
		_ = s.finishToolCall(ctx, run, result, callRef, unavailable("agent.tool.proposal_unavailable"))
		return TaskToolResult{}, unavailable("agent.tool.proposal_unavailable")
	}
	principal := current.Principal
	principal.CorrelationID = run.CorrelationID
	proposal, err := s.proposals.CreateHostDraft(ctx, result.Proposal, principal)
	if err != nil {
		_ = s.finishToolCall(ctx, run, result, callRef, err)
		return TaskToolResult{}, err
	}
	result.Proposal = proposal
	if err = s.finishToolCall(ctx, run, result, callRef, nil); err != nil {
		return TaskToolResult{}, err
	}
	if _, err = s.state.WaitForApproval(ctx, run, run.Lease.Owner, run.Lease.FencingToken, proposal.ProposalID, run.Evidence); err != nil {
		return TaskToolResult{}, err
	}
	return result, nil
}

func (s *TaskToolService) finishToolCall(ctx context.Context, run agentmodel.AgentTaskRun, result TaskToolResult, callRef string, cause error) error {
	status, code := strings.TrimSpace(result.Status), ""
	if cause != nil {
		status, code = "failed", errorCode(cause, "agent.tool.failed")
	}
	return s.ledger.FinishAgentToolCall(ctx, agentpersistence.AgentToolCallFinish{
		WorkspaceID: run.WorkspaceID, TaskRunID: run.ID, CallRef: callRef, Status: status, ErrorCode: code,
		Owner: run.Lease.Owner, FencingToken: run.Lease.FencingToken,
		Evidence: map[string]any{"output_hash": stableHash(map[string]any{"output": result.Output, "proposal": result.Proposal})}, Authorization: result.Authorization,
	})
}

func agentTaskMaxToolCalls(value int) int {
	if value > 0 {
		return value
	}
	return execution.DefaultMaxToolCalls
}

func agentTaskMaxCostUnits(value int) int {
	if value > 0 {
		return value
	}
	return 20
}

func agentTaskCostBudgetUnits(budget string) int {
	switch strings.ToLower(strings.TrimSpace(budget)) {
	case "low":
		return 5
	case "high":
		return 50
	default:
		return 20
	}
}

func agentToolCostUnits(tool string) int {
	switch strings.TrimSpace(tool) {
	case agentsdk.AgentToolInvokeAction:
		return 5
	case agentsdk.AgentToolQueryRecords:
		return 2
	default:
		return 1
	}
}
