package provider

import (
	"context"
	"encoding/json"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func knowledgeStep(name, arguments string) agentsdk.ConversationStepResult {
	r := taskFormatReply("", 10)
	r.FinishReason = "tool_calls"
	r.Message.ToolCalls = []agentsdk.ConversationToolCall{{ID: name, Name: name, Arguments: arguments}}
	return r
}

func TestTaskKnowledgeReadsUseCurrentHostWithoutAcquiringObjectAuthority(t *testing.T) {
	request := taskFormatRequest()
	request.AllowedTools = []string{agentsdk.AgentToolKnowledgeSearch, agentsdk.AgentToolKnowledgeRead}
	request.AllowedObjects = nil
	model := &taskFormatModel{results: []agentsdk.ConversationStepResult{
		knowledgeStep(agentsdk.AgentToolKnowledgeSearch, `{"library_id":"support","query":"Eco Mode"}`),
		knowledgeStep(agentsdk.AgentToolKnowledgeRead, `{"library_id":"support","document_id":"doc-1"}`),
		taskFormatReply(`{"summary":"source-backed"}`, 10),
	}}
	runner := NewModelTaskRunner(model)
	calls := []string{}
	runner.BindToolInvoker(func(_ context.Context, current agentsdk.TaskRequest, call agentsdk.ConversationToolCall, input map[string]any) (any, error) {
		if current.ExecutionCredential != request.ExecutionCredential || len(current.AllowedObjects) != 0 || input["library_id"] != "support" {
			t.Fatal("Knowledge tool widened Task authority")
		}
		calls = append(calls, call.Name)
		return map[string]any{"call_ref": "knowledge-receipt", "output": map[string]any{"citations": []any{map[string]any{"id": "citation-1", "document_id": "doc-1", "document_version": 3}}}}, nil
	})
	result, err := runner.Start(t.Context(), request)
	if err != nil || len(calls) != 2 || result.Output["summary"] != "source-backed" {
		t.Fatal("governed Knowledge read did not reach final result", result, calls, err)
	}
	for _, tool := range model.steps[0].Tools {
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema["additionalProperties"] != false || tool.Effect != "read" {
			t.Fatal("Knowledge tool exposed extra parameters or a write", tool)
		}
		properties := schema["properties"].(map[string]any)
		for _, forbidden := range []string{"permission_ids", "api_key", "base_url", "authority", "workspace_id", "user_id"} {
			if _, exists := properties[forbidden]; exists {
				t.Fatal("provider-controlled authority", forbidden)
			}
		}
	}
}

func TestKnowledgeRegistrationDoesNotBypassRecordOrToolAllowlist(t *testing.T) {
	for _, recordAttempt := range []bool{true, false} {
		request := taskFormatRequest()
		request.AllowedObjects = nil
		step := knowledgeStep(agentsdk.AgentToolKnowledgeSearch, `{"library_id":"support","query":"Eco"}`)
		if recordAttempt {
			request.AllowedTools = []string{agentsdk.AgentToolKnowledgeSearch, agentsdk.AgentToolGetRecord}
			step = taskFormatRead()
		}
		model := &taskFormatModel{results: []agentsdk.ConversationStepResult{step}}
		runner := NewModelTaskRunner(model)
		calls := 0
		runner.BindToolInvoker(func(context.Context, agentsdk.TaskRequest, agentsdk.ConversationToolCall, map[string]any) (any, error) {
			calls++
			return nil, nil
		})
		result, err := runner.Start(t.Context(), request)
		if err == nil || calls != 0 || result.Status == agentsdk.ProviderRunCompleted {
			t.Fatal("Knowledge widened unrelated authority", calls, result, err)
		}
	}
}
