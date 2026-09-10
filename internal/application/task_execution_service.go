package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-agent/definition"
	"github.com/domainry/domainry-agent/internal/execution"
)

type taskLocator struct{ workspaceID, runID string }

// TaskExecutionService is the Agent-owned durable asynchronous task
// capability. Start only persists and accepts a command; provider execution,
// polling, retries and the later Runtime completion notification happen on the
// Agent worker, outside the Runtime workflow call stack.
type TaskExecutionService struct {
	state        agentpersistence.AgentTaskStateService
	provider     agentsdk.TaskRunner
	workerID     string
	pollInterval time.Duration
	leaseTTL     time.Duration
	wakeups      chan taskLocator

	mu     sync.RWMutex
	host   modulehost.TaskHost
	cancel context.CancelFunc
	done   chan struct{}
}

func NewTaskExecutionService(state agentpersistence.AgentTaskStateService, provider agentsdk.TaskRunner, workerID string) *TaskExecutionService {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		workerID = fmt.Sprintf("agent-worker-%d", time.Now().UnixNano())
	}
	return &TaskExecutionService{
		state: state, provider: provider, workerID: workerID,
		pollInterval: 500 * time.Millisecond, leaseTTL: 30 * time.Second,
		wakeups: make(chan taskLocator, 256), done: make(chan struct{}),
	}
}

func (s *TaskExecutionService) BindHost(host modulehost.TaskHost) error {
	if s == nil || host == nil {
		return fmt.Errorf("Agent task host is incomplete")
	}
	s.mu.Lock()
	s.host = host
	s.mu.Unlock()
	return nil
}

func (s *TaskExecutionService) StartWorker(parent context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s.cancel = cancel
	s.mu.Unlock()
	go s.loop(ctx)
}

func (s *TaskExecutionService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-s.done
	}
}

func (s *TaskExecutionService) Start(ctx context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	if !agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience) {
		return taskFailure("authorization", "agent.authorization.service_action_denied", false), forbidden("agent.authorization.service_action_denied")
	}
	if s == nil || s.state == nil || s.provider == nil {
		return taskFailure("configuration", "agent.task.capability_unavailable", false), unavailable("agent.task.capability_unavailable")
	}
	if err := definition.ValidateTaskRequest(request); err != nil {
		return taskFailure("request_contract", "agent.task.request_invalid", false), err
	}
	maxAttempts := request.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	timeoutSeconds := request.Task.ExecutionLimits.TimeoutSeconds
	if !request.Deadline.IsZero() {
		remaining := time.Until(request.Deadline)
		if remaining > 0 {
			timeoutSeconds = int(remaining.Round(time.Second) / time.Second)
		}
	}
	run := agentmodel.AgentTaskRun{
		ID: strings.TrimSpace(request.TaskRunID), WorkspaceID: strings.TrimSpace(request.WorkspaceID),
		ProcessID: strings.TrimSpace(request.ProcessID), NodeInstanceID: strings.TrimSpace(request.NodeInstanceID), TaskKey: strings.TrimSpace(request.Task.Key), TaskVersion: strings.TrimSpace(request.Task.Version),
		Status: agentmodel.AgentTaskRunPending, Identity: request.Identity, Input: cloneTaskMap(request.Input),
		MaxAttempts: maxAttempts, TimeoutSeconds: timeoutSeconds,
		MaxToolCalls: agentTaskMaxToolCalls(request.Task.ExecutionLimits.MaxToolCalls), MaxCostUnits: agentTaskCostBudgetUnits(request.Task.ExecutionLimits.CostBudget),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
		CorrelationID:  strings.TrimSpace(request.CorrelationID),
		Evidence: agentmodel.AgentTaskExecutionEvidence{
			TaskVersion: request.Task.Version, AgentKey: request.Task.AgentKey,
			Authorization: []agentmodel.AgentAuthorizationEvidence{{
				Decision: "allowed", Code: "agent.task.command_authorized",
				AuthorizationRevision: request.Identity.Execution.AuthorizationRevision,
				AllowedObjects:        append([]string(nil), request.AllowedObjects...),
				AllowedActions:        append([]string(nil), request.AllowedActions...),
				AllowedOutcomes:       append([]string(nil), request.AllowedOutcomes...),
				AllowedTools:          append([]string(nil), request.AllowedTools...),
			}},
		},
	}
	created, replayed, err := s.state.Create(ctx, run)
	if err != nil {
		return taskFailure("state", "agent.task.create_failed", false), err
	}
	if !replayed || !created.Status.Terminal() {
		s.Wake(created.WorkspaceID, created.ID)
	}
	return taskResultFromRun(created), nil
}

