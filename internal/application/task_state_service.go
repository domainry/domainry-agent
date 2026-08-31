package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type TaskStateService struct {
	repository agentpersistence.AgentTaskRunRepository
	now        func() time.Time
}

func NewTaskStateService(repository agentpersistence.AgentTaskRunRepository) *TaskStateService {
	return &TaskStateService{repository: repository, now: time.Now}
}

func NewTaskStateServiceWithClock(repository agentpersistence.AgentTaskRunRepository, now func() time.Time) *TaskStateService {
	service := NewTaskStateService(repository)
	if now != nil {
		service.now = now
	}
	return service
}

func (s *TaskStateService) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.repository_unavailable")
	}
	now := s.now().UTC()
	run.ID, run.WorkspaceID = strings.TrimSpace(run.ID), strings.TrimSpace(run.WorkspaceID)
	run.TaskKey, run.TaskVersion, run.IdempotencyKey = strings.TrimSpace(run.TaskKey), strings.TrimSpace(run.TaskVersion), strings.TrimSpace(run.IdempotencyKey)
	run.Status, run.Attempt, run.Revision = agentmodel.AgentTaskRunPending, 0, 1
	run.CreatedAt, run.UpdatedAt = now, now
	if run.MaxAttempts <= 0 {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.max_attempts_invalid")
	}
	if !run.ValidForCreate() || !validWorkspace(run.WorkspaceID) {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.contract_invalid")
	}
	return s.repository.Create(ctx, run)
}

func (s *TaskStateService) Get(ctx context.Context, workspaceID, runID string) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.repository_unavailable")
	}
	workspaceID, runID = strings.TrimSpace(workspaceID), strings.TrimSpace(runID)
	if !validWorkspace(workspaceID) || runID == "" {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.query_invalid")
	}
	return s.repository.Get(ctx, workspaceID, runID)
}

func (s *TaskStateService) List(ctx context.Context, workspaceID string, filter agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return nil, unavailable("agent.task.repository_unavailable")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if !validWorkspace(workspaceID) {
		return nil, badRequest("agent.task.query_invalid")
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 500
	}
	return s.repository.List(ctx, workspaceID, filter)
}

func (s *TaskStateService) ClaimNext(ctx context.Context, workspaceID, owner string, leaseDuration time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	if s == nil || s.repository == nil {
		return agentpersistence.AgentTaskClaim{}, false, unavailable("agent.task.repository_unavailable")
	}
	workspaceID, owner = strings.TrimSpace(workspaceID), strings.TrimSpace(owner)
	if !validWorkspace(workspaceID) || owner == "" || leaseDuration <= 0 {
		return agentpersistence.AgentTaskClaim{}, false, badRequest("agent.task.claim_invalid")
	}
	return s.repository.ClaimNext(ctx, workspaceID, owner, s.now().UTC(), leaseDuration)
}

func (s *TaskStateService) ClaimTask(ctx context.Context, workspaceID, runID, owner string, leaseDuration time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	if s == nil || s.repository == nil {
		return agentpersistence.AgentTaskClaim{}, false, unavailable("agent.task.worker_unavailable")
	}
	repository, ok := s.repository.(agentpersistence.AgentTaskRunDirectClaimRepository)
	workspaceID, runID, owner = strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(owner)
	if !ok || !validWorkspace(workspaceID) || runID == "" || owner == "" || leaseDuration <= 0 {
		return agentpersistence.AgentTaskClaim{}, false, badRequest("agent.task.claim_invalid")
	}
	return repository.ClaimAgentTaskRun(ctx, workspaceID, runID, owner, s.now().UTC(), leaseDuration)
}

func (s *TaskStateService) ClaimNextForWorker(ctx context.Context, scope agentpersistence.SystemScope, owner string, leaseDuration time.Duration) (agentpersistence.AgentTaskClaim, bool, error) {
	if s == nil {
		return agentpersistence.AgentTaskClaim{}, false, unavailable("agent.task.system_worker_unavailable")
	}
	repository, ok := s.repository.(agentpersistence.AgentTaskRunSystemWorkerRepository)
	if !ok {
		return agentpersistence.AgentTaskClaim{}, false, unavailable("agent.task.system_worker_unavailable")
	}
	if !validSystemScope(scope) || strings.TrimSpace(owner) == "" || leaseDuration <= 0 {
		return agentpersistence.AgentTaskClaim{}, false, badRequest("agent.task.claim_invalid")
	}
	return repository.ClaimNextAgentTaskRunForWorker(ctx, scope, strings.TrimSpace(owner), s.now().UTC(), leaseDuration)
}

