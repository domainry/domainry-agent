package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-foundation/requestcontext"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type retryableTaskToolError struct{ kind string }

func (e retryableTaskToolError) Error() string                { return e.kind }
func (e retryableTaskToolError) ApplicationErrorKind() string { return e.kind }

type recordingTaskModel struct {
	request     agentsdk.ConversationModelRequest
	workspaceID string
	actorID     string
}

type taskReadModel struct {
	steps  []agentsdk.ConversationStepRequest
	calls  []agentsdk.ConversationToolCall
	stream bool
}

func (*taskReadModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("tool-enabled task used one-shot generation")
}

func (*taskReadModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "task-reader", Fingerprint: "task-reader-v1"}
}

func (m *taskReadModel) StreamConversationStep(_ context.Context, request agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	m.steps = append(m.steps, request)
	index := len(m.steps) - 1
	if index < len(m.calls) {
		content := fmt.Sprintf("先核对第 %d 项授权来源，再继续判断。", index+1)
		if m.stream {
			first := "先核"
			if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Offset: 0, Delta: first}); err != nil {
				return agentsdk.ConversationStepResult{}, err
			}
			if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Offset: len(first), Delta: strings.TrimPrefix(content, first)}); err != nil {
				return agentsdk.ConversationStepResult{}, err
			}
			if err := emit(agentsdk.ConversationModelEvent{Type: "tool.started", Index: 0, CallID: m.calls[index].ID, Name: m.calls[index].Name}); err != nil {
				return agentsdk.ConversationStepResult{}, err
			}
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: content, ToolCalls: []agentsdk.ConversationToolCall{m.calls[index]}}, FinishReason: "tool_calls", Model: "task-reader", Usage: map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(2), "prompt_tokens_details": map[string]any{"cached_tokens": float64(3)}}}, nil
	}
	content := `{"source_hash":"source-hash","summary":"依据已授权来源"}`
	if m.stream {
		if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Offset: 0, Delta: content}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: content}, FinishReason: "stop", Model: "task-reader", Usage: map[string]any{"prompt_tokens": float64(20), "completion_tokens": float64(5)}}, nil
}

