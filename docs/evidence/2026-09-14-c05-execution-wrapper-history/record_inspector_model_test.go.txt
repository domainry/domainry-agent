package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	agent "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

type recordInspectorInput struct {
	ID        string                         `json:"id"`
	Reference agent.ConversationRunReference `json:"reference"`
}

type recordInspectorModel struct{}

func (recordInspectorModel) ConversationModelIdentity() agent.ConversationModelIdentity {
	return agent.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "record-inspector", Fingerprint: "record-inspector-v1"}
}
func (recordInspectorModel) GenerateConversation(context.Context, agent.ConversationModelRequest) (agent.ConversationModelResult, error) {
	return agent.ConversationModelResult{}, fmt.Errorf("record inspection needs actual tool results")
}
func (m recordInspectorModel) ConversationStepInputBytes(in agent.ConversationStepRequest) (int, error) {
	if in.ModelIdentity != m.ConversationModelIdentity() {
		return 0, fmt.Errorf("record inspector identity changed")
	}
	return provider.ConversationStepInputBytes(provider.ConversationModelConfig{Provider: in.ModelIdentity.Provider, Protocol: in.ModelIdentity.Protocol, Model: in.ModelIdentity.Model}, in)
}
func (recordInspectorModel) StreamConversationStep(_ context.Context, in agent.ConversationStepRequest, _ func(agent.ConversationModelEvent) error) (agent.ConversationStepResult, error) {
	tool := func(id, key string, args any) (agent.ConversationStepResult, error) {
		raw, err := json.Marshal(args)
		return agent.ConversationStepResult{FinishReason: "tool_calls", Message: agent.ConversationStepMessage{Role: "assistant", ToolCalls: []agent.ConversationToolCall{{ID: id, Name: key, Arguments: string(raw)}}}}, err
	}
	var input recordInspectorInput
	outputs := map[string]json.RawMessage{}
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Inspect shared record execution:\n") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(message.Content, "Inspect shared record execution:\n")), &input); err != nil {
				return agent.ConversationStepResult{}, err
			}
		}
		if message.Role == "tool" {
			var result agent.ConversationToolResult
			if json.Unmarshal([]byte(message.Content), &result) != nil || result.Status != "completed" || result.ErrorCode != "" {
				return agent.ConversationStepResult{}, fmt.Errorf("actual shared record inspection failed: %s", message.ToolCallID)
			}
			outputs[message.ToolCallID] = result.Content
		}
	}
	if input.ID == "" || input.Reference.RunID == "" {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original record execution reference missing")
	}
	if _, ok := outputs["inspection-agreement"]; !ok {
		return tool("inspection-agreement", "delegation_get", map[string]string{"id": input.ID})
	}
	var agreement agent.ConversationDelegationDetail
	if json.Unmarshal(outputs["inspection-agreement"], &agreement) != nil || agreement.ID != input.ID || agreement.Delivery == nil {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original record agreement missing")
	}
	if _, ok := outputs["inspection-publish"]; !ok {
		return tool("inspection-publish", "delegation_execution_publish", map[string]any{"id": input.ID, "publication": map[string]any{"reference": input.Reference, "expected_revision": agreement.Revision, "reason": "Actual execution owner confirms publishing the original structured record run"}})
	}
	var publication agent.ConversationExecutionPublication
	if json.Unmarshal(outputs["inspection-publish"], &publication) != nil || publication.Reference != input.Reference || publication.Withdrawn {
		return agent.ConversationStepResult{}, fmt.Errorf("actual original execution publication changed")
	}
	if _, ok := outputs["inspection-index"]; !ok {
		return tool("inspection-index", "delegation_executions", map[string]string{"id": input.ID})
	}
	var index agent.ConversationDelegationExecutionIndex
	if json.Unmarshal(outputs["inspection-index"], &index) != nil || index.DelegationID != input.ID || len(index.Publications) != 1 || index.Publications[0].Reference != input.Reference {
		return agent.ConversationStepResult{}, fmt.Errorf("actual shared execution index changed")
	}
	if _, ok := outputs["inspection-read"]; !ok {
		return tool("inspection-read", "delegation_execution_read", agent.ConversationDelegationExecutionRead{ID: input.ID, Reference: index.Publications[0].Reference})
	}
	var view agent.ConversationDelegationExecutionView
	if json.Unmarshal(outputs["inspection-read"], &view) != nil || view.Run.ID != input.Reference.RunID || len(view.Run.Steps) != 6 || view.Run.Interaction != nil || view.Run.WriteScope != nil {
		return agent.ConversationStepResult{}, fmt.Errorf("actual six-step execution inspection changed")
	}
	var original agent.ConversationResultReference
	for _, step := range view.Run.Steps {
		for _, call := range step.Calls {
			if call.Name == "requirements_save" && call.ResultReference != nil {
				original = *call.ResultReference
			}
		}
	}
	if original.RunID != input.Reference.RunID || original.CallID == "" {
		return agent.ConversationStepResult{}, fmt.Errorf("actual successful original save reference missing")
	}
	var full strings.Builder
	offset := 0
	for page := 0; page < 16; page++ {
		id := "inspection-page-" + strconv.Itoa(page)
		if _, ok := outputs[id]; !ok {
			return tool(id, "delegation_execution_result_read", agent.ConversationDelegationExecutionResultRead{ID: input.ID, Read: agent.ConversationResultRead{Reference: original, Offset: offset, MaxBytes: 256}})
		}
		var slice agent.ConversationDelegationExecutionResult
		if json.Unmarshal(outputs[id], &slice) != nil || slice.DelegationID != input.ID || slice.Result.Reference != original || slice.Result.Offset != offset || slice.Result.NextOffset <= offset {
			return agent.ConversationStepResult{}, fmt.Errorf("actual original structured result slice changed")
		}
		full.WriteString(slice.Result.JSONText)
		offset = slice.Result.NextOffset
		if slice.Result.Complete {
			sum := sha256.Sum256([]byte(full.String()))
			if hex.EncodeToString(sum[:]) != original.SHA256 || !strings.Contains(full.String(), `"amount":9007199254740993`) {
				return agent.ConversationStepResult{}, fmt.Errorf("actual original result SHA or integer changed")
			}
			return agent.ConversationStepResult{FinishReason: "stop", Message: agent.ConversationStepMessage{Role: "assistant", Content: "Verified the six-step original execution and the complete saved result using actual shared execution tools."}}, nil
		}
	}
	return agent.ConversationStepResult{}, fmt.Errorf("actual original result paging did not complete")
}