func (s *TaskStateService) ListForWorker(ctx context.Context, scope agentpersistence.SystemScope, filter agentpersistence.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if s == nil {
		return nil, unavailable("agent.task.system_worker_unavailable")
	}
	repository, ok := s.repository.(agentpersistence.AgentTaskRunSystemWorkerRepository)
	if !ok {
		return nil, unavailable("agent.task.system_worker_unavailable")
	}
	if !validSystemScope(scope) {
		return nil, badRequest("agent.task.query_invalid")
	}
	return repository.ListAgentTaskRunsForWorker(ctx, scope, filter)
}

func (s *TaskStateService) Heartbeat(ctx context.Context, workspaceID, runID, owner string, token int64, leaseDuration time.Duration) (agentpersistence.AgentTaskHeartbeatResult, error) {
	if s == nil || s.repository == nil {
		return agentpersistence.AgentTaskHeartbeatResult{}, unavailable("agent.task.repository_unavailable")
	}
	workspaceID, runID, owner = strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(owner)
	if !validWorkspace(workspaceID) || runID == "" || owner == "" || token <= 0 || leaseDuration <= 0 {
		return agentpersistence.AgentTaskHeartbeatResult{}, badRequest("agent.task.heartbeat_invalid")
	}
	return s.repository.Heartbeat(ctx, workspaceID, runID, owner, token, s.now().UTC(), leaseDuration)
}

func (s *TaskStateService) WaitForApproval(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64, proposalID string, evidence agentmodel.AgentTaskExecutionEvidence) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, unavailable("agent.task.repository_unavailable")
	}
	proposalID = strings.TrimSpace(proposalID)
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(strings.TrimSpace(owner), token) || proposalID == "" {
		return agentmodel.AgentTaskRun{}, conflict("agent.task.terminal_fence_rejected")
	}
	now := s.now().UTC()
	run.Status, run.Evidence, run.UpdatedAt, run.Revision = agentmodel.AgentTaskRunWaitingApproval, evidence, now, run.Revision+1
	run.Approval = &agentmodel.AgentTaskApproval{ProposalID: proposalID, Status: "pending", RequestedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := s.repository.SaveRunning(ctx, run, strings.TrimSpace(owner), token); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *TaskStateService) ResolveApproval(ctx context.Context, workspaceID, runID string, resolution agentpersistence.AgentTaskApprovalResolution) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.repository_unavailable")
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	resolution.ProposalID, resolution.Decision = strings.TrimSpace(resolution.ProposalID), strings.TrimSpace(resolution.Decision)
	if run.Status.Terminal() && run.Approval != nil && run.Approval.ProposalID == resolution.ProposalID && run.Approval.Decision == resolution.Decision {
		return run, true, nil
	}
	if run.Status != agentmodel.AgentTaskRunWaitingApproval || run.Approval == nil || run.Approval.ProposalID != resolution.ProposalID {
		return run, false, conflict("agent.task.approval_state_conflict")
	}
	now, expectedRevision := s.now().UTC(), run.Revision
	run.Approval.Status, run.Approval.Decision = "resolved", resolution.Decision
	run.Approval.Actor, run.Approval.Reason = strings.TrimSpace(resolution.Actor), strings.TrimSpace(resolution.Reason)
	run.Approval.Execution, run.Approval.ResolvedAt = cloneTaskMap(resolution.Execution), &now
	run.Output = map[string]any{"proposal_id": resolution.ProposalID, "decision": resolution.Decision, "execution": cloneTaskMap(resolution.Execution)}
	switch resolution.Decision {
	case "approved":
		if strings.TrimSpace(fmt.Sprint(resolution.Execution["status"])) == "failed" {
			run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunFailed, "error", "agent.task.approval_execution_failed"
		} else {
			run.Status, run.Outcome = agentmodel.AgentTaskRunSucceeded, "success"
		}
	case "rejected", "returned":
		run.Status, run.Outcome = agentmodel.AgentTaskRunRejected, "rejected"
	case "timed_out":
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunFailed, "error", "agent.task.approval_timeout"
	case "cancelled":
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunCancelled, "error", "agent.task.approval_cancelled"
	default:
		return run, false, badRequest("agent.task.approval_decision_invalid")
	}
	run.UpdatedAt, run.CompletedAt, run.Revision = now, &now, run.Revision+1
	if run.ProcessID != "" {
		run.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending", Reason: "workflow completion callback has not been acknowledged"}
	}
	if len(run.Attempts) > 0 && run.Attempts[len(run.Attempts)-1].FinishedAt == nil {
		run.Attempts[len(run.Attempts)-1].FinishedAt = &now
	}
	err = s.repository.SaveWaitingApproval(ctx, run, expectedRevision)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, false, nil
}