func (s *TaskExecutionService) Poll(ctx context.Context, externalRunID, idempotencyKey string) (agentsdk.TaskResult, error) {
	if !agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentTaskExecutionPoll, agentsdk.AgentRuntimeServiceAudience) {
		return taskFailure("authorization", "agent.authorization.service_action_denied", false), forbidden("agent.authorization.service_action_denied")
	}
	workspaceID, runID, ok := decodeTaskLocator(externalRunID)
	if !ok || s == nil || s.state == nil {
		return taskFailure("request_contract", "agent.task.locator_invalid", false), badRequest("agent.task.locator_invalid")
	}
	run, found, err := s.state.Get(ctx, workspaceID, runID)
	if err != nil {
		return taskFailure("state", "agent.task.read_failed", true), err
	}
	if !found {
		return agentsdk.TaskResult{Status: agentsdk.ProviderRunUnknown, ErrorClass: "not_found", ErrorCode: "agent.task.not_found"}, nil
	}
	if strings.TrimSpace(idempotencyKey) != "" && strings.TrimSpace(idempotencyKey) != run.IdempotencyKey {
		return taskFailure("request_contract", "agent.task.idempotency_mismatch", false), badRequest("agent.task.idempotency_mismatch")
	}
	if run.Status.Terminal() && run.ProcessID != "" && run.Reconciliation.Required && run.Reconciliation.State == "workflow_callback_pending" {
		go s.notifyWorkflow(context.Background(), run)
	}
	return taskResultFromRun(run), nil
}

func (s *TaskExecutionService) Cancel(ctx context.Context, externalRunID, idempotencyKey string) (agentsdk.TaskResult, error) {
	if !agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentTaskExecutionCancel, agentsdk.AgentRuntimeServiceAudience) {
		return taskFailure("authorization", "agent.authorization.service_action_denied", false), forbidden("agent.authorization.service_action_denied")
	}
	workspaceID, runID, ok := decodeTaskLocator(externalRunID)
	if !ok || s == nil || s.state == nil {
		return taskFailure("request_contract", "agent.task.locator_invalid", false), badRequest("agent.task.locator_invalid")
	}
	run, found, err := s.state.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return taskFailure("state", "agent.task.not_found", false), err
	}
	if strings.TrimSpace(idempotencyKey) != "" && strings.TrimSpace(idempotencyKey) != run.IdempotencyKey {
		return taskFailure("request_contract", "agent.task.idempotency_mismatch", false), badRequest("agent.task.idempotency_mismatch")
	}
	run, _, err = s.state.RequestCancel(ctx, workspaceID, runID, "capability cancellation requested")
	if err != nil {
		return taskFailure("state", "agent.task.cancel_failed", true), err
	}
	s.Wake(workspaceID, runID)
	return taskResultFromRun(run), nil
}

func (s *TaskExecutionService) Wake(workspaceID, runID string) {
	if s == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(runID) == "" {
		return
	}
	select {
	case s.wakeups <- taskLocator{workspaceID: strings.TrimSpace(workspaceID), runID: strings.TrimSpace(runID)}:
	default:
	}
}

