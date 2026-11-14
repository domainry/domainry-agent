package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type taskFormatModel struct {
	results []agentsdk.ConversationStepResult
	errors  map[int]error
	steps   []agentsdk.ConversationStepRequest
	plain   []agentsdk.ConversationModelRequest
	after   func(int, context.Context)
}

func (*taskFormatModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "format-test", Fingerprint: "format-test-v1"}
}

func (m *taskFormatModel) answer(index int, ctx context.Context) (agentsdk.ConversationStepResult, error) {
	if m.after != nil {
		m.after(index, ctx)
	}
	if err := m.errors[index]; err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	if index >= len(m.results) {
		return agentsdk.ConversationStepResult{}, errors.New("unexpected extra model request")
	}
	return m.results[index], nil
}

func (m *taskFormatModel) StreamConversationStep(ctx context.Context, request agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	// Freeze observed requests before the runner appends to its message ledger.
	raw, _ := json.Marshal(request)
	var frozen agentsdk.ConversationStepRequest
	_ = json.Unmarshal(raw, &frozen)
	m.steps = append(m.steps, frozen)
	return m.answer(len(m.steps)-1, ctx)
}

func (m *taskFormatModel) GenerateConversation(ctx context.Context, request agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.plain = append(m.plain, request)
	result, err := m.answer(len(m.plain)-1, ctx)
	return agentsdk.ConversationModelResult{Content: result.Message.Content, Model: result.Model, Usage: result.Usage}, err
}

func taskFormatReply(text string, tokens int) agentsdk.ConversationStepResult {
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop", Model: "format-test", Usage: map[string]any{"prompt_tokens": tokens, "completion_tokens": 2, "prompt_tokens_details": map[string]any{"cached_tokens": 1}}}
}