func (s *TaskStateService) FailAttempt(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64, errorClass, errorCode string, retryable bool, nextAttemptAt time.Time) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, unavailable("agent.task.repository_unavailable")
	}
	owner = strings.TrimSpace(owner)
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(owner, token) {
		return agentmodel.AgentTaskRun{}, conflict("agent.task.terminal_fence_rejected")
	}
	now := s.now().UTC()
	run.LastErrorCode, run.UpdatedAt, run.Revision = strings.TrimSpace(errorCode), now, run.Revision+1
	if len(run.Attempts) > 0 {
		attempt := &run.Attempts[len(run.Attempts)-1]
		attempt.FinishedAt, attempt.ErrorClass, attempt.ErrorCode, attempt.Retryable = &now, strings.TrimSpace(errorClass), run.LastErrorCode, retryable
	}
	if retryable && run.Attempt < run.MaxAttempts && nextAttemptAt.After(now) {
		next := nextAttemptAt.UTC()
		run.Status, run.NextAttemptAt = agentmodel.AgentTaskRunRetryScheduled, &next
	} else {
		run.Status, run.Outcome, run.CompletedAt = agentmodel.AgentTaskRunDeadLetter, "error", &now
	}
	err := s.repository.SaveRunning(ctx, run, owner, token)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *TaskStateService) Complete(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64, completion agentpersistence.AgentTaskRunCompletion) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, unavailable("agent.task.repository_unavailable")
	}
	owner = strings.TrimSpace(owner)
	if run.Status != agentmodel.AgentTaskRunRunning || !run.Lease.Matches(owner, token) || !completion.Status.Terminal() {
		return agentmodel.AgentTaskRun{}, conflict("agent.task.terminal_fence_rejected")
	}
	now := s.now().UTC()
	run.Status, run.Outcome, run.Output = completion.Status, strings.TrimSpace(completion.Outcome), cloneTaskMap(completion.Output)
	run.RawEvidenceRef, run.LastErrorCode = strings.TrimSpace(completion.RawEvidenceRef), strings.TrimSpace(completion.ErrorCode)
	run.Evidence, run.Reconciliation, run.UpdatedAt, run.CompletedAt, run.Revision = completion.Evidence, completion.Reconciliation, now, &now, run.Revision+1
	if len(run.Attempts) > 0 {
		run.Attempts[len(run.Attempts)-1].FinishedAt = &now
		run.Attempts[len(run.Attempts)-1].ErrorCode = run.LastErrorCode
		run.Attempts[len(run.Attempts)-1].ExternalRunID = strings.TrimSpace(completion.ExternalRunID)
	}
	err := s.repository.SaveRunning(ctx, run, owner, token)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *TaskStateService) MarkWorkflowCompletionDelivered(ctx context.Context, workspaceID, runID string) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, unavailable("agent.task.repository_unavailable")
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, err
	}
	if !run.Status.Terminal() {
		return run, conflict("agent.task.workflow_completion_not_terminal")
	}
	if !run.Reconciliation.Required && run.Reconciliation.State == "workflow_callback_delivered" {
		return run, nil
	}
	expectedRevision := run.Revision
	now := s.now().UTC()
	run.Reconciliation.Required = false
	run.Reconciliation.State = "workflow_callback_delivered"
	run.Reconciliation.Reason = ""
	run.Reconciliation.LastCheckedAt = &now
	run.UpdatedAt, run.Revision = now, run.Revision+1
	if err := s.repository.SaveTerminalOverride(ctx, run, expectedRevision); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *TaskStateService) RequestCancel(ctx context.Context, workspaceID, runID, reason string) (agentmodel.AgentTaskRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.repository_unavailable")
	}
	run, replayed, err := s.repository.RequestCancel(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(reason), s.now().UTC())
	if err != nil || run.Status != agentmodel.AgentTaskRunWaitingApproval || run.Approval == nil {
		return run, replayed, err
	}
	return s.ResolveApproval(ctx, run.WorkspaceID, run.ID, agentpersistence.AgentTaskApprovalResolution{ProposalID: run.Approval.ProposalID, Decision: "cancelled", Actor: "system", Reason: run.CancellationReason})
}