func (s *TaskExecutionService) ResolveProposal(ctx context.Context, proposal agentsdk.AgentProposal, principal modulehost.Principal) error {
	if s == nil || s.state == nil {
		return unavailable("agent.task.capability_unavailable")
	}
	runID := strings.TrimSpace(fmt.Sprint(proposal.Metadata["task_run_id"]))
	if runID == "" || runID == "<nil>" {
		return nil
	}
	run, _, err := s.state.ResolveApproval(ctx, proposal.WorkspaceID, runID, agentpersistence.AgentTaskApprovalResolution{
		ProposalID: proposal.ProposalID, Decision: proposal.Status, Actor: principal.UserID,
		Reason: proposal.DecisionReason, Execution: cloneTaskMap(proposal.Execution),
	})
	if err != nil {
		return err
	}
	if run.Status.Terminal() && run.ProcessID != "" {
		run.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending", Reason: "workflow completion callback has not been acknowledged"}
		s.notifyWorkflow(ctx, run)
	}
	return nil
}

func (s *TaskExecutionService) loop(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case locator := <-s.wakeups:
			s.processLocator(ctx, locator)
		case <-ticker.C:
			s.processRecovery(ctx)
		}
	}
}

func (s *TaskExecutionService) processLocator(ctx context.Context, locator taskLocator) {
	if s.taskHost() == nil {
		return
	}
	claim, found, err := s.state.ClaimTask(ctx, locator.workspaceID, locator.runID, s.workerID, s.leaseTTL)
	if err != nil || !found {
		return
	}
	s.executeClaim(ctx, claim)
}

func (s *TaskExecutionService) processRecovery(ctx context.Context) {
	if s.taskHost() == nil {
		return
	}
	scope := agentpersistence.SystemScope{Kind: agentpersistence.AgentSystemScopeKindGlobal, Purpose: "agent_task_execution_recovery"}
	claim, found, err := s.state.ClaimNextForWorker(ctx, scope, s.workerID, s.leaseTTL)
	if err == nil && found {
		s.executeClaim(ctx, claim)
	}
	terminal, listErr := s.state.ListForWorker(ctx, scope, agentpersistence.AgentTaskRunFilter{Statuses: []agentmodel.AgentTaskRunStatus{
		agentmodel.AgentTaskRunSucceeded, agentmodel.AgentTaskRunManualReview, agentmodel.AgentTaskRunRejected,
		agentmodel.AgentTaskRunNoResult, agentmodel.AgentTaskRunFailed, agentmodel.AgentTaskRunCancelled, agentmodel.AgentTaskRunDeadLetter,
	}, Limit: 100})
	if listErr != nil {
		return
	}
	for _, run := range terminal {
		if run.ProcessID != "" && run.Reconciliation.Required && run.Reconciliation.State == "workflow_callback_pending" {
			s.notifyWorkflow(ctx, run)
		}
	}
}

func (s *TaskExecutionService) executeClaim(parent context.Context, claim agentpersistence.AgentTaskClaim) {
	run := claim.Run
	if run.CancelRequestedAt != nil {
		s.cancelClaim(parent, run, claim.Lease)
		return
	}
	host := s.taskHost()
	if host == nil {
		return
	}
	authorization, err := authorizeTaskRun(parent, host, run)
	if err != nil {
		s.failClaim(parent, run, claim.Lease, "authorization", errorCode(err, "agent.task.authorization_failed"), false)
		return
	}
	credential, err := host.IssueTaskCredential(parent, modulehost.TaskCredentialRequest{
		WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID,
		Principal: authorization.Principal, AllowedTools: append([]string(nil), authorization.AllowedTools...),
		TTLSeconds: taskCredentialTTL(run.TimeoutSeconds),
	})
	if err != nil {
		s.failClaim(parent, run, claim.Lease, "credential", errorCode(err, "agent.task.credential_failed"), true)
		return
	}
	executionContext, cancel := context.WithCancel(parent)
	if run.TimeoutSeconds > 0 {
		executionContext, cancel = context.WithTimeout(parent, time.Duration(run.TimeoutSeconds)*time.Second)
	}
	defer cancel()
	heartbeatErrors := make(chan error, 1)
	stopHeartbeat := make(chan struct{})
	go s.heartbeat(executionContext, run, claim.Lease, stopHeartbeat, heartbeatErrors, cancel)
	result, externalRunID, executeErr := s.runProvider(executionContext, run, authorization, credential)
	close(stopHeartbeat)
	select {
	case heartbeatErr := <-heartbeatErrors:
		if heartbeatErr != nil {
			executeErr = heartbeatErr
		}
	default:
	}
	if executeErr != nil {
		if len(run.Attempts) > 0 {
			run.Attempts[len(run.Attempts)-1].ExternalRunID = externalRunID
		}
		s.failClaim(parent, run, claim.Lease, "provider", valueOrDefault(result.ErrorCode, errorCode(executeErr, "agent.task.execution_failed")), providerRetryable(result, executeErr))
		return
	}
	completion, completionErr := taskCompletion(authorization, result, externalRunID, run.Evidence)
	if completionErr != nil {
		s.failClaim(parent, run, claim.Lease, "output_validation", errorCode(completionErr, "agent.task.output_invalid"), false)
		return
	}
	if run.ProcessID != "" {
		completion.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending", Reason: "workflow completion callback has not been acknowledged"}
	}
	completed, err := s.state.Complete(parent, run, claim.Lease.Owner, claim.Lease.FencingToken, completion)
	if err == nil && completed.ProcessID != "" {
		s.notifyWorkflow(parent, completed)
	}
}

