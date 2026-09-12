package agent

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestExecutionOutcomePersistsAcceptedAndFailedReceiptsAcrossFailureAndReplay(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "outcomes"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "three actions"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "outcome-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	calls := []agentsdk.ConversationToolCall{{ID: "accepted", Name: "create_thing", Arguments: `{}`}, {ID: "failed", Name: "create_thing", Arguments: `{}`}, {ID: "later", Name: "create_thing", Arguments: `{}`}}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{FinishReason: "tool_calls", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}}); err != nil {
		t.Fatal(err)
	}
	auth := agentsdk.ConversationToolAuthorization{Granted: true}
	for _, id := range []string{"accepted", "failed"} {
		if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, id, auth); err != nil {
			t.Fatal(err)
		}
		result := agentsdk.ConversationToolResult{Status: "completed", Completion: "accepted", ResourceID: "process-1", Content: json.RawMessage(`{"id":"process-1"}`)}
		if id == "failed" {
			result = agentsdk.ConversationToolResult{Status: "failed", ErrorCode: "invalid_input", Content: json.RawMessage(`{"error":"invalid_input"}`)}
		}
		if err = repo.FinishExecutionTool(t.Context(), claim, 0, id, result); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{}, "tool_not_authorized"); err != nil {
		t.Fatal(err)
	}
	saved, err := NewConversationStore(store).Run(t.Context(), c.ID, run.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || saved.Steps[0].Calls[0].Completion != "accepted" || saved.Steps[0].Calls[0].Effect != "write" || saved.Steps[0].Calls[1].Status != "failed" || saved.Steps[0].Calls[2].Status != "not_started" {
		t.Fatalf("outcomes lost: %+v", saved.Steps)
	}
	events, err := repo.Events(t.Context(), c.ID, run.ID, 0, 100, a)
	if err != nil {
		t.Fatal(err)
	}
	projection := agentsdk.ConversationRun{Attempt: 1}
	for _, event := range events.Items {
		if err := projectConversationExecutionEvent(&projection, event.Type, event.Data); err != nil {
			t.Fatal(err)
		}
	}
	publicSteps := append([]agentsdk.ConversationStepView(nil), saved.Steps...)
	for index := range publicSteps {
		publicSteps[index].Usage = nil
		publicSteps[index].StartedAt = nil
		publicSteps[index].CompletedAt = nil
		publicSteps[index].DurationMilliseconds = 0
		publicSteps[index].Calls = append([]agentsdk.ConversationToolView(nil), publicSteps[index].Calls...)
		for call := range publicSteps[index].Calls {
			publicSteps[index].Calls[call].Authorization = nil
			publicSteps[index].Calls[call].Confirmation = nil
			publicSteps[index].Calls[call].StartedAt = nil
			publicSteps[index].Calls[call].CompletedAt = nil
			publicSteps[index].Calls[call].DurationMilliseconds = 0
		}
	}
	if !reflect.DeepEqual(projection.Steps, publicSteps) {
		t.Fatalf("SSE differs from persisted snapshot: %+v vs %+v", projection.Steps, saved.Steps)
	}
	if _, err = repo.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	next, ok, err := repo.Claim(t.Context(), a.RuntimeID, "recovery-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	for _, id := range []string{"accepted", "failed"} {
		receipt, replayed, err := repo.BeginExecutionTool(t.Context(), next, 0, id, auth)
		if err != nil || !replayed || receipt.State != "completed" {
			t.Fatal("definite receipt must replay", id, err)
		}
		if id == "accepted" && receipt.Result.Completion != "accepted" {
			t.Fatal("business acceptance lost")
		}
	}
}

func TestExecutionOutcomeRejectsAcceptanceOnReadTools(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "read-outcome"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "read"}, a); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "read-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := executionStoreInput()
	in.Tools[0].Effect = "read"
	in.Tools[0].Idempotency = "natural"
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{FinishReason: "tool_calls", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "read", Name: "create_thing", Arguments: `{}`}}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "read", agentsdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	for _, result := range []agentsdk.ConversationToolResult{{Status: "completed", Completion: "accepted", Content: json.RawMessage(`{}`)}, {Status: "failed", Completion: "accepted"}, {Status: "completed", Completion: "invented", Content: json.RawMessage(`{}`)}} {
		requireConversationCode(t, repo.FinishExecutionTool(t.Context(), claim, 0, "read", result), "tool_result_invalid")
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "read", agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}