func (s *TaskStateService) ExpireApprovals(ctx context.Context, workspaceID string, before time.Time) (int, error) {
	runs, err := s.List(ctx, workspaceID, agentpersistence.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunWaitingApproval}, Limit: 500})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, run := range runs {
		if run.Approval == nil || run.Approval.ExpiresAt.IsZero() || run.Approval.ExpiresAt.After(before) {
			continue
		}
		_, replayed, resolveErr := s.ResolveApproval(ctx, run.WorkspaceID, run.ID, agentpersistence.AgentTaskApprovalResolution{ProposalID: run.Approval.ProposalID, Decision: "timed_out", Actor: "system", Reason: "approval deadline exceeded"})
		if resolveErr != nil {
			return count, resolveErr
		}
		if !replayed {
			count++
		}
	}
	return count, nil
}

func (s *TaskStateService) OverrideOutput(ctx context.Context, workspaceID, runID string, output map[string]any, actor agentpersistence.AgentTaskActor, reason string, audit agentpersistence.AgentTaskAuditAppender) (agentmodel.AgentTaskRun, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentTaskRun{}, unavailable("agent.task.repository_unavailable")
	}
	reason, workspaceID = strings.TrimSpace(reason), strings.TrimSpace(workspaceID)
	if !actor.Known || strings.TrimSpace(actor.UserID) == "" || reason == "" || strings.TrimSpace(actor.WorkspaceID) != workspaceID {
		return agentmodel.AgentTaskRun{}, badRequest("agent.task.override_evidence_required")
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, err
	}
	if !run.Status.Terminal() {
		return run, conflict("agent.task.override_terminal_required")
	}
	if audit == nil {
		return run, unavailable("agent.task.override_audit_unavailable")
	}
	now, expectedRevision := s.now().UTC(), run.Revision
	auditRef := "agent-task-override:" + run.ID + ":" + strconv.FormatInt(expectedRevision+1, 10)
	request := agentpersistence.AgentTaskAuditRequest{Event: "agent_task_output_overridden", ObjectKey: "agent_task_run", RecordID: run.ID, Actor: actor, Summary: "Agent Task output manually overridden", Before: map[string]any{"output_hash": stableHash(run.Output)}, After: map[string]any{"output_hash": stableHash(output)}, Metadata: map[string]any{"audit_ref": auditRef, "reason": reason, "task_key": run.TaskKey, "task_version": run.TaskVersion, "process_id": run.ProcessID}}
	if err := audit.AppendAgentTaskAudit(ctx, request); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	run.ManualOverrides = append(run.ManualOverrides, agentmodel.AgentTaskManualOverride{OriginalOutput: cloneTaskMap(run.Output), NewOutput: cloneTaskMap(output), Actor: actor.UserID, Reason: reason, AuditRef: auditRef, CreatedAt: now})
	run.Evidence.AuditRefs = append(run.Evidence.AuditRefs, auditRef)
	run.Output, run.UpdatedAt, run.Revision = cloneTaskMap(output), now, run.Revision+1
	if err := s.repository.SaveTerminalOverride(ctx, run, expectedRevision); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	return run, nil
}

