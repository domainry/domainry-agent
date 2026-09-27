package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type recordingTaskModel struct {
	request agentsdk.ConversationModelRequest
}

type taskReadModel struct {
	steps []agentsdk.ConversationStepRequest
	calls []agentsdk.ConversationToolCall
}

func (*taskReadModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("tool-enabled task used one-shot generation")
}

func (*taskReadModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "task-reader", Fingerprint: "task-reader-v1"}
}

func (m *taskReadModel) StreamConversationStep(_ context.Context, request agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	m.steps = append(m.steps, request)
	index := len(m.steps) - 1
	if index < len(m.calls) {
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{m.calls[index]}}, FinishReason: "tool_calls", Model: "task-reader", Usage: map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(2), "prompt_tokens_details": map[string]any{"cached_tokens": float64(3)}}}, nil
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: `{"source_hash":"source-hash","summary":"依据已授权来源"}`}, FinishReason: "stop", Model: "task-reader", Usage: map[string]any{"prompt_tokens": float64(20), "completion_tokens": float64(5)}}, nil
}

func TestModelTaskRunnerUsesAuthorizedReadGatewayBeforeStructuredResult(t *testing.T) {
	model := &taskReadModel{calls: []agentsdk.ConversationToolCall{
		{ID: "call-message", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"email_message","record_id":"message-1"}`},
		{ID: "call-thread", Name: agentsdk.AgentToolGetRecord, Arguments: `{"object_key":"email_thread","record_id":"thread-1"}`},
	}}
	runner := NewModelTaskRunner(model)
	var invoked []string
	runner.BindToolInvoker(func(_ context.Context, request agentsdk.TaskRequest, call agentsdk.ConversationToolCall, input map[string]any) (any, error) {
		if request.ExecutionCredential != "current-task-credential" || request.TaskRunID != "task-1" {
			t.Fatalf("read gateway received wrong task authority: %+v", request)
		}
		invoked = append(invoked, call.Name+":"+input["record_id"].(string))
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

func (m *recordingTaskModel) GenerateConversation(_ context.Context, request agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.request = request
	return agentsdk.ConversationModelResult{
		Content: `{"title":"田中太郎","kind":"business_card"}`,
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
		IdempotencyKey: "request-1",
		Input:          map[string]any{"scene": "business_card"},
		Task: agentsdk.AgentTaskDefinition{
			Instruction:  "Extract the fields for the selected scene.",
			OutputSchema: map[string]any{"type": "object", "required": []any{"title", "kind"}},
			ExecutionLimits: agentsdk.AgentExecutionLimits{
				MaxOutputBytes: 4096,
			},
		},
		Attachments: []agentsdk.TaskAttachment{
			{ID: "att-image", Filename: "card.png", ContentType: "image/png", Bytes: int64(len(imageData)), SHA256: hex.EncodeToString(imageDigest[:]), Detail: "high", Data: imageData},
			{ID: "att-pdf", Filename: "document.pdf", ContentType: "application/pdf", Bytes: int64(len(pdfData)), SHA256: hex.EncodeToString(pdfDigest[:]), Data: pdfData},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != agentsdk.ProviderRunCompleted || result.Output["title"] != "田中太郎" || result.Model != "vision-test-model" {
		t.Fatalf("unexpected task result: %+v", result)
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