func TestModelTaskRunnerUsesAuthorizedReadGatewayBeforeStructuredResult(t *testing.T) {
	model := &taskReadModel{stream: true, calls: []agentsdk.ConversationToolCall{
		{ID: "call-message", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"email_message","record_id":"message-1"}`},
		{ID: "call-thread", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"email_thread","record_id":"thread-1"}`},
	}}
	runner := NewModelTaskRunner(model)
	var invoked []string
	var events []agentmodulehost.TaskRuntimeEvent
	runner.BindEventPublisher(func(_ context.Context, event agentmodulehost.TaskRuntimeEvent) error {
		events = append(events, event)
		return nil
	})
	runner.BindToolInvoker(func(ctx context.Context, request agentsdk.TaskRequest, call agentsdk.ConversationToolCall, input map[string]any) (any, error) {
		if request.ExecutionCredential != "current-task-credential" || request.TaskRunID != "task-1" {
			t.Fatalf("read gateway received wrong task authority: %+v", request)
		}
		invoked = append(invoked, call.Name+":"+input["record_id"].(string))
		if input["record_id"] == "message-1" {
			if err := toolsdk.ReportProgress(ctx, toolsdk.Progress{Key: "query", Status: "started", Detail: "正在查询授权记录"}); err != nil {
				return nil, err
			}
			if err := toolsdk.ReportProgress(ctx, toolsdk.Progress{Key: "query", Status: "completed", Detail: "授权记录查询完成"}); err != nil {
				return nil, err
			}
		}
		return map[string]any{"id": input["record_id"], "body": "已授权的原文"}, nil
	})
	request := agentsdk.TaskRequest{
		TaskRunID: "task-1", WorkspaceID: "workspace-1", IdempotencyKey: "task-idempotency", ExecutionCredential: "current-task-credential",
		Task:  agentsdk.AgentTaskDefinition{Instruction: "Read message and thread before analysis.", OutputSchema: map[string]any{"type": "object"}, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 3, MaxToolCalls: 2, MaxInputBytes: 1024, MaxOutputBytes: 4096}},
		Input: map[string]any{"message_id": "message-1"}, AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"email_message", "email_thread"},
	}
	result, err := runner.Start(t.Context(), request)
	if err != nil || result.Status != agentsdk.ProviderRunCompleted || result.Output["summary"] != "依据已授权来源" || len(invoked) != 2 {
		t.Fatalf("authorized task result=%+v calls=%v err=%v", result, invoked, err)
	}
	if result.Usage["prompt_tokens"] != float64(40) || result.Usage["completion_tokens"] != float64(9) {
		t.Fatalf("task usage did not include all model steps: %+v", result.Usage)
	}
	if result.Usage["prompt_tokens_details"].(map[string]any)["cached_tokens"] != float64(6) {
		t.Fatalf("task nested usage did not include all model steps: %+v", result.Usage)
	}
	if model.steps[0].MaxToolCalls != 2 || model.steps[1].MaxToolCalls != 1 || model.steps[2].MaxToolCalls != 1 || len(model.steps[2].Tools) != 0 {
		t.Fatalf("task model tool budget leaked across steps: %+v", model.steps)
	}
	if got := model.steps[2].Messages; len(got) != 6 || got[3].Role != "tool" || got[3].ToolCallID != "call-message" || !strings.Contains(got[3].Content, "已授权的原文") || got[5].ToolCallID != "call-thread" {
		t.Fatalf("task model did not receive ordered authorized tool results: %+v", got)
	}
	wantTypes := []string{
		agentmodulehost.TaskRuntimeEventModelStarted, agentmodulehost.TaskRuntimeEventTextDelta, agentmodulehost.TaskRuntimeEventTextDelta, agentmodulehost.TaskRuntimeEventDecision,
		agentmodulehost.TaskRuntimeEventToolStarted, agentmodulehost.TaskRuntimeEventToolProgress, agentmodulehost.TaskRuntimeEventToolProgress, agentmodulehost.TaskRuntimeEventToolCompleted,
		agentmodulehost.TaskRuntimeEventModelStarted, agentmodulehost.TaskRuntimeEventTextDelta, agentmodulehost.TaskRuntimeEventTextDelta, agentmodulehost.TaskRuntimeEventDecision,
		agentmodulehost.TaskRuntimeEventToolStarted, agentmodulehost.TaskRuntimeEventToolCompleted,
		agentmodulehost.TaskRuntimeEventModelStarted, agentmodulehost.TaskRuntimeEventDecision,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("runtime events=%+v", events)
	}
	for index, want := range wantTypes {
		if events[index].Type != want || events[index].WorkspaceID != "workspace-1" || events[index].TaskRunID != "task-1" || events[index].IdempotencyKey == "" {
			t.Fatalf("runtime event[%d]=%+v want type %q", index, events[index], want)
		}
	}
	if events[3].FinishReason != "tool_calls" || len(events[3].ToolCalls) != 1 || events[3].ToolCalls[0].Input["record_id"] != "message-1" {
		t.Fatalf("first accepted decision was not published: %+v", events[3])
	}
	decisionOutput, decisionOK := events[3].Output.(map[string]any)
	firstDelta, firstOK := events[1].Output.(map[string]any)
	secondDelta, secondOK := events[2].Output.(map[string]any)
	if !firstOK || firstDelta["offset"] != 0 || firstDelta["delta"] != "先核" || firstDelta["public_text"] != "先核" || !secondOK || secondDelta["public_text"] != "先核对第 1 项授权来源，再继续判断。" || !decisionOK || decisionOutput["public_streamed"] != true {
		t.Fatalf("operator-visible model narration was not streamed incrementally: first=%+v second=%+v decision=%+v", events[1], events[2], events[3])
	}
	progress := events[5].Output.(map[string]any)
	if progress["progress_key"] != "query" || progress["detail"] != "正在查询授权记录" || events[5].Status != "started" {
		t.Fatalf("tool progress was not published: %+v", events[5])
	}
	if events[7].Output.(map[string]any)["body"] != "已授权的原文" || events[15].FinishReason != "stop" {
		t.Fatalf("tool result or final decision missing: %+v", events)
	}
}