func (s *TaskStateService) Operate(ctx context.Context, workspaceID, runID, kind, idempotencyKey, reason string, actor agentpersistence.AgentTaskActor, audit agentpersistence.AgentTaskAuditAppender) (agentmodel.AgentTaskRun, bool, error) {
	kind, idempotencyKey, reason = strings.TrimSpace(kind), strings.TrimSpace(idempotencyKey), strings.TrimSpace(reason)
	workspaceID = strings.TrimSpace(workspaceID)
	if s == nil || s.repository == nil || audit == nil {
		return agentmodel.AgentTaskRun{}, false, unavailable("agent.task.operation_unavailable")
	}
	if !actor.Known || strings.TrimSpace(actor.WorkspaceID) != workspaceID || strings.TrimSpace(actor.UserID) == "" || idempotencyKey == "" || reason == "" {
		return agentmodel.AgentTaskRun{}, false, badRequest("agent.task.operation_evidence_required")
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	for _, operation := range run.Operations {
		if operation.IdempotencyKey == idempotencyKey {
			if operation.Kind != kind {
				return run, false, conflict("backend.idempotency.key_reused")
			}
			return run, true, nil
		}
	}
	previousStatus, expectedRevision, now := run.Status, run.Revision, s.now().UTC()
	switch kind {
	case "retry":
		if run.Status != agentmodel.AgentTaskRunFailed && run.Status != agentmodel.AgentTaskRunDeadLetter && run.Status != agentmodel.AgentTaskRunCancelled {
			return run, false, conflict("agent.task.retry_state_invalid")
		}
		run.Status, run.Outcome, run.CompletedAt, run.NextAttemptAt, run.CancelRequestedAt, run.CancellationReason = agentmodel.AgentTaskRunPending, "", nil, nil, nil, ""
		run.Reconciliation = agentmodel.AgentTaskReconciliation{State: "force_restart"}
	case "reconcile":
		if !run.Reconciliation.Required || strings.TrimSpace(run.Reconciliation.ExternalRunID) == "" {
			return run, false, conflict("agent.task.reconcile_state_invalid")
		}
		run.Status, run.Outcome, run.CompletedAt, run.NextAttemptAt = agentmodel.AgentTaskRunPending, "", nil, nil
		run.Reconciliation.State, run.Reconciliation.Reason = "poll_required", "operator requested reconciliation"
	case "resolve":
		if !run.Status.Terminal() || !run.Reconciliation.Required {
			return run, false, conflict("agent.task.resolve_state_invalid")
		}
		run.Status, run.Outcome, run.LastErrorCode = agentmodel.AgentTaskRunManualReview, "manual_review", ""
		run.Reconciliation.Required, run.Reconciliation.State, run.Reconciliation.Reason = false, "manually_resolved", reason
	default:
		return run, false, badRequest("agent.task.operation_invalid")
	}
	auditRef := "agent-task-operation:" + run.ID + ":" + kind + ":" + strconv.FormatInt(expectedRevision+1, 10)
	request := agentpersistence.AgentTaskAuditRequest{Event: "agent_task_" + kind, ObjectKey: "agent_task_run", RecordID: run.ID, Actor: actor, Summary: "Agent Task operator action", Before: map[string]any{"status": previousStatus}, After: map[string]any{"status": run.Status}, Metadata: map[string]any{"audit_ref": auditRef, "reason": reason, "idempotency_key": idempotencyKey, "process_id": run.ProcessID}}
	if err := audit.AppendAgentTaskAudit(ctx, request); err != nil {
		return run, false, err
	}
	run.Operations = append(run.Operations, agentmodel.AgentTaskOperationEvidence{Kind: kind, IdempotencyKey: idempotencyKey, Actor: actor.UserID, Reason: reason, AuditRef: auditRef, CreatedAt: now})
	run.Evidence.AuditRefs = append(run.Evidence.AuditRefs, auditRef)
	run.UpdatedAt, run.Revision = now, run.Revision+1
	if err := s.repository.SaveOperationalTransition(ctx, run, previousStatus, expectedRevision); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, false, nil
}

func validWorkspace(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.EqualFold(value, "default")
}

func validSystemScope(scope agentpersistence.SystemScope) bool {
	return strings.TrimSpace(scope.Kind) == agentpersistence.AgentSystemScopeKindGlobal && strings.TrimSpace(scope.Purpose) != ""
}

func cloneTaskMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func stableHash(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

var _ agentpersistence.AgentTaskStateService = (*TaskStateService)(nil)