func (s *TaskExecutionService) runProvider(ctx context.Context, run agentmodel.AgentTaskRun, authorization modulehost.TaskAuthorization, credential string) (agentsdk.TaskResult, string, error) {
	request := agentsdk.TaskRequest{
		TaskRunID: run.ID, ProcessID: run.ProcessID, NodeInstanceID: run.NodeInstanceID, WorkspaceID: run.WorkspaceID,
		Task: authorization.Task, Identity: authorization.Identity, Input: cloneTaskMap(run.Input),
		AllowedObjects:      append([]string(nil), authorization.Evidence.AllowedObjects...),
		AllowedActions:      append([]string(nil), authorization.Evidence.AllowedActions...),
		AllowedOutcomes:     append([]string(nil), authorization.Evidence.AllowedOutcomes...),
		AllowedTools:        append([]string(nil), authorization.AllowedTools...),
		ExecutionCredential: credential, CorrelationID: run.CorrelationID, IdempotencyKey: run.IdempotencyKey,
	}
	if run.TimeoutSeconds > 0 {
		request.Deadline = time.Now().UTC().Add(time.Duration(run.TimeoutSeconds) * time.Second)
	}
	result, err := s.provider.Start(ctx, request)
	externalRunID := strings.TrimSpace(result.ExternalRunID)
	if err != nil {
		return result, externalRunID, err
	}
	for result.Status == agentsdk.ProviderRunAccepted || result.Status == agentsdk.ProviderRunRunning {
		if externalRunID == "" {
			return result, "", fmt.Errorf("agent provider external run id is required")
		}
		select {
		case <-ctx.Done():
			return result, externalRunID, ctx.Err()
		case <-time.After(s.pollInterval):
		}
		result, err = s.provider.Poll(ctx, externalRunID, run.IdempotencyKey)
		if err != nil {
			return result, externalRunID, err
		}
	}
	return result, externalRunID, nil
}

