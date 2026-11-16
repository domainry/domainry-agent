package provider

import (
	"context"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/execution"
)

type taskReadGuardModel struct {
	*taskFormatModel
	alwaysReject bool
	checks       []TaskReadCompletionInput
}

func (m *taskReadGuardModel) EvaluateTaskReadCompletion(_ context.Context, input TaskReadCompletionInput) (TaskReadCompletionCheck, error) {
	m.checks = append(m.checks, input)
	completed := 0
	for _, message := range input.Messages {
		if message.Role == "tool" {
			completed++
		}
	}
	if !m.alwaysReject && completed >= 2 {
		return TaskReadCompletionCheck{Complete: true}, nil
	}
	return TaskReadCompletionCheck{PendingGroups: 1, Instruction: "The existing source page has a continuation. Read it using its actual cursor."}, nil
}

func taskNextRead() agentsdk.ConversationStepResult {
	r := taskFormatRead()
	r.Message.ToolCalls = []agentsdk.ConversationToolCall{{ID: "continue-page", Name: agentsdk.AgentToolQueryRecords, Arguments: `{"object_key":"meeting","query":{"after_id":"42"}}`}}
	return r
}

func TestTaskCompletionGuardContinuesOnlyWithinExistingAuthorityAndBudgets(t *testing.T) {
	m := &taskReadGuardModel{taskFormatModel: &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(`{"summary":"partial"}`, 20), taskNextRead(), taskFormatReply(`{"summary":"complete"}`, 30)}}}
	runner := NewModelTaskRunner(m)
	reads := 0
	runner.BindToolInvoker(func(_ context.Context, r agentsdk.TaskRequest, _ agentsdk.ConversationToolCall, _ map[string]any) (any, error) {
		reads++
		if r.ExecutionCredential != "current-task-credential" {
			t.Fatal("changed authority")
		}
		return map[string]any{"items": []any{}, "has_next": false}, nil
	})
	request := taskFormatRequest()
	request.AllowedTools = append(request.AllowedTools, agentsdk.AgentToolQueryRecords)
	result, err := runner.Start(t.Context(), request)
	if err != nil || result.Output["summary"] != "complete" || reads != 2 || len(m.steps) != 4 || result.Usage["task_read_completion_rejections"] != 1 || result.Usage["task_read_completion_pending_groups"] != 0 {
		t.Fatalf("result=%+v err=%v reads=%d steps=%d", result, err, reads, len(m.steps))
	}
	if m.checks[0].RemainingCalls != 3 || m.checks[0].RemainingCostUnits != 19 || m.checks[0].RemainingModelSteps != 2 {
		t.Fatalf("incorrect completion budget: %+v", m.checks[0])
	}
	if !strings.Contains(m.steps[2].Messages[len(m.steps[2].Messages)-1].Content, "final result was not accepted") || m.steps[2].IdempotencyKey != "format-intent:step:2" {
		t.Fatal("missing explicit bounded continuation")
	}
}

func TestTaskCompletionGuardNeverAcceptsPartialResultAtLimits(t *testing.T) {
	for _, limitCalls := range []int{1, 4} {
		m := &taskReadGuardModel{alwaysReject: true, taskFormatModel: &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(`{"summary":"partial"}`, 20), taskFormatReply(`{"summary":"partial"}`, 20), taskFormatReply(`{"summary":"partial"}`, 20)}}}
		runner := NewModelTaskRunner(m)
		reads := 0
		runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
			reads++
			return map[string]any{}, nil
		})
		request := taskFormatRequest()
		request.AllowedTools = append(request.AllowedTools, agentsdk.AgentToolQueryRecords)
		request.Task.ExecutionLimits.MaxToolCalls = limitCalls
		result, err := runner.Start(t.Context(), request)
		if err == nil || result.ErrorCode != "agent.task.required_reads_incomplete" || result.Retryable || result.Output != nil || len(result.RawEvidence) != 0 || reads != 1 || len(m.steps) > 4 {
			t.Fatalf("partial result accepted or limits expanded: result=%+v err=%v reads=%d steps=%d", result, err, reads, len(m.steps))
		}
		if limitCalls == 1 && len(m.steps) != 2 {
			t.Fatal("continued after call budget exhausted")
		}
	}
}

func TestTaskRunnerUsesDurableCostSnapshotAndFiltersUnaffordableTools(t *testing.T) {
	m := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(`{"summary":"known source"}`, 20)}}
	runner := NewModelTaskRunner(m)
	runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
		return map[string]any{}, nil
	})
	request := taskFormatRequest()
	request.AllowedTools = append(request.AllowedTools, agentsdk.AgentToolQueryRecords)
	ctx := execution.WithTaskBudget(t.Context(), execution.TaskBudget{Limit: execution.Budget{Calls: 4, Cost: 5}, Used: execution.Usage{Calls: 1, Cost: 4}})
	result, err := runner.Start(ctx, request)
	if err != nil || result.Status != agentsdk.ProviderRunCompleted {
		t.Fatal(result, err)
	}
	if len(m.steps[0].Tools) != 1 || m.steps[0].Tools[0].Key != agentsdk.AgentToolGetRecord || !strings.Contains(m.steps[0].Messages[0].Content, "cost_units_used=4; cost_units_remaining=1; max_cost_units=5") || len(m.steps[1].Tools) != 0 {
		t.Fatal("ignored reserved cost or exposed unaffordable tools")
	}
}

func TestTaskRunnerRejectsOverCostBatchBeforeExecutingAnyRead(t *testing.T) {
	r := taskNextRead()
	second := r.Message.ToolCalls[0]
	second.ID = "other-page"
	r.Message.ToolCalls = append(r.Message.ToolCalls, second)
	m := &taskFormatModel{results: []agentsdk.ConversationStepResult{r}}
	runner := NewModelTaskRunner(m)
	reads := 0
	runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
		reads++
		return nil, nil
	})
	request := taskFormatRequest()
	request.AllowedTools = append(request.AllowedTools, agentsdk.AgentToolQueryRecords)
	ctx := execution.WithTaskBudget(t.Context(), execution.TaskBudget{Limit: execution.Budget{Calls: 4, Cost: 3}})
	result, err := runner.Start(ctx, request)
	if err == nil || result.ErrorCode != "agent.task.local_tool_budget_exceeded" || reads != 0 {
		t.Fatalf("over-cost batch partly executed: result=%+v err=%v reads=%d", result, err, reads)
	}
}
