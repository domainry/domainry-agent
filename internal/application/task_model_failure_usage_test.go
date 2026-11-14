package application

import (
	"context"
	"errors"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type taskFailureUsageState struct {
	agentpersistence.AgentTaskStateService
	failed agentmodel.AgentTaskRun
	code   string
}

func (s *taskFailureUsageState) FailAttempt(_ context.Context, run agentmodel.AgentTaskRun, owner string, token int64, _, code string, retryable bool, _ time.Time) (agentmodel.AgentTaskRun, error) {
	if owner != run.Lease.Owner || token != run.Lease.FencingToken || retryable {
		return agentmodel.AgentTaskRun{}, errors.New("failure changed the task fence or retried invalid output")
	}
	s.failed, s.code = run, code
	run.Status = agentmodel.AgentTaskRunDeadLetter
	return run, nil
}

type taskFailureUsageProvider struct {
	agentsdk.TaskRunner
	result agentsdk.TaskResult
	err    error
}

func (p taskFailureUsageProvider) Start(context.Context, agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	return p.result, p.err
}

func TestTaskWorkerKeepsModelUsageWhenGenerationOrOutputValidationFails(t *testing.T) {
	for _, schemaFailure := range []bool{false, true} {
		state := &taskFailureUsageState{}
		result := agentsdk.TaskResult{Status: agentsdk.ProviderRunFailed, ErrorCode: "agent.task.model_output_invalid", Model: "format-test", Usage: map[string]any{"prompt_tokens": 60, "completion_tokens": 6, "task_format_recovery_attempts": 1}}
		providerErr := errors.New("invalid final JSON")
		if schemaFailure {
			result.Status, result.Outcome, result.ErrorCode = agentsdk.ProviderRunCompleted, "success", ""
			result.Output, providerErr = map[string]any{"summary": 42}, nil
		}
		service := NewTaskExecutionService(state, taskFailureUsageProvider{result: result, err: providerErr}, "worker")
		authorization := modulehost.TaskAuthorization{Principal: modulehost.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}, Task: agentsdk.AgentTaskDefinition{Key: "summarize", Version: "1", OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}}, "required": []string{"summary"}}}, Evidence: agentmodel.AgentAuthorizationEvidence{AllowedOutcomes: []string{"success"}}}
		if err := service.BindHost(&taskToolHostStub{authorization: &authorization}); err != nil {
			t.Fatal(err)
		}
		run := agentmodel.AgentTaskRun{ID: "task", WorkspaceID: "workspace", TaskKey: "summarize", TaskVersion: "1", Status: agentmodel.AgentTaskRunRunning, Attempt: 1, MaxAttempts: 1, Lease: agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 3}}
		err := service.executeClaim(t.Context(), agentpersistence.AgentTaskClaim{Run: run, Lease: run.Lease})
		if err == nil || state.failed.ID != run.ID || state.failed.Evidence.Model != "format-test" || state.failed.Evidence.Usage["prompt_tokens"] != 60 || state.failed.Evidence.Usage["task_format_recovery_attempts"] != 1 {
			t.Fatalf("failure lost billed model steps: state=%+v err=%v", state, err)
		}
		if schemaFailure && state.code != "agent.task.output_invalid" {
			t.Fatal("format recovery bypassed output schema validation")
		}
		if !schemaFailure && state.code != result.ErrorCode {
			t.Fatal("model failure code changed")
		}
		if state.failed.Output != nil || state.failed.RawEvidenceRef != "" {
			t.Fatal("invalid output was accepted as durable business evidence")
		}
	}
}
