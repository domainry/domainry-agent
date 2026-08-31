package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type InteractiveStateService struct {
	repository agentpersistence.AgentInteractiveRunRepository
	now        func() time.Time
	newID      func() string
	metricsMu  sync.Mutex
	metrics    interactiveStateMetrics
}

type interactiveStateMetrics struct {
	Created, Completed, HandedOff, PermissionDenied, ToolCalls uint64
	LatencyMilliseconds                                        uint64
}

func NewInteractiveStateService(repository agentpersistence.AgentInteractiveRunRepository) *InteractiveStateService {
	return &InteractiveStateService{repository: repository, now: time.Now, newID: newInteractiveRunID}
}

func NewInteractiveStateServiceWithRuntime(repository agentpersistence.AgentInteractiveRunRepository, now func() time.Time, newID func() string) *InteractiveStateService {
	service := NewInteractiveStateService(repository)
	if now != nil {
		service.now = now
	}
	if newID != nil {
		service.newID = newID
	}
	return service
}

func (s *InteractiveStateService) Create(ctx context.Context, run agentmodel.AgentInteractiveRun, authority agentpersistence.AgentInteractiveAuthority) (agentmodel.AgentInteractiveRun, bool, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if s == nil || s.repository == nil {
		return agentmodel.AgentInteractiveRun{}, false, unavailable("agent.interactive.repository_unavailable")
	}
	workspaceID := strings.TrimSpace(authority.WorkspaceID)
	if !authority.Known || !validWorkspace(workspaceID) || run.Context.Principal.WorkspaceID != workspaceID || run.Context.Principal.UserID != strings.TrimSpace(authority.UserID) || run.Context.Principal.RoleKey != strings.TrimSpace(authority.RoleKey) {
		return agentmodel.AgentInteractiveRun{}, false, forbidden("agent.interactive.context_denied")
	}
	now := s.now().UTC()
	run.ID = "interactive_run_" + s.newID()
	run.SessionID, run.WorkspaceID = strings.TrimSpace(run.SessionID), workspaceID
	run.UserID, run.RoleKey = strings.TrimSpace(authority.UserID), strings.TrimSpace(authority.RoleKey)
	run.RouteKey, run.AgentKey = strings.TrimSpace(run.Context.RouteKey), strings.TrimSpace(run.Context.AgentKey)
	run.EntrypointKey, run.ContextRevision = strings.TrimSpace(run.Context.EntrypointKey), strings.TrimSpace(run.Context.ContextRevision)
	run.Authorization = agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.interactive_allowed", ContextRevision: run.ContextRevision, AuthorizationRevision: strings.TrimSpace(authority.AuthorizationRevision)}
	run.Status, run.IdempotencyKey = agentmodel.AgentInteractiveRunRunning, strings.TrimSpace(run.IdempotencyKey)
	run.CreatedAt, run.UpdatedAt, run.Revision = now, now, 1
	if !run.ValidForCreate() {
		return agentmodel.AgentInteractiveRun{}, false, badRequest("agent.interactive.contract_invalid")
	}
	created, replayed, err := s.repository.CreateInteractiveRun(ctx, run)
	if err == nil {
		s.addMetric(func(metrics *interactiveStateMetrics) { metrics.Created++ })
	}
	return created, replayed, err
}

func (s *InteractiveStateService) Get(ctx context.Context, runID string, authority agentpersistence.AgentInteractiveAuthority) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentInteractiveRun{}, false, unavailable("agent.interactive.repository_unavailable")
	}
	workspaceID := strings.TrimSpace(authority.WorkspaceID)
	if !authority.Known || !validWorkspace(workspaceID) {
		s.ObservePermissionDenied(ctx)
		return agentmodel.AgentInteractiveRun{}, false, forbidden("agent.interactive.principal_denied")
	}
	run, found, err := s.repository.GetInteractiveRun(ctx, workspaceID, strings.TrimSpace(runID))
	if err != nil || !found {
		return run, found, err
	}
	if run.UserID != strings.TrimSpace(authority.UserID) || run.RoleKey != strings.TrimSpace(authority.RoleKey) || interactiveAuthorizationStale(run, authority) {
		s.ObservePermissionDenied(ctx)
		return agentmodel.AgentInteractiveRun{}, false, forbidden("agent.interactive.principal_denied")
	}
	return run, true, nil
}

func (s *InteractiveStateService) List(ctx context.Context, authority agentpersistence.AgentInteractiveAuthority, filter agentpersistence.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	if s == nil || s.repository == nil || !authority.Known || !validWorkspace(authority.WorkspaceID) {
		return nil, forbidden("agent.interactive.principal_denied")
	}
	runs, err := s.repository.ListInteractiveRuns(ctx, strings.TrimSpace(authority.WorkspaceID), strings.TrimSpace(authority.UserID), strings.TrimSpace(authority.RoleKey), filter)
	if err != nil {
		return nil, err
	}
	visible := make([]agentmodel.AgentInteractiveRun, 0, len(runs))
	for _, run := range runs {
		if !interactiveAuthorizationStale(run, authority) {
			visible = append(visible, run)
		}
	}
	return visible, nil
}

func (s *InteractiveStateService) Complete(ctx context.Context, run agentmodel.AgentInteractiveRun, status agentmodel.AgentInteractiveRunStatus, result map[string]any, errorCode string) (agentmodel.AgentInteractiveRun, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || !status.Terminal() || status == agentmodel.AgentInteractiveRunHandedOff {
		return agentmodel.AgentInteractiveRun{}, conflict("agent.interactive.transition_invalid")
	}
	expected, now := run.Revision, s.now().UTC()
	run.Status, run.StructuredResult, run.ErrorCode = status, cloneTaskMap(result), strings.TrimSpace(errorCode)
	run.UpdatedAt, run.CompletedAt, run.Revision = now, &now, run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, err
	}
	if !updated {
		return agentmodel.AgentInteractiveRun{}, conflict("agent.interactive.revision_conflict")
	}
	s.addMetric(func(metrics *interactiveStateMetrics) {
		metrics.Completed++
		if elapsed := now.Sub(run.CreatedAt).Milliseconds(); elapsed > 0 {
			metrics.LatencyMilliseconds += uint64(elapsed)
		}
	})
	return run, nil
}

