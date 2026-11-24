package provider

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
)

type hostReadTestModel struct {
	taskReadModel
	choose func(agentsdk.ConversationStepRequest) agentsdk.ConversationStepResult
}

func (m *hostReadTestModel) StreamConversationStep(_ context.Context, request agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	return m.choose(request), nil
}

func hostReadTestDefinition(key, description string) agentsdk.ConversationToolDefinition {
	return agentsdk.ConversationToolDefinition{
		Key: key, Version: "1", Description: description, Effect: "read", Idempotency: "natural",
		InputSchema:  json.RawMessage(`{"type":"object","required":["locator"],"properties":{"locator":{"type":"string","minLength":1}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`), TimeoutMillis: 500, MaxOutputBytes: 1024,
	}
}

func hostReadTestRequest(definitions ...agentsdk.ConversationToolDefinition) agentsdk.TaskRequest {
	request := agentsdk.TaskRequest{
		TaskRunID: "task-1", WorkspaceID: "workspace-1", ExecutionCredential: "credential-fixture", IdempotencyKey: "task-1-key",
		Task: agentsdk.AgentTaskDefinition{
			Instruction: "Use only data relevant to the case.", OutputSchema: map[string]any{"type": "object"},
			ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 4, MaxToolCalls: 3, MaxOutputBytes: 4096},
		},
		AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"support_case"}, ReadTools: definitions,
	}
	for _, definition := range definitions {
		request.AllowedTools = append(request.AllowedTools, definition.Key)
	}
	return request
}

func TestModelTaskUsesEvidenceToSelectAuthorizedHostReads(t *testing.T) {
	logs := hostReadTestDefinition("find_runtime_logs", "Find logs for a verified session.")
	code := hostReadTestDefinition("find_repository_code", "Find code for a verified error.")
	balance := hostReadTestDefinition("lookup_customer_balance", "Read the balance of the verified customer.")
	for _, tc := range []struct {
		name, caseBody, logResult string
		wantCalls                 []string
	}{
		{"bug", "interrupted run session-123", "error-456", []string{agentsdk.AgentToolGetRecord, logs.Key, code.Key}},
		{"no code locator", "interrupted run session-123", "no matching error", []string{agentsdk.AgentToolGetRecord, logs.Key}},
		{"billing", "credit balance discrepancy", "", []string{agentsdk.AgentToolGetRecord, balance.Key}},
		{"no external reads", "how do I request a refund?", "", []string{agentsdk.AgentToolGetRecord}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := func(name, arguments string) agentsdk.ConversationStepResult {
				return agentsdk.ConversationStepResult{
					Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{
						{ID: "call-" + name, Name: name, Arguments: arguments},
					}}, FinishReason: "tool_calls",
				}
			}
			request := hostReadTestRequest(logs, code, balance)
			model := &hostReadTestModel{choose: func(input agentsdk.ConversationStepRequest) agentsdk.ConversationStepResult {
				last := input.Messages[len(input.Messages)-1]
				switch {
				case last.Role != "tool":
					if len(input.Tools) != len(request.AllowedTools) || input.Tools[1].Description != logs.Description || input.Tools[2].Description != code.Description || input.Tools[3].Description != balance.Description {
						t.Fatalf("model received wrong tool definitions: %+v", input.Tools)
					}
					return step(agentsdk.AgentToolGetRecord, `{"object_key":"support_case","record_id":"case-1"}`)
				case last.ToolCallID == "call-"+agentsdk.AgentToolGetRecord && strings.Contains(last.Content, "session-123"):
					return step(logs.Key, `{"locator":"session-123"}`)
				case last.ToolCallID == "call-"+agentsdk.AgentToolGetRecord && strings.Contains(last.Content, "balance discrepancy"):
					return step(balance.Key, `{"locator":"verified-customer"}`)
				case last.ToolCallID == "call-"+logs.Key && strings.Contains(last.Content, "error-456"):
					return step(code.Key, `{"locator":"error-456"}`)
				}
				return agentsdk.ConversationStepResult{
					Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: `{"summary":"Reviewed the relevant evidence"}`}, FinishReason: "stop",
				}
			}}
			runner := NewModelTaskRunner(model)
			var calls, completed []string
			runner.BindEventPublisher(func(_ context.Context, event agentmodulehost.TaskRuntimeEvent) error {
				if event.Type == agentmodulehost.TaskRuntimeEventToolCompleted {
					completed = append(completed, event.Tool)
				}
				return nil
			})
			runner.BindToolInvoker(func(ctx context.Context, invocation agentsdk.TaskRequest, call agentsdk.ConversationToolCall, input map[string]any) (any, error) {
				if invocation.ExecutionCredential != request.ExecutionCredential || invocation.TaskRunID != request.TaskRunID {
					t.Fatal("host read did not use the task credential and run")
				}
				calls = append(calls, call.Name)
				switch call.Name {
				case agentsdk.AgentToolGetRecord:
					return map[string]any{"body": tc.caseBody}, nil
				case logs.Key:
					if _, ok := ctx.Deadline(); !ok || input["locator"] != "session-123" {
						t.Fatal("invalid log read")
					}
					return map[string]any{"error": tc.logResult}, nil
				case code.Key:
					if input["locator"] != "error-456" {
						t.Fatal("code lookup did not use the previous log result")
					}
					return map[string]any{"match": "worker.go"}, nil
				case balance.Key:
					if input["locator"] != "verified-customer" {
						t.Fatal("invalid customer balance lookup")
					}
					return map[string]any{"available_credits": 120}, nil
				}
				t.Fatalf("called an unrelated tool: %s", call.Name)
				return nil, nil
			})
			got, err := runner.Start(t.Context(), request)
			if err != nil || got.Status != agentsdk.ProviderRunCompleted || !slices.Equal(calls, tc.wantCalls) || !slices.Equal(completed, tc.wantCalls) {
				t.Fatalf("status=%s calls=%v completed=%v error=%v", got.Status, calls, completed, err)
			}
		})
	}
}