func taskFormatRead() agentsdk.ConversationStepResult {
	result := taskFormatReply("", 10)
	result.FinishReason = "tool_calls"
	result.Message.ToolCalls = []agentsdk.ConversationToolCall{{ID: "read-source", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"meeting","record_id":"source-1"}`}}
	result.Message.ProviderState = json.RawMessage(`{"protocol":"chat_completions","opaque":"source-state"}`)
	return result
}

func taskFormatRequest() agentsdk.TaskRequest {
	return agentsdk.TaskRequest{
		TaskRunID: "task-format", WorkspaceID: "workspace-format", IdempotencyKey: "format-intent", ExecutionCredential: "current-task-credential",
		Task:         agentsdk.AgentTaskDefinition{Instruction: "Read the source and summarize it.", OutputSchema: map[string]any{"type": "object", "required": []string{"summary"}}, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 4, MaxToolCalls: 4, MaxInputBytes: 1024, MaxOutputBytes: 1024, TimeoutSeconds: 150}},
		AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"meeting"},
	}
}

func TestModelTaskFormatRecoveryKeepsSourceLedgerAndUsesOneRemainingStep(t *testing.T) {
	invalid := taskFormatReply(`{"summary":"known source",}`, 20)
	invalid.Message.ProviderState = json.RawMessage(`{"protocol":"chat_completions","opaque":"final-state"}`)
	model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), invalid, taskFormatReply(`{"summary":"known source"}`, 30)}}
	runner := NewModelTaskRunner(model)
	reads := 0
	runner.BindToolInvoker(func(_ context.Context, request agentsdk.TaskRequest, _ agentsdk.ConversationToolCall, _ map[string]any) (any, error) {
		reads++
		if request.ExecutionCredential != "current-task-credential" {
			t.Fatal("recovery changed task authority")
		}
		return map[string]any{"summary": "known source"}, nil
	})
	result, err := runner.Start(t.Context(), taskFormatRequest())
	if err != nil || result.Status != agentsdk.ProviderRunCompleted || result.Output["summary"] != "known source" || reads != 1 || len(model.steps) != 3 {
		t.Fatalf("status=%s error=%v reads=%d steps=%d", result.Status, err, reads, len(model.steps))
	}
	last := model.steps[2]
	if len(last.Tools) != 0 || last.MaxToolCalls != 1 || last.IdempotencyKey != "format-intent:step:2" || last.MaxOutputBytes != 1024 {
		t.Fatalf("recovery changed the step budget or exposed tools: %+v", last)
	}
	prior := model.steps[1].Messages
	if len(last.Messages) != len(prior)+2 || !reflect.DeepEqual(last.Messages[:len(prior)], prior) || !reflect.DeepEqual(last.Messages[len(prior)], invalid.Message) {
		t.Fatal("recovery lost source messages or opaque provider continuation")
	}
	if last.Messages[len(last.Messages)-1].Role != "user" || !strings.Contains(last.Messages[len(last.Messages)-1].Content, "JSON") {
		t.Fatal("recovery did not request a format-only final answer")
	}
	if result.Usage["prompt_tokens"] != float64(60) || result.Usage["completion_tokens"] != float64(6) || result.Usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(3) {
		t.Fatalf("recovery lost billed model usage: %+v", result.Usage)
	}
	if result.Usage["task_format_recovery_attempts"] != 1 || string(result.RawEvidence) != `{"summary":"known source"}` {
		t.Fatal("recovery evidence did not identify the bounded attempt and accepted final result")
	}
}

func TestModelTaskFormatRecoveryFailsClosedAtTheSameBudgets(t *testing.T) {
	for _, test := range []struct {
		name, invalid string
		steps, calls  int
		final         agentsdk.ConversationStepResult
	}{
		{name: "second invalid", invalid: `{"summary":`, steps: 4, calls: 3, final: taskFormatReply(`{"summary":`, 30)},
		{name: "no remaining step", invalid: `{"summary":`, steps: 2, calls: 2},
		{name: "empty", invalid: " ", steps: 4, calls: 2},
		{name: "too large", invalid: strings.Repeat("x", 1025), steps: 4, calls: 2},
		{name: "valid array", invalid: `[{"summary":"x"}]`, steps: 4, calls: 2},
		{name: "valid null", invalid: `null`, steps: 4, calls: 2},
		{name: "tools during recovery", invalid: `{"summary":`, steps: 4, calls: 3, final: taskFormatRead()},
		{name: "tools after stop", invalid: `{"summary":`, steps: 4, calls: 3, final: func() agentsdk.ConversationStepResult { r := taskFormatRead(); r.FinishReason = "stop"; return r }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(test.invalid, 20), test.final}}
			runner := NewModelTaskRunner(model)
			reads := 0
			runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
				reads++
				return map[string]any{"summary": "known source"}, nil
			})
			request := taskFormatRequest()
			request.Task.ExecutionLimits.MaxSteps = test.steps
			result, err := runner.Start(t.Context(), request)
			if err == nil || result.ErrorCode != "agent.task.model_output_invalid" || result.Retryable || reads != 1 || len(model.steps) != test.calls || result.Output != nil || len(result.RawEvidence) != 0 {
				t.Fatalf("result=%+v err=%v reads=%d calls=%d", result, err, reads, len(model.steps))
			}
			if result.Usage["prompt_tokens"] == nil {
				t.Fatal("failed format handling discarded model usage")
			}
		})
	}
}

func TestModelTaskFormatRecoveryDoesNotRetryModelFailureOrCancelledWork(t *testing.T) {
	for _, where := range []int{0, 2} {
		model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(`{"summary":`, 20)}, errors: map[int]error{where: errors.New("provider request failed")}}
		runner := NewModelTaskRunner(model)
		runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
			return map[string]any{"summary": "known source"}, nil
		})
		if _, err := runner.Start(t.Context(), taskFormatRequest()); err == nil || len(model.steps) != where+1 {
			t.Fatalf("provider failure was retried: calls=%d err=%v", len(model.steps), err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatRead(), taskFormatReply(`{"summary":`, 20)}, after: func(step int, _ context.Context) {
		if step == 1 {
			cancel()
		}
	}}
	runner := NewModelTaskRunner(model)
	runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
		return map[string]any{"summary": "known source"}, nil
	})
	if _, err := runner.Start(ctx, taskFormatRequest()); err == nil || len(model.steps) != 2 {
		t.Fatalf("cancelled work started another generation: calls=%d err=%v", len(model.steps), err)
	}
}

func TestModelTaskFormatRecoveryWithoutToolsRequiresAnExplicitRemainingStep(t *testing.T) {
	for _, steps := range []int{0, 1, 2} {
		model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatReply("```json\n{\"summary\":\"x\"}\n```", 10), taskFormatReply(`{"summary":"x"}`, 20)}}
		request := taskFormatRequest()
		request.AllowedTools = nil
		request.Task.ExecutionLimits.MaxSteps = steps
		result, err := NewModelTaskRunner(model).Start(t.Context(), request)
		if steps < 2 {
			if err == nil || len(model.plain) != 1 {
				t.Fatalf("undeclared extra step: err=%v calls=%d", err, len(model.plain))
			}
			continue
		}
		if err != nil || len(model.plain) != 2 || result.Output["summary"] != "x" || result.Usage["prompt_tokens"] != float64(30) {
			t.Fatalf("one-shot recovery result=%+v err=%v calls=%d", result, err, len(model.plain))
		}
		if model.plain[0].IdempotencyKey == model.plain[1].IdempotencyKey || model.plain[1].MaxOutputBytes != model.plain[0].MaxOutputBytes {
			t.Fatal("format generation reused a provider request key or changed the output cap")
		}
	}
}

func TestModelTaskRunnerKeepsTheOriginalDeadlineDuringRecovery(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	model := &taskFormatModel{results: []agentsdk.ConversationStepResult{taskFormatReply(`{"summary":`, 10), taskFormatReply(`{"summary":"x"}`, 20)}, after: func(_ int, ctx context.Context) {
		got, ok := ctx.Deadline()
		if !ok || got.After(deadline) {
			t.Error("model generation escaped the original task deadline")
		}
	}}
	request := taskFormatRequest()
	request.AllowedTools, request.Deadline = nil, deadline
	if _, err := NewModelTaskRunner(model).Start(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}