func (s *InteractiveStateService) HandoffTask(ctx context.Context, run agentmodel.AgentInteractiveRun, route agentpersistence.AgentInteractiveRoute, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || route.RouteType != "agent_task" || strings.TrimSpace(route.TargetKey) != task.TaskKey || strings.TrimSpace(route.TargetVersion) != task.TaskVersion || strings.TrimSpace(route.IdempotencyKey) == "" {
		return agentmodel.AgentInteractiveRun{}, false, conflict("agent.interactive.handoff_invalid")
	}
	task.InteractiveRunID = run.ID
	run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion = route.RouteType, route.TargetKey, route.TargetVersion
	handedOff, replayed, err := s.repository.CommitInteractiveTaskHandoff(ctx, run, run.Revision, task)
	if err == nil {
		s.addMetric(func(metrics *interactiveStateMetrics) { metrics.HandedOff++ })
	}
	return handedOff, replayed, err
}

func (s *InteractiveStateService) HandoffWorkflow(ctx context.Context, run agentmodel.AgentInteractiveRun, route agentpersistence.AgentInteractiveRoute, processID string) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || route.RouteType != "workflow" || strings.TrimSpace(route.TargetKey) == "" || strings.TrimSpace(route.IdempotencyKey) == "" || strings.TrimSpace(processID) == "" {
		return agentmodel.AgentInteractiveRun{}, false, conflict("agent.interactive.handoff_invalid")
	}
	expected, now := run.Revision, s.now().UTC()
	run.Status, run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion = agentmodel.AgentInteractiveRunHandedOff, route.RouteType, route.TargetKey, route.TargetVersion
	run.ProcessID, run.UpdatedAt, run.CompletedAt, run.Revision = strings.TrimSpace(processID), now, &now, run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if updated {
		s.addMetric(func(metrics *interactiveStateMetrics) { metrics.HandedOff++ })
		return run, false, nil
	}
	current, found, err := s.repository.GetInteractiveRun(ctx, run.WorkspaceID, run.ID)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if found && current.Status == agentmodel.AgentInteractiveRunHandedOff && current.RouteType == route.RouteType && current.RoutedTargetKey == route.TargetKey && current.ProcessID == processID {
		s.addMetric(func(metrics *interactiveStateMetrics) { metrics.HandedOff++ })
		return current, true, nil
	}
	return agentmodel.AgentInteractiveRun{}, false, conflict("agent.interactive.handoff_conflict")
}

func (s *InteractiveStateService) RecordToolInvocation(ctx context.Context, run agentmodel.AgentInteractiveRun, evidence agentmodel.AgentTaskToolInvocationEvidence) (agentmodel.AgentInteractiveRun, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || strings.TrimSpace(evidence.Ref) == "" || strings.TrimSpace(evidence.Tool) == "" {
		return agentmodel.AgentInteractiveRun{}, conflict("agent.interactive.tool_evidence_invalid")
	}
	expected := run.Revision
	run.ToolCallCount++
	run.ToolInvocations = append(run.ToolInvocations, evidence)
	run.UpdatedAt, run.Revision = s.now().UTC(), run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, err
	}
	if !updated {
		return agentmodel.AgentInteractiveRun{}, conflict("agent.interactive.revision_conflict")
	}
	s.addMetric(func(metrics *interactiveStateMetrics) { metrics.ToolCalls++ })
	return run, nil
}

func (s *InteractiveStateService) ObservePermissionDenied(context.Context) {
	if s != nil {
		s.addMetric(func(metrics *interactiveStateMetrics) { metrics.PermissionDenied++ })
	}
}

func (s *InteractiveStateService) OpenMetrics(context.Context) string {
	metrics := s.metricsSnapshot()
	return fmt.Sprintf("# HELP domainry_agent_interactive_events Interactive Agent events by outcome.\n# TYPE domainry_agent_interactive_events counter\ndomainry_agent_interactive_events{event=\"created\"} %d\ndomainry_agent_interactive_events{event=\"completed\"} %d\ndomainry_agent_interactive_events{event=\"handoff\"} %d\ndomainry_agent_interactive_events{event=\"permission_denied\"} %d\ndomainry_agent_interactive_tool_calls_total %d\ndomainry_agent_interactive_latency_milliseconds_total %d\n", metrics.Created, metrics.Completed, metrics.HandedOff, metrics.PermissionDenied, metrics.ToolCalls, metrics.LatencyMilliseconds)
}

func (s *InteractiveStateService) addMetric(update func(*interactiveStateMetrics)) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	update(&s.metrics)
}

func (s *InteractiveStateService) metricsSnapshot() interactiveStateMetrics {
	if s == nil {
		return interactiveStateMetrics{}
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	return s.metrics
}

func interactiveAuthorizationStale(run agentmodel.AgentInteractiveRun, authority agentpersistence.AgentInteractiveAuthority) bool {
	stored, current := strings.TrimSpace(run.Authorization.AuthorizationRevision), strings.TrimSpace(authority.AuthorizationRevision)
	return stored != "" && current != "" && stored != current
}

func newInteractiveRunID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
}

var _ agentpersistence.AgentInteractiveStateService = (*InteractiveStateService)(nil)
