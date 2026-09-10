package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestExecutionReadCursorChangesAndCancelledEvidence(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "read-cursor"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "work", Message: "处理两个事项"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	input := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	calls := []agentsdk.ConversationToolCall{{ID: "a", Name: "create_thing", Arguments: `{"name":"a"}`}, {ID: "b", Name: "create_thing", Arguments: `{"name":"b"}`}}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "a", agentsdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "a", agentsdk.ConversationToolResult{Status: "completed", ResourceID: "actual-resource", Content: json.RawMessage(`{"saved":true}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "b", agentsdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	args := agentsdk.ConversationExecutionRead{ConversationID: c.ID, RunID: run.ID, Limit: 1}
	page, err := repo.ReadExecutionCalls(t.Context(), args, a)
	if err != nil || page.Complete || page.NextCursor == "" {
		t.Fatal("no continuation", err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "b", agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown", Content: json.RawMessage(`{"unknown":true}`)}); err != nil {
		t.Fatal(err)
	}
	args.Cursor = page.NextCursor
	_, err = repo.ReadExecutionCalls(t.Context(), args, a)
	requireConversationCode(t, err, "execution_cursor_changed")
	if _, err = repo.Cancel(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	args.Cursor = ""
	args.Limit = 5
	page, err = repo.ReadExecutionCalls(t.Context(), args, a)
	if err != nil || !page.Complete || page.RunStatus != "cancelled" || len(page.Items) != 2 {
		t.Fatal("cancelled ledger lost", err)
	}
	states := map[string]string{}
	for _, item := range page.Items {
		states[item.Call.ID] = item.State
	}
	if states["a"] != "completed" || states["b"] != "uncertain" {
		t.Fatal("cancellation fabricated outcomes", states)
	}
	c, err = repo.Get(t.Context(), c.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(t.Context(), c.ID, c.Revision, a); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReadExecutionCalls(t.Context(), args, a); err == nil {
		t.Fatal("deleted run readable")
	}
	if _, err = repo.ReadExecutionCall(t.Context(), c.ID, run.ID, 0, "a", a); err == nil {
		t.Fatal("deleted source call readable")
	}
}
