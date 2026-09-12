package agent

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestCancellationRetainsReceiptsAndFencesAllOtherWrites(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "cancellation-receipt"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "three", Message: "three actions"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "original-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	var calls []agentsdk.ConversationToolCall
	for _, id := range []string{"done", "in-flight", "untouched"} {
		calls = append(calls, agentsdk.ConversationToolCall{ID: id, Name: "create_thing", Arguments: `{"name":"item"}`})
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	auth := agentsdk.ConversationToolAuthorization{Granted: true}
	first := agentsdk.ConversationToolResult{Status: "completed", ResourceID: "saved-first", Content: json.RawMessage(`{"id":"saved-first"}`)}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "done", auth); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "done", first); err != nil {
		t.Fatal(err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "in-flight", auth); err != nil {
		t.Fatal(err)
	}
	stopped, err := repo.Cancel(t.Context(), c.ID, run.ID, a)
	if err != nil || stopped.Status != "cancelled" {
		t.Fatal(stopped, err)
	}
	if stopped.Steps[0].Calls[0].Status != "completed" || stopped.Steps[0].Calls[1].Status != "uncertain" {
		t.Fatal("cancellation erased outcomes", stopped.Steps)
	}
	repeated, err := repo.Cancel(t.Context(), c.ID, run.ID, a)
	if err != nil || repeated.LastEventSeq != stopped.LastEventSeq {
		t.Fatal("duplicate cancellation changed events", err)
	}
	if ok, err := repo.Heartbeat(t.Context(), claim, time.Minute); err != nil || ok {
		t.Fatal("cancelled lease renewed", err)
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), claim, 0, "untouched", auth); err == nil {
		t.Fatal("cancelled worker started next action")
	}
	if err = repo.AppendDelta(t.Context(), claim, 0, "not a final answer"); err == nil {
		t.Fatal("cancelled draft appended")
	}
	if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "not completed"}, ""); err == nil {
		t.Fatal("cancelled run promoted to completed")
	}
	// Internal transactional effects must not gain the public receipt exception.
	if err = repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.finishExecutionTool(t.Context(), tx, claim, 0, "in-flight", first) }); err == nil {
		t.Fatal("local effect accepted cancelled lease")
	}
	second := agentsdk.ConversationToolResult{Status: "completed", ResourceID: "saved-second", Content: json.RawMessage(`{"id":"saved-second"}`)}
	for _, invalid := range []string{"owner", "fence", "user", "workspace"} {
		bad := claim
		switch invalid {
		case "owner":
			bad.Owner = "other"
		case "fence":
			bad.Fence++
		case "user":
			bad.Authority.UserID = "other"
		case "workspace":
			bad.Authority.WorkspaceID = "other"
		}
		if err = repo.FinishExecutionTool(t.Context(), bad, 0, "in-flight", second); err == nil {
			t.Fatalf("invalid %s receipt accepted", invalid)
		}
	}
	// Reopening repository state does not lose the cancellation/receipt guard.
	repo = NewConversationStore(store)
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "in-flight", second); err != nil {
		t.Fatal("late definitive receipt lost", err)
	}
	settled, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || settled.Status != "cancelled" || settled.Steps[0].Calls[1].ResourceID != second.ResourceID {
		t.Fatal("receipt changed run semantics", settled, err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "in-flight", second); err != nil {
		t.Fatal(err)
	}
	duplicate, _ := repo.Run(t.Context(), c.ID, run.ID, a)
	if duplicate.LastEventSeq != settled.LastEventSeq {
		t.Fatal("receipt repeated its event")
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "in-flight", first); err == nil {
		t.Fatal("conflicting receipt replaced actual result")
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "untouched", second); err == nil {
		t.Fatal("receipt invented unstarted effect")
	}
	page, err := repo.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Role != "user" {
		t.Fatal("cancelled reply entered history", page, err)
	}
	if _, err = repo.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, "in-flight", second); err == nil {
		t.Fatal("old receipt crossed resume fence")
	}
	recovered, ok, err := repo.Claim(t.Context(), a.RuntimeID, "new-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	for _, id := range []string{"done", "in-flight"} {
		call, replay, err := repo.BeginExecutionTool(t.Context(), recovered, 0, id, auth)
		if err != nil || !replay || call.State != "completed" {
			t.Fatalf("lost durable receipt %s: %+v %v", id, call, err)
		}
	}
	if _, _, err = repo.BeginExecutionTool(t.Context(), recovered, 0, "untouched", auth); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Cancel(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	c, err = repo.Get(t.Context(), c.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(t.Context(), c.ID, c.Revision, a); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(t.Context(), recovered, 0, "untouched", second); err == nil {
		t.Fatal("deleted execution accepted late receipt")
	}
}