func TestModelTaskRunnerFailsClosedWhenReadGatewayOrObjectScopeIsMissing(t *testing.T) {
	request := agentsdk.TaskRequest{
		TaskRunID: "task-1", WorkspaceID: "workspace-1", IdempotencyKey: "task-idempotency", ExecutionCredential: "current-task-credential",
		Task:         agentsdk.AgentTaskDefinition{Instruction: "Read a record.", OutputSchema: map[string]any{"type": "object"}, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 2, MaxToolCalls: 1, MaxOutputBytes: 4096}},
		AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"meeting"},
	}
	model := &taskReadModel{calls: []agentsdk.ConversationToolCall{{ID: "call-denied", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"email_message","record_id":"message-1"}`}}}
	runner := NewModelTaskRunner(model)
	if result, err := runner.Start(t.Context(), request); err == nil || result.ErrorCode != "agent.task.local_tools_unavailable" {
		t.Fatalf("missing task read gateway result=%+v err=%v", result, err)
	}
	called := false
	runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
		called = true
		return nil, nil
	})
	if result, err := runner.Start(t.Context(), request); err == nil || result.ErrorCode != "agent.task.local_tool_object_denied" || called {
		t.Fatalf("out-of-scope task read result=%+v called=%t err=%v", result, called, err)
	}
}

func TestModelTaskRunnerClassifiesOnlyTransientLocalToolFailuresAsRetryable(t *testing.T) {
	request := agentsdk.TaskRequest{
		TaskRunID: "task-1", WorkspaceID: "workspace-1", IdempotencyKey: "task-idempotency", ExecutionCredential: "current-task-credential",
		Task:         agentsdk.AgentTaskDefinition{Instruction: "Read a record.", OutputSchema: map[string]any{"type": "object"}, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 2, MaxToolCalls: 1, MaxInputBytes: 1024, MaxOutputBytes: 4096}},
		AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"meeting"},
	}
	for _, test := range []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "deadline", err: context.DeadlineExceeded, retryable: true},
		{name: "wrapped deadline", err: fmt.Errorf("read failed: %w", context.DeadlineExceeded), retryable: true},
		{name: "unavailable", err: retryableTaskToolError{kind: "unavailable"}, retryable: true},
		{name: "rate limited", err: retryableTaskToolError{kind: "rate_limited"}, retryable: true},
		{name: "forbidden", err: retryableTaskToolError{kind: "forbidden"}, retryable: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &taskReadModel{calls: []agentsdk.ConversationToolCall{{ID: "call", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"meeting","record_id":"meeting-1"}`}}}
			runner := NewModelTaskRunner(model)
			runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
				return nil, test.err
			})
			result, err := runner.Start(t.Context(), request)
			if err == nil || result.ErrorCode != "agent.task.local_tool_failed" || result.Retryable != test.retryable {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func (m *recordingTaskModel) GenerateConversation(ctx context.Context, request agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.request = request
	m.workspaceID = requestcontext.WorkspaceID(ctx)
	m.actorID = requestcontext.ActorID(ctx)
	return agentsdk.ConversationModelResult{
		Content: `{"title":"采购合同","kind":"document"}`,
		Model:   "vision-test-model",
		Usage:   map[string]any{"input_tokens": 42},
	}, nil
}

func TestModelTaskRunnerSendsImageAndPDFAsNativeModelInputs(t *testing.T) {
	imageData := []byte("image-bytes")
	imageDigest := sha256.Sum256(imageData)
	pdfData := []byte("%PDF-1.4\nfile-bytes")
	pdfDigest := sha256.Sum256(pdfData)
	model := &recordingTaskModel{}
	runner := NewModelTaskRunner(model)

	result, err := runner.Start(t.Context(), agentsdk.TaskRequest{
		TaskRunID:      "run-1",
		WorkspaceID:    "workspace-1",
		Identity:       agentsdk.ExecutionIdentity{Execution: agentsdk.PrincipalReference{UserID: "user-1"}},
		IdempotencyKey: "request-1",
		Input:          map[string]any{"document_type": "contract"},
		Task: agentsdk.AgentTaskDefinition{
			Instruction:  "Extract the fields for the selected scene.",
			OutputSchema: map[string]any{"type": "object", "required": []any{"title", "kind"}},
			ExecutionLimits: agentsdk.AgentExecutionLimits{
				MaxOutputBytes: 4096,
			},
		},
		Attachments: []agentsdk.TaskAttachment{
			{ID: "att-image", Filename: "scan.png", ContentType: "image/png", Bytes: int64(len(imageData)), SHA256: hex.EncodeToString(imageDigest[:]), Detail: "high", Data: imageData},
			{ID: "att-pdf", Filename: "document.pdf", ContentType: "application/pdf", Bytes: int64(len(pdfData)), SHA256: hex.EncodeToString(pdfDigest[:]), Data: pdfData},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != agentsdk.ProviderRunCompleted || result.Output["title"] != "采购合同" || result.Model != "vision-test-model" {
		t.Fatalf("unexpected task result: %+v", result)
	}
	if model.workspaceID != "workspace-1" || model.actorID != "user-1" {
		t.Fatalf("task model execution scope = %q/%q", model.workspaceID, model.actorID)
	}
	if len(model.request.Messages) != 2 {
		t.Fatalf("expected system and user messages, got %+v", model.request.Messages)
	}
	if !strings.Contains(model.request.Messages[0].Content, "Extract the fields") || !strings.Contains(model.request.Messages[0].Content, `"required"`) {
		t.Fatalf("task instruction or output schema missing from system prompt: %q", model.request.Messages[0].Content)
	}
	blocks := model.request.Messages[1].ContentBlocks
	if len(blocks) != 3 || blocks[0].Type != "text" {
		t.Fatalf("unexpected task content blocks: %+v", blocks)
	}
	if blocks[1].Type != "image" || blocks[1].Image == nil || string(blocks[1].Image.Data) != string(imageData) || blocks[1].Image.Detail != "high" {
		t.Fatalf("image was not materialized as a native model input: %+v", blocks[1])
	}
	if blocks[2].Type != "file" || blocks[2].File == nil || string(blocks[2].File.Data) != string(pdfData) {
		t.Fatalf("PDF was not materialized as a native model input: %+v", blocks[2])
	}
}

func TestDecodeTaskModelOutputRejectsNonJSONEnvelope(t *testing.T) {
	if _, err := decodeTaskModelOutput("```json\n{\"title\":\"x\"}\n```", 1024); err == nil {
		t.Fatal("Markdown-wrapped model output was accepted")
	}
	if _, err := decodeTaskModelOutput(`{"title":"x"} {"extra":true}`, 1024); err == nil {
		t.Fatal("multiple model output values were accepted")
	}
}

var _ agentsdk.ConversationModel = (*recordingTaskModel)(nil)
