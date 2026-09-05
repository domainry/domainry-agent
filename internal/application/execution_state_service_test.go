package application

import (
	"context"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type taskStateRepositoryStub struct {
	current agentmodel.AgentTaskRun
	claim   agentpersistence.AgentTaskClaim
	saved   agentmodel.AgentTaskRun
}

func (s *taskStateRepositoryStub) Create(_ context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	s.current = run
	return run, false, nil
}
func (s *taskStateRepositoryStub) Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error) {
	return s.current, s.current.ID != "", nil
}
func (*taskStateRepositoryStub) List(context.Context, string, agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	return nil, nil
}
func (s *taskStateRepositoryStub) ClaimNext(context.Context, string, string, time.Time, time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	return s.claim, s.claim.Run.ID != "", nil
}
func (*taskStateRepositoryStub) Heartbeat(context.Context, string, string, string, int64, time.Time, time.Duration) (agentpersistence.AgentTaskHeartbeatResult, error) {
	return agentpersistence.AgentTaskHeartbeatResult{}, nil
}
func (s *taskStateRepositoryStub) SaveRunning(_ context.Context, run agentmodel.AgentTaskRun, _ string, _ int64) error {
	s.current, s.saved = run, run
	return nil
}
func (s *taskStateRepositoryStub) SaveWaitingApproval(_ context.Context, run agentmodel.AgentTaskRun, _ int64) error {
	s.current, s.saved = run, run
	return nil
}
func (s *taskStateRepositoryStub) SaveTerminalOverride(_ context.Context, run agentmodel.AgentTaskRun, _ int64) error {
	s.current, s.saved = run, run
	return nil
}
func (s *taskStateRepositoryStub) SaveOperationalTransition(_ context.Context, run agentmodel.AgentTaskRun, _ agentmodel.AgentTaskRunStatus, _ int64) error {
	s.current, s.saved = run, run
	return nil
}
func (s *taskStateRepositoryStub) RequestCancel(_ context.Context, _, _ string, reason string, now time.Time) (agentmodel.AgentTaskRun, bool, error) {
	s.current.CancelRequestedAt, s.current.CancellationReason = &now, reason
	return s.current, false, nil
}

type taskAuditStub struct {
	request agentpersistence.AgentTaskAuditRequest
}

func (s *taskAuditStub) AppendAgentTaskAudit(_ context.Context, request agentpersistence.AgentTaskAuditRequest) error {
	s.request = request
	return nil
}

func TestTaskStateServiceOwnsCreateFenceApprovalAndOperatorTransitions(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	repository := &taskStateRepositoryStub{}
	service := NewTaskStateServiceWithClock(repository, func() time.Time { return now })
	run, replayed, err := service.Create(t.Context(), agentmodel.AgentTaskRun{ID: " run-1 ", WorkspaceID: " workspace-1 ", TaskKey: " review ", TaskVersion: " 1 ", IdempotencyKey: " command-1 ", MaxAttempts: 2})
	if err != nil || replayed || run.Status != agentmodel.AgentTaskRunPending || run.Revision != 1 || run.ID != "run-1" || !run.CreatedAt.Equal(now) {
		t.Fatalf("created=%#v replayed=%v err=%v", run, replayed, err)
	}
	run.Status, run.Attempt, run.Revision = agentmodel.AgentTaskRunRunning, 1, 2
	run.Lease = agentmodel.AgentTaskLease{Owner: "worker-1", FencingToken: 4, ExpiresAt: now.Add(time.Minute)}
	if _, err := service.WaitForApproval(t.Context(), run, "worker-1", 3, "proposal-1", agentmodel.AgentTaskExecutionEvidence{}); executionErrorCode(err) != "agent.task.terminal_fence_rejected" {
		t.Fatalf("stale fence error=%v", err)
	}
	waiting, err := service.WaitForApproval(t.Context(), run, "worker-1", 4, "proposal-1", agentmodel.AgentTaskExecutionEvidence{TaskVersion: "1"})
	if err != nil || waiting.Status != agentmodel.AgentTaskRunWaitingApproval || waiting.Approval == nil {
		t.Fatalf("waiting=%#v err=%v", waiting, err)
	}
	repository.current = waiting
	resolved, replayed, err := service.ResolveApproval(t.Context(), waiting.WorkspaceID, waiting.ID, agentpersistence.AgentTaskApprovalResolution{ProposalID: "proposal-1", Decision: "approved", Actor: "operator", Execution: map[string]any{"status": "failed"}})
	if err != nil || replayed || resolved.Status != agentmodel.AgentTaskRunFailed || resolved.CompletedAt == nil {
		t.Fatalf("resolved=%#v replayed=%v err=%v", resolved, replayed, err)
	}
	audit := &taskAuditStub{}
	repository.current = resolved
	retried, replayed, err := service.Operate(t.Context(), resolved.WorkspaceID, resolved.ID, "retry", "retry-1", "provider repaired", agentpersistence.AgentTaskActor{Known: true, WorkspaceID: resolved.WorkspaceID, UserID: "operator"}, audit)
	if err != nil || replayed || retried.Status != agentmodel.AgentTaskRunPending || audit.request.Event != "agent_task_retry" || len(retried.Operations) != 1 {
		t.Fatalf("retried=%#v replayed=%v audit=%#v err=%v", retried, replayed, audit.request, err)
	}
}

