package agent

import (
	"context"
	"strings"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func (s *AgentTaskRunStore) ListAgentTaskRunsForWorker(ctx context.Context, scope agentpersistence.SystemScope, filter agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if err := requireAgentTaskWorkerScope(scope); err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	workspaces, err := s.store.workerScopePage(ctx, min(256, max(32, limit*2)))
	if err != nil {
		return nil, err
	}
	out := []agentmodel.AgentTaskRun{}
	for _, workspaceID := range workspaces {
		runs, err := s.List(ctx, workspaceID, filter)
		if err != nil {
			return nil, err
		}
		out = append(out, runs...)
	}
	return fairAgentRuns(out, limit), nil
}

func (s *AgentTaskRunStore) ClaimNextAgentTaskRunForWorker(ctx context.Context, scope agentpersistence.SystemScope, owner string, now time.Time, duration time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	if err := requireAgentTaskWorkerScope(scope); err != nil {
		return agentpersistence.AgentTaskClaim{}, false, err
	}
	workspaces, err := s.store.workerScopePage(ctx, 64)
	if err != nil {
		return agentpersistence.AgentTaskClaim{}, false, err
	}
	for _, workspaceID := range workspaces {
		claim, found, err := s.ClaimNext(ctx, workspaceID, owner, now, duration)
		if err != nil || found {
			return claim, found, err
		}
	}
	return agentpersistence.AgentTaskClaim{}, false, nil
}

func requireAgentTaskWorkerScope(scope agentpersistence.SystemScope) error {
	if strings.TrimSpace(scope.Purpose) == "" || scope.Kind != "runtime_global" {
		return agentpersistence.ErrSystemScopeRequired
	}
	return nil
}

func fairAgentRuns(values []agentmodel.AgentTaskRun, limit int) []agentmodel.AgentTaskRun {
	if len(values) <= 1 || limit <= 0 {
		return values
	}
	queues := map[string][]agentmodel.AgentTaskRun{}
	order := []string{}
	for _, value := range values {
		if _, found := queues[value.WorkspaceID]; !found {
			order = append(order, value.WorkspaceID)
		}
		queues[value.WorkspaceID] = append(queues[value.WorkspaceID], value)
	}
	out := make([]agentmodel.AgentTaskRun, 0, min(limit, len(values)))
	for len(out) < limit {
		added := false
		for _, key := range order {
			if len(queues[key]) == 0 {
				continue
			}
			out = append(out, queues[key][0])
			queues[key] = queues[key][1:]
			added = true
			if len(out) == limit {
				break
			}
		}
		if !added {
			break
		}
	}
	return out
}

var _ agentpersistence.AgentTaskRunSystemWorkerRepository = (*AgentTaskRunStore)(nil)