func TestModelTaskRejectsInvalidHostRead(t *testing.T) {
	read := hostReadTestDefinition("find_runtime_logs", "Read logs by verified identifier.")
	write := read
	write.Effect = "write"
	shadow := read
	shadow.Key = agentsdk.AgentToolGetRecord
	for _, tc := range []struct {
		name              string
		definitions       []agentsdk.ConversationToolDefinition
		omitAuthorization bool
		want              string
	}{
		{"unauthorized", []agentsdk.ConversationToolDefinition{read}, true, "agent.task.local_tool_unsupported"},
		{"write", []agentsdk.ConversationToolDefinition{write}, false, "agent.task.local_tool_unsupported"},
		{"reserved name", []agentsdk.ConversationToolDefinition{shadow}, false, "agent.task.local_tool_unsupported"},
		{"duplicate", []agentsdk.ConversationToolDefinition{read, read}, false, "agent.task.local_tool_unsupported"},
		{"invalid arguments", []agentsdk.ConversationToolDefinition{read}, false, "agent.task.local_tool_input_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := hostReadTestRequest(tc.definitions...)
			if tc.omitAuthorization {
				request.AllowedTools = request.AllowedTools[:1]
			}
			model := &taskReadModel{calls: []agentsdk.ConversationToolCall{
				{ID: "call-denied", Name: read.Key, Arguments: `{"locator":123}`},
			}}
			runner := NewModelTaskRunner(model)
			runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
				t.Fatal("invalid read reached the gateway")
				return nil, nil
			})
			got, err := runner.Start(t.Context(), request)
			if err == nil || got.ErrorCode != tc.want {
				t.Fatalf("error code=%q error=%v want=%q", got.ErrorCode, err, tc.want)
			}
		})
	}
}
