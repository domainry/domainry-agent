package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type recordingTaskModel struct {
	request agentsdk.ConversationModelRequest
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
