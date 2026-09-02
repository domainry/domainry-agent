package application

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type TaskOperationsService struct {
	state agentpersistence.AgentTaskStateService
	audit modulehost.AuditHost
	wake  func(string, string)
}

func NewTaskOperationsService(state agentpersistence.AgentTaskStateService, audit modulehost.AuditHost, wake func(string, string)) *TaskOperationsService {
	return &TaskOperationsService{state: state, audit: audit, wake: wake}
}

func (s *TaskOperationsService) Operate(ctx context.Context, workspaceID, runID, kind, idempotencyKey, reason string, principal modulehost.Principal) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.state == nil || s.audit == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.operation_unavailable")
	}
	if !principal.HasAuthorizedAction(taskOperationActionKey(kind)) {
		return agentmodel.AgentTaskRun{}, false, forbidden("agent.authorization.action_denied")
	}
	run, replayed, err := s.state.Operate(ctx, workspaceID, runID, kind, idempotencyKey, reason, taskActor(principal), taskAuditAdapter{s.audit})
	if err == nil && !replayed && (run.Status == agentmodel.AgentTaskRunPending || run.Status == agentmodel.AgentTaskRunRetryScheduled) && s.wake != nil {
		s.wake(run.WorkspaceID, run.ID)
	}
	return run, replayed, err
}

func (s *TaskOperationsService) Cancel(ctx context.Context, workspaceID, runID, reason string, principal modulehost.Principal) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.state == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.operation_unavailable")
	}
	if !principal.HasAuthorizedAction(agentsdk.ActionAgentTasksCancel) {
		return agentmodel.AgentTaskRun{}, false, forbidden("agent.authorization.action_denied")
	}
	run, replayed, err := s.state.RequestCancel(ctx, workspaceID, runID, reason)
	if err == nil && !run.Status.Terminal() && s.wake != nil {
		s.wake(run.WorkspaceID, run.ID)
	}
	return run, replayed, err
}

func (s *TaskOperationsService) List(ctx context.Context, workspaceID string, filter agentpersistence.AgentTaskRunFilter, principal modulehost.Principal) ([]agentmodel.AgentTaskRun, error) {
	if s == nil || s.state == nil {
		return nil, unavailable("agent.task.operation_unavailable")
	}
	if !principal.HasAuthorizedAction(agentsdk.ActionAgentTasksList) {
		return nil, forbidden("agent.authorization.action_denied")
	}
	return s.state.List(ctx, workspaceID, filter)
}

func (s *TaskOperationsService) Get(ctx context.Context, workspaceID, runID string, principal modulehost.Principal) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.state == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.operation_unavailable")
	}
	if !principal.HasAuthorizedAction(agentsdk.ActionAgentTasksGet) {
		return agentmodel.AgentTaskRun{}, false, forbidden("agent.authorization.action_denied")
	}
	return s.state.Get(ctx, workspaceID, runID)
}

func taskOperationActionKey(kind string) string {
	switch strings.TrimSpace(kind) {
	case "retry":
		return agentsdk.ActionAgentTasksRetry
	case "resolve":
		return agentsdk.ActionAgentTasksResolve
	case "reconcile":
		return agentsdk.ActionAgentTasksReconcile
	default:
		return ""
	}
}

func taskActor(principal modulehost.Principal) agentpersistence.AgentTaskActor {
	return agentpersistence.AgentTaskActor{Known: principal.Known, WorkspaceID: strings.TrimSpace(principal.WorkspaceID), UserID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey)}
}

type taskAuditAdapter struct{ host modulehost.AuditHost }

func (a taskAuditAdapter) AppendAgentTaskAudit(ctx context.Context, request agentpersistence.AgentTaskAuditRequest) error {
	return a.host.AppendAgentAudit(ctx, modulehost.AuditRequest{
		Event: request.Event, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Summary: request.Summary,
		Principal: modulehost.Principal{Known: request.Actor.Known, WorkspaceID: request.Actor.WorkspaceID, UserID: request.Actor.UserID, RoleKey: request.Actor.RoleKey},
		Before:    request.Before, After: request.After, Metadata: request.Metadata,
	})
}
