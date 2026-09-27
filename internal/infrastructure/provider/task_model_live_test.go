package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// This opt-in check exercises the real configured gateway without writing its
// credential or synthetic source text to the repository or test output.
func TestLiveGLMTaskReadsSourceBeforeReturningJSON(t *testing.T) {
	if os.Getenv("AGENT_LIVE_MODEL_E2E") != "1" {
		t.Skip("set AGENT_LIVE_MODEL_E2E=1 with a private AGENT_TASK_MODEL_API_KEY")
	}
	key := os.Getenv("AGENT_TASK_MODEL_API_KEY")
	if key == "" {
		t.Fatal("private task model credential is missing")
	}
	model, err := NewConversationModel(ConversationModelConfig{Provider: ConversationProviderGateway, Protocol: ConversationProtocolChat, BaseURL: "https://api.verdent.ai", APIKey: key, Model: "glm-5.3-flash"})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewModelTaskRunner(model)
	calls := 0
	runner.BindToolInvoker(func(_ context.Context, _ agentsdk.TaskRequest, call agentsdk.ConversationToolCall, input map[string]any) (any, error) {
		if call.Name != agentsdk.AgentToolGetRecord || input["object_key"] != "meeting" || input["record_id"] != "meeting-fixture" {
			t.Fatalf("live model requested an unexpected source: %s", call.Name)
		}
		calls++
		return map[string]any{"id": "meeting-fixture", "transcript_content": "客户：请明天下午发送报价单。", "transcript_content_hash": "synthetic-hash"}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	result, err := runner.Start(ctx, agentsdk.TaskRequest{
		TaskRunID: "live-synthetic-task", WorkspaceID: "live-synthetic-workspace", IdempotencyKey: "live-synthetic-once", ExecutionCredential: "synthetic-task-credential",
		Task:  agentsdk.AgentTaskDefinition{Instruction: "先调用 get_record，参数 object_key=meeting、record_id=input.meeting_id。只依据返回的 transcript_content 输出 JSON，source_hash 原样返回 input.source_hash，summary 简短概括客户要求。", OutputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"source_hash": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"}}, "required": []string{"source_hash", "summary"}}, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 3, MaxToolCalls: 1, MaxInputBytes: 4096, MaxOutputBytes: 8192}},
		Input: map[string]any{"meeting_id": "meeting-fixture", "source_hash": "synthetic-hash"}, AllowedTools: []string{agentsdk.AgentToolGetRecord}, AllowedObjects: []string{"meeting"},
	})
	summary, _ := result.Output["summary"].(string)
	if err != nil || result.Status != agentsdk.ProviderRunCompleted || calls != 1 || result.Output["source_hash"] != "synthetic-hash" || !strings.Contains(summary, "报价") {
		t.Fatalf("live GLM task failed: status=%s code=%s calls=%d err=%v", result.Status, result.ErrorCode, calls, err)
	}
}
