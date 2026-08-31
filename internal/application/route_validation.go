package application

import (
	"encoding/json"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func ValidateInteractiveRoute(result agentsdk.RouteResult, candidates []agentsdk.RouteCandidate) (agentsdk.RouteResult, error) {
	allowed := map[string]struct{}{}
	for _, candidate := range candidates {
		key := strings.TrimSpace(candidate.RouteType) + "\x00" + strings.TrimSpace(candidate.TargetKey) + "\x00" + strings.TrimSpace(candidate.Version)
		if strings.TrimSpace(candidate.TargetKey) != "" {
			allowed[key] = struct{}{}
		}
	}
	result.RouteType = strings.TrimSpace(result.RouteType)
	result.TargetKey = strings.TrimSpace(result.TargetKey)
	result.TargetVersion = strings.TrimSpace(result.TargetVersion)
	if _, ok := allowed[result.RouteType+"\x00"+result.TargetKey+"\x00"+result.TargetVersion]; !ok {
		return agentsdk.RouteResult{}, forbidden("agent.router.target_denied")
	}
	switch result.RouteType {
	case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal:
	default:
		return agentsdk.RouteResult{}, forbidden("agent.router.route_type_denied")
	}
	encoded, err := json.Marshal(result.Input)
	if err != nil || len(encoded) > 64*1024 {
		return agentsdk.RouteResult{}, badRequest("agent.router.input_invalid")
	}
	if result.RouteType != agentsdk.AgentRouteInteractiveQuery && strings.TrimSpace(result.IdempotencyKey) == "" {
		return agentsdk.RouteResult{}, badRequest("agent.router.idempotency_required")
	}
	return result, nil
}
