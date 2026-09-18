package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// ModelTaskRunner executes durable Agent tasks directly against the configured
// conversation model. It keeps task durability in Agent while avoiding a
// second, separately configured Agent HTTP service for one-shot structured
// extraction tasks.
type ModelTaskRunner struct{ model agentsdk.ConversationModel }

func NewModelTaskRunner(model agentsdk.ConversationModel) *ModelTaskRunner {
	return &ModelTaskRunner{model: model}
}

func (r *ModelTaskRunner) Start(ctx context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	if r == nil || r.model == nil {
		return taskModelFailure("configuration", "agent.task.model_unavailable", false), fmt.Errorf("Agent task model is unavailable")
	}
	input, err := json.Marshal(request.Input)
	if err != nil {
		return taskModelFailure("request_contract", "agent.task.input_invalid", false), err
	}
	schema, err := json.Marshal(request.Task.OutputSchema)
	if err != nil {
		return taskModelFailure("request_contract", "agent.task.output_schema_invalid", false), err
	}
	system := strings.TrimSpace(request.Task.Instruction) + "\n\nReturn exactly one JSON object and no Markdown or explanation. The JSON must match this schema:\n" + string(schema)
	user := "Task input:\n" + string(input)
	blocks := []agentsdk.ConversationContentBlock{{Type: "text", Text: user}}
	for _, attachment := range request.Attachments {
		switch attachment.ContentType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			blocks = append(blocks, agentsdk.ConversationContentBlock{Type: "image", Image: &agentsdk.ConversationImageReference{
				AttachmentID: attachment.ID, Filename: attachment.Filename, ContentType: attachment.ContentType,
				Bytes: attachment.Bytes, SHA256: attachment.SHA256, Revision: 1, Detail: attachment.Detail, Data: append([]byte(nil), attachment.Data...),
			}})
		case "application/pdf":
			blocks = append(blocks, agentsdk.ConversationContentBlock{Type: "file", File: &agentsdk.ConversationFileReference{
				AttachmentID: attachment.ID, Filename: attachment.Filename, ContentType: attachment.ContentType,
				Bytes: attachment.Bytes, SHA256: attachment.SHA256, Revision: 1, Data: append([]byte(nil), attachment.Data...),
			}})
		default:
			return taskModelFailure("request_contract", "agent.task.attachment_type_unsupported", false), fmt.Errorf("unsupported Agent task attachment content type %q", attachment.ContentType)
		}
	}
	maxOutputBytes := request.Task.ExecutionLimits.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 << 10
	}
	modelRequest := agentsdk.ConversationModelRequest{
		Messages: []agentsdk.ConversationModelMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user, ContentBlocks: blocks},
		},
		Purpose: "reply", IdempotencyKey: request.IdempotencyKey, MaxOutputBytes: maxOutputBytes,
	}
	if source, ok := r.model.(agentsdk.ConversationModelCapabilitiesProvider); ok {
		modelRequest.ModelCapabilities = source.ConversationModelCapabilities()
	}
	if source, ok := r.model.(interface {
		ConversationModelIdentity() agentsdk.ConversationModelIdentity
	}); ok {
		modelRequest.ModelIdentity = source.ConversationModelIdentity()
	}
	result, err := r.model.GenerateConversation(ctx, modelRequest)
	if err != nil {
		failure := taskModelFailure("provider", "agent.task.model_failed", false)
		if details, ok := err.(agentsdk.ConversationModelFailureProvider); ok {
			modelFailure := details.ConversationModelFailureDetails()
			failure.ErrorCode, failure.Retryable, failure.Usage = modelFailure.ErrorCode, modelFailure.Retryable, modelFailure.Usage
			if strings.TrimSpace(failure.ErrorCode) == "" {
				failure.ErrorCode = "agent.task.model_failed"
			}
		}
		return failure, err
	}
	output, err := decodeTaskModelOutput(result.Content, maxOutputBytes)
	if err != nil {
		return taskModelFailure("provider_contract", "agent.task.model_output_invalid", false), err
	}
	digest := sha256.Sum256([]byte(request.WorkspaceID + "\x00" + request.TaskRunID + "\x00" + request.IdempotencyKey))
	return agentsdk.TaskResult{
		ExternalRunID: "model_task_" + hex.EncodeToString(digest[:16]), Status: agentsdk.ProviderRunCompleted,
		Outcome: "success", Output: output, RawEvidence: []byte(result.Content), Model: result.Model, Usage: result.Usage,
	}, nil
}

func (*ModelTaskRunner) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunUnknown, ErrorClass: "request_contract", ErrorCode: "agent.task.model_poll_unsupported"}, nil
}

func (*ModelTaskRunner) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunCancelled}, nil
}

func decodeTaskModelOutput(content string, maxBytes int) (map[string]any, error) {
	raw := []byte(strings.TrimSpace(content))
	if len(raw) == 0 || len(raw) > maxBytes {
		return nil, fmt.Errorf("Agent task model output is empty or exceeds %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var output map[string]any
	if err := decoder.Decode(&output); err != nil {
		return nil, fmt.Errorf("decode Agent task model output: %w", err)
	}
	if output == nil {
		return nil, fmt.Errorf("Agent task model output must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("Agent task model output must contain exactly one JSON object")
	}
	return output, nil
}

func taskModelFailure(class, code string, retryable bool) agentsdk.TaskResult {
	return agentsdk.TaskResult{Status: agentsdk.ProviderRunFailed, ErrorClass: class, ErrorCode: code, Retryable: retryable}
}

var _ agentsdk.TaskRunner = (*ModelTaskRunner)(nil)
