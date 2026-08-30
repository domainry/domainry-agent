// Package definition validates Agent-owned execution definitions before they
// are translated to any provider protocol.
package definition

import (
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func ValidateTaskRequest(request agentsdk.TaskRequest) error {
	if strings.TrimSpace(request.TaskRunID) == "" || strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("Agent task execution identity is incomplete")
	}
	return ValidateTaskDefinition(request.Task)
}

func ValidateTaskDefinition(task agentsdk.AgentTaskDefinition) error {
	if task.ContractVersion != agentsdk.AgentTaskContractVersion {
		return fmt.Errorf("unsupported Agent task contract %q", task.ContractVersion)
	}
	if strings.TrimSpace(task.Key) == "" || strings.TrimSpace(task.Version) == "" || strings.TrimSpace(task.AgentKey) == "" || strings.TrimSpace(task.Instruction) == "" {
		return fmt.Errorf("Agent task definition is incomplete")
	}
	if task.InputSchema == nil || task.OutputSchema == nil || len(task.AllowedOutcomes) == 0 {
		return fmt.Errorf("Agent task schemas and outcomes are required")
	}
	switch task.SideEffectMode {
	case agentsdk.AgentTaskSideEffectAnalysisOnly, agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed:
	default:
		return fmt.Errorf("unsupported Agent task side-effect mode %q", task.SideEffectMode)
	}
	allowed := map[string]bool{}
	for _, outcome := range agentsdk.AgentTaskOutcomes {
		allowed[outcome] = true
	}
	for _, outcome := range task.AllowedOutcomes {
		if !allowed[strings.TrimSpace(outcome)] {
			return fmt.Errorf("unsupported Agent task outcome %q", outcome)
		}
	}
	limits := task.ExecutionLimits
	if limits.MaxSteps < 0 || limits.TimeoutSeconds < 0 || limits.MaxToolCalls < 0 || limits.MaxInputBytes < 0 || limits.MaxOutputBytes < 0 {
		return fmt.Errorf("Agent execution limits cannot be negative")
	}
	return nil
}

func ValidateInteractiveRequest(request agentsdk.InteractiveRequest) error {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.Message) == "" {
		return fmt.Errorf("interactive Agent request is incomplete")
	}
	if request.Context.ContractVersion != agentsdk.GlobalAgentContextContractVersion || strings.TrimSpace(request.Context.ContextRevision) == "" || strings.TrimSpace(request.Context.EntrypointKey) == "" || strings.TrimSpace(request.Context.AgentKey) == "" {
		return fmt.Errorf("interactive Agent context is invalid")
	}
	if strings.TrimSpace(request.Context.Principal.WorkspaceID) == "" || strings.TrimSpace(request.Context.Principal.UserID) == "" {
		return fmt.Errorf("interactive Agent principal is incomplete")
	}
	for _, candidate := range request.Candidates {
		switch candidate.RouteType {
		case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal:
		default:
			return fmt.Errorf("unsupported Agent route candidate %q", candidate.RouteType)
		}
		if strings.TrimSpace(candidate.TargetKey) == "" {
			return fmt.Errorf("Agent route candidate target is required")
		}
	}
	return nil
}