func TestTaskStateServiceRejectsOperationsForMissingRuns(t *testing.T) {
	repository := &taskStateRepositoryStub{}
	service := NewTaskStateServiceWithClock(repository, time.Now)
	actor := agentpersistence.AgentTaskActor{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}
	audit := &taskAuditStub{}

	if _, _, err := service.Operate(t.Context(), "workspace-1", "missing", "retry", "retry-missing", "operator evidence", actor, audit); executionErrorCode(err) != "agent.task.not_found" {
		t.Fatalf("missing retry error=%v", err)
	}
	if _, _, err := service.RequestCancel(t.Context(), "workspace-1", "missing", "operator evidence"); executionErrorCode(err) != "agent.task.not_found" {
		t.Fatalf("missing cancel error=%v", err)
	}
	if _, err := service.OverrideOutput(t.Context(), "workspace-1", "missing", map[string]any{"status": "resolved"}, actor, "operator evidence", audit); executionErrorCode(err) != "agent.task.not_found" {
		t.Fatalf("missing override error=%v", err)
	}
}

type interactiveStateRepositoryStub struct {
	run     agentmodel.AgentInteractiveRun
	updated bool
}

func (s *interactiveStateRepositoryStub) CreateInteractiveRun(_ context.Context, run agentmodel.AgentInteractiveRun) (agentmodel.AgentInteractiveRun, bool, error) {
	s.run = run
	return run, false, nil
}
func (s *interactiveStateRepositoryStub) GetInteractiveRun(context.Context, string, string) (agentmodel.AgentInteractiveRun, bool, error) {
	return s.run, s.run.ID != "", nil
}
func (s *interactiveStateRepositoryStub) ListInteractiveRuns(context.Context, string, string, string, agentpersistence.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	return []agentmodel.AgentInteractiveRun{s.run}, nil
}
func (s *interactiveStateRepositoryStub) SaveInteractiveRun(_ context.Context, run agentmodel.AgentInteractiveRun, _ int64) (bool, error) {
	s.run = run
	return s.updated, nil
}
func (s *interactiveStateRepositoryStub) CommitInteractiveTaskHandoff(_ context.Context, run agentmodel.AgentInteractiveRun, _ int64, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	run.Status, run.TaskRunID = agentmodel.AgentInteractiveRunHandedOff, task.ID
	s.run = run
	return run, false, nil
}

func TestInteractiveStateServiceOwnsIdentityIsolationAndHandoff(t *testing.T) {
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	repository := &interactiveStateRepositoryStub{updated: true}
	service := NewInteractiveStateServiceWithRuntime(repository, func() time.Time { return now }, func() string { return "nonce" })
	authority := agentpersistence.AgentInteractiveAuthority{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", RoleKey: "operator", AuthorizationRevision: "auth-1"}
	global := agentsdk.GlobalContext{ContextRevision: "context-1", EntrypointKey: "assistant.global", AgentKey: "reviewer", RouteKey: "customer.detail", Principal: agentsdk.PrincipalReference{WorkspaceID: authority.WorkspaceID, UserID: authority.UserID, RoleKey: authority.RoleKey, AuthorizationRevision: authority.AuthorizationRevision}}
	created, replayed, err := service.Create(t.Context(), agentmodel.AgentInteractiveRun{SessionID: "session-1", IdempotencyKey: "message-1", Context: global}, authority)
	if err != nil || replayed || created.ID != "interactive_run_nonce" || created.Authorization.AuthorizationRevision != "auth-1" {
		t.Fatalf("created=%#v replayed=%v err=%v", created, replayed, err)
	}
	revoked := authority
	revoked.AuthorizationRevision = "auth-2"
	if _, _, err := service.Get(t.Context(), created.ID, revoked); executionErrorCode(err) != "agent.interactive.principal_denied" {
		t.Fatalf("revoked read error=%v", err)
	}
	task := agentmodel.AgentTaskRun{ID: "task-1", WorkspaceID: authority.WorkspaceID, TaskKey: "customer.review", TaskVersion: "1"}
	handedOff, replayed, err := service.HandoffTask(t.Context(), created, agentpersistence.AgentInteractiveRoute{RouteType: agentsdk.AgentRouteTask, TargetKey: task.TaskKey, TargetVersion: task.TaskVersion, IdempotencyKey: "handoff-1"}, task)
	if err != nil || replayed || handedOff.Status != agentmodel.AgentInteractiveRunHandedOff || handedOff.TaskRunID != task.ID {
		t.Fatalf("handoff=%#v replayed=%v err=%v", handedOff, replayed, err)
	}
}

func executionErrorCode(err error) string {
	if err == nil {
		return ""
	}
	type coded interface{ ErrorCode() string }
	if value, ok := err.(coded); ok {
		return value.ErrorCode()
	}
	return ""
}
