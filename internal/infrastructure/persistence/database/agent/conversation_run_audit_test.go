package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationRunDetailProjectsBoundedAuditMetricsUsageAndDurations(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	authority := conversationTestAuthority()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "run-audit"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "audit-message", Message: "call one tool"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	claim, found, err := repo.Claim(t.Context(), authority.RuntimeID, "audit-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("claim found=%t err=%v", found, err)
	}
	input := executionStoreInput()
	input.Tools[0].ActionKey = "things.create"
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.AppendEvent(t.Context(), claim, "step.attempt.started", map[string]any{"step": 0, "attempt": claim.Run.Attempt}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	call := agentsdk.ConversationToolCall{ID: "call-audit", Name: "create_thing", Arguments: `{"secret_argument":"must-not-enter-audit"}`}
	stepResult := agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "fixture-model", Usage: map[string]any{"input_tokens": 7, "output_tokens": 3}, Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call}}}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, stepResult); err != nil {
		t.Fatal(err)
	}
	if err = repo.AppendEvent(t.Context(), claim, "authorization.checked", map[string]any{"step": 0, "attempt": claim.Run.Attempt, "call_id": call.ID, "tool": call.Name, "action_key": "things.create", "status": "granted", "revision": "auth-r7"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, agentsdk.ConversationToolAuthorization{Granted: true, Revision: "auth-r7"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"secret_result":"must-not-enter-audit"}`)}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Model: "fixture-model", Usage: map[string]any{"input_tokens": float64(7), "output_tokens": float64(3)}}, "model_stopped_after_tool"); err != nil {
		t.Fatal(err)
	}

	detail, err := NewConversationStore(store).Run(t.Context(), conversation.ID, run.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	if detail.CorrelationID != run.ID || detail.StartedAt == nil || detail.CompletedAt == nil || detail.DurationMilliseconds < 1 || detail.QueueDurationMilliseconds < 1 || !detail.AuditComplete {
		t.Fatalf("timing/correlation detail=%+v", detail)
	}
	if detail.Metrics.Steps != 1 || detail.Metrics.ModelCalls != 1 || detail.Metrics.ToolCalls != 1 || detail.Metrics.ToolAttempts != 1 || detail.Metrics.AuthorizationChecks != 1 {
		t.Fatalf("metrics=%+v", detail.Metrics)
	}
	if detail.Steps[0].Usage["input_tokens"] == nil || detail.Steps[0].StartedAt == nil || detail.Steps[0].CompletedAt == nil || detail.Steps[0].Calls[0].StartedAt == nil || detail.Steps[0].Calls[0].CompletedAt == nil {
		t.Fatalf("step audit=%+v", detail.Steps[0])
	}
	authorization := detail.Steps[0].Calls[0].Authorization
	if authorization == nil || authorization.Status != "granted" || authorization.Revision != "auth-r7" || authorization.Checks != 1 {
		t.Fatalf("authorization=%+v", authorization)
	}
	raw, _ := json.Marshal(detail.Audit)
	if strings.Contains(string(raw), "secret_argument") || strings.Contains(string(raw), "secret_result") {
		t.Fatalf("audit leaked tool payload: %s", raw)
	}
	foundAuthorization, foundFailure := false, false
	for _, event := range detail.Audit {
		foundAuthorization = foundAuthorization || event.Type == "authorization" && event.Status == "granted" && event.AuthorizationRevision == "auth-r7"
		foundFailure = foundFailure || event.Type == "run" && event.Status == "failed" && event.ErrorCode == "model_stopped_after_tool"
	}
	if !foundAuthorization || !foundFailure {
		t.Fatalf("audit=%+v", detail.Audit)
	}
}