func (s *TaskExecutionService) heartbeat(ctx context.Context, run agentmodel.AgentTaskRun, lease agentmodel.AgentTaskLease, stop <-chan struct{}, failures chan<- error, cancel context.CancelFunc) {
	ticker := time.NewTicker(s.leaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			result, err := s.state.Heartbeat(ctx, run.WorkspaceID, run.ID, lease.Owner, lease.FencingToken, s.leaseTTL)
			if err != nil || result.Lost {
				if err == nil {
					err = fmt.Errorf("Agent task lease lost")
				}
				select {
				case failures <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}

func (s *TaskExecutionService) failClaim(ctx context.Context, run agentmodel.AgentTaskRun, lease agentmodel.AgentTaskLease, class, code string, retryable bool) {
	next := time.Now().UTC().Add(time.Second * time.Duration(1<<min(run.Attempt, 10)))
	if run.ProcessID != "" && (!retryable || run.Attempt >= run.MaxAttempts) {
		run.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending", Reason: "workflow completion callback has not been acknowledged"}
	}
	failed, err := s.state.FailAttempt(ctx, run, lease.Owner, lease.FencingToken, class, code, retryable, next)
	if err != nil {
		return
	}
	if failed.Status == agentmodel.AgentTaskRunRetryScheduled {
		time.AfterFunc(time.Until(next), func() { s.Wake(failed.WorkspaceID, failed.ID) })
		return
	}
	if failed.ProcessID != "" {
		s.notifyWorkflow(ctx, failed)
	}
}

func (s *TaskExecutionService) cancelClaim(ctx context.Context, run agentmodel.AgentTaskRun, lease agentmodel.AgentTaskLease) {
	externalID := latestProviderRunID(run)
	if externalID != "" {
		_, _ = s.provider.Cancel(ctx, externalID, run.IdempotencyKey)
	}
	completion := agentpersistence.AgentTaskRunCompletion{
		Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancelled",
		Evidence: run.Evidence,
	}
	if run.ProcessID != "" {
		completion.Reconciliation = agentmodel.AgentTaskReconciliation{Required: true, State: "workflow_callback_pending", Reason: "workflow completion callback has not been acknowledged"}
	}
	completed, err := s.state.Complete(ctx, run, lease.Owner, lease.FencingToken, completion)
	if err == nil && completed.ProcessID != "" {
		s.notifyWorkflow(ctx, completed)
	}
}

func (s *TaskExecutionService) notifyWorkflow(ctx context.Context, run agentmodel.AgentTaskRun) {
	host := s.taskHost()
	if host == nil || run.ProcessID == "" || !run.Status.Terminal() {
		return
	}
	err := host.CompleteWorkflowTask(ctx, modulehost.WorkflowTaskCompletion{
		WorkspaceID: run.WorkspaceID, TaskRunID: run.ID, ProcessID: run.ProcessID, NodeInstanceID: run.NodeInstanceID,
		TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, Identity: run.Identity,
		Status: string(run.Status), Outcome: run.Outcome, Output: cloneTaskMap(run.Output), ErrorCode: run.LastErrorCode, Evidence: run.Evidence,
	})
	if err == nil {
		_, _ = s.state.MarkWorkflowCompletionDelivered(ctx, run.WorkspaceID, run.ID)
	}
}

func (s *TaskExecutionService) taskHost() modulehost.TaskHost {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.host
}

func taskCompletion(authorization modulehost.TaskAuthorization, result agentsdk.TaskResult, externalRunID string, evidence agentmodel.AgentTaskExecutionEvidence) (agentpersistence.AgentTaskRunCompletion, error) {
	switch result.Status {
	case agentsdk.ProviderRunCompleted:
		if !containsString(authorization.Evidence.AllowedOutcomes, result.Outcome) {
			return agentpersistence.AgentTaskRunCompletion{}, fmt.Errorf("Agent task outcome is not authorized")
		}
		if err := validateTaskOutput(authorization.Task.OutputSchema, result.Output, authorization.Task.ExecutionLimits.MaxOutputBytes); err != nil {
			return agentpersistence.AgentTaskRunCompletion{}, err
		}
		evidence.TaskVersion, evidence.AgentKey, evidence.Model, evidence.Usage = authorization.Task.Version, authorization.Task.AgentKey, result.Model, cloneTaskMap(result.Usage)
		evidence.Authorization = append(evidence.Authorization, authorization.Evidence)
		return agentpersistence.AgentTaskRunCompletion{
			Status: taskStatusForOutcome(result.Outcome), Outcome: result.Outcome, Output: cloneTaskMap(result.Output), ExternalRunID: externalRunID,
			RawEvidenceRef: stableHash(result.RawEvidence), Evidence: evidence,
		}, nil
	case agentsdk.ProviderRunCancelled:
		return agentpersistence.AgentTaskRunCompletion{Status: agentmodel.AgentTaskRunCancelled, Outcome: "cancelled", ErrorCode: "agent.task.cancelled", Evidence: evidence}, nil
	case agentsdk.ProviderRunFailed:
		return agentpersistence.AgentTaskRunCompletion{}, fmt.Errorf("%s", valueOrDefault(result.ErrorCode, "agent.task.provider_failed"))
	default:
		return agentpersistence.AgentTaskRunCompletion{}, fmt.Errorf("Agent provider returned unknown status %q", result.Status)
	}
}

func taskResultFromRun(run agentmodel.AgentTaskRun) agentsdk.TaskResult {
	result := agentsdk.TaskResult{ExternalRunID: encodeTaskLocator(run.WorkspaceID, run.ID), Outcome: run.Outcome, Output: cloneTaskMap(run.Output), ErrorCode: run.LastErrorCode, Usage: cloneTaskMap(run.Evidence.Usage)}
	switch run.Status {
	case agentmodel.AgentTaskRunPending, agentmodel.AgentTaskRunRetryScheduled:
		result.Status = agentsdk.ProviderRunAccepted
	case agentmodel.AgentTaskRunRunning, agentmodel.AgentTaskRunWaitingApproval:
		result.Status = agentsdk.ProviderRunRunning
	case agentmodel.AgentTaskRunSucceeded, agentmodel.AgentTaskRunManualReview, agentmodel.AgentTaskRunRejected, agentmodel.AgentTaskRunNoResult:
		result.Status = agentsdk.ProviderRunCompleted
	case agentmodel.AgentTaskRunCancelled:
		result.Status = agentsdk.ProviderRunCancelled
	case agentmodel.AgentTaskRunFailed, agentmodel.AgentTaskRunDeadLetter:
		result.Status, result.ErrorClass = agentsdk.ProviderRunFailed, "execution"
	default:
		result.Status = agentsdk.ProviderRunUnknown
	}
	return result
}

func taskFailure(class, code string, retryable bool) agentsdk.TaskResult {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunFailed, ErrorClass: class, ErrorCode: code, Retryable: retryable}
}

func encodeTaskLocator(workspaceID, runID string) string {
	return strings.TrimSpace(workspaceID) + "/" + strings.TrimSpace(runID)
}
func decodeTaskLocator(value string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), "/", 2)
	returnPart := len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
	if !returnPart {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func latestProviderRunID(run agentmodel.AgentTaskRun) string {
	for index := len(run.Attempts) - 1; index >= 0; index-- {
		if value := strings.TrimSpace(run.Attempts[index].ExternalRunID); value != "" {
			return value
		}
	}
	return strings.TrimSpace(run.Reconciliation.ExternalRunID)
}

func taskCredentialTTL(timeoutSeconds int) int {
	if timeoutSeconds <= 0 {
		return 300
	}
	if timeoutSeconds > 900 {
		return 900
	}
	return timeoutSeconds
}

func taskStatusForOutcome(outcome string) agentmodel.AgentTaskRunStatus {
	switch strings.TrimSpace(outcome) {
	case "success":
		return agentmodel.AgentTaskRunSucceeded
	case "manual_review":
		return agentmodel.AgentTaskRunManualReview
	case "rejected":
		return agentmodel.AgentTaskRunRejected
	case "no_result":
		return agentmodel.AgentTaskRunNoResult
	default:
		return agentmodel.AgentTaskRunFailed
	}
}

func validateTaskOutput(schema map[string]any, output map[string]any, maxBytes int) error {
	raw, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	if len(raw) > maxBytes {
		return fmt.Errorf("Agent task output exceeds %d bytes", maxBytes)
	}
	if strings.TrimSpace(fmt.Sprint(schema["type"])) != "object" {
		return fmt.Errorf("Agent task output schema must be an object")
	}
	schemaRaw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	compiled, err := execution.CompileSchema(schemaRaw)
	if err != nil {
		return err
	}
	return execution.ValidateJSON(compiled, raw)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(target) {
			return true
		}
	}
	return false
}

func providerRetryable(result agentsdk.TaskResult, err error) bool {
	return result.Retryable || err == context.Canceled || err == context.DeadlineExceeded
}

func errorCode(err error, fallback string) string {
	if err == nil {
		return strings.TrimSpace(fallback)
	}
	type coded interface{ ErrorCode() string }
	if value, ok := err.(coded); ok && strings.TrimSpace(value.ErrorCode()) != "" {
		return strings.TrimSpace(value.ErrorCode())
	}
	return strings.TrimSpace(fallback)
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(fallback)
}

var _ agentsdk.TaskRunner = (*TaskExecutionService)(nil)
