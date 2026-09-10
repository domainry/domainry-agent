package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func executionStoreInput() agentsdk.ConversationStepRequest {
	return agentsdk.ConversationStepRequest{ModelIdentity: agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "chat_completions", Model: "model", Fingerprint: "frozen-model"}, IdempotencyKey: "step-0", MaxOutputBytes: 1024, MaxArgumentBytes: 1024, MaxToolCalls: 4, Messages: []agentsdk.ConversationStepMessage{{Role: "user", Content: "Create two things"}}, Tools: []agentsdk.ConversationToolDefinition{{Key: "create_thing", Version: "1", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "things.create", Effect: "write", Idempotency: "key", MaxOutputBytes: 1024, TimeoutMillis: 1000}}}
}

func TestConversationExecutionFreezesEachStepAndNeverReplaysCompletedWrites(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	ctx := t.Context()
	a := conversationTestAuthority()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "execution", Title: "Execution"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create two things"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(ctx, a.RuntimeID, "worker-one", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := executionStoreInput()
	step, found, err := repo.ExecutionStep(ctx, claim, 0, &in)
	if err != nil || !found || step.Input.IdempotencyKey != "step-0" {
		t.Fatalf("freeze %+v %v", step, err)
	}
	changed := in
	changed.IdempotencyKey = "different"
	_, _, err = repo.ExecutionStep(ctx, claim, 0, &changed)
	requireConversationCode(t, err, "step_input_conflict")
	calls := []agentsdk.ConversationToolCall{{ID: "one", Name: "create_thing", Arguments: `{"name":"one"}`}, {ID: "two", Name: "create_thing", Arguments: `{"name":"two"}`}}
	result := agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls, ProviderState: json.RawMessage(`{"reasoning_content":"private"}`)}, FinishReason: "tool_calls", Model: "served"}
	if err = repo.CompleteExecutionStep(ctx, claim, 0, result); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(ctx, claim, 0, result); err != nil {
		t.Fatal("same completion must be idempotent", err)
	}
	_, _, err = repo.ExecutionStep(ctx, claim, 1, &in)
	requireConversationCode(t, err, "previous_tools_incomplete")
	auth := agentsdk.ConversationToolAuthorization{Granted: true, Revision: "r1"}
	_, _, err = repo.BeginExecutionTool(ctx, claim, 0, "one", agentsdk.ConversationToolAuthorization{})
	requireConversationCode(t, err, "tool_not_authorized")
	_, _, err = repo.BeginExecutionTool(ctx, claim, 0, "two", auth)
	requireConversationCode(t, err, "previous_tool_incomplete")
	first, replayed, err := repo.BeginExecutionTool(ctx, claim, 0, "one", auth)
	if err != nil || replayed || first.State != "started" {
		t.Fatalf("begin %+v %v", first, err)
	}
	success := agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created-one"}`), ResourceID: "created-one"}
	if err = repo.FinishExecutionTool(ctx, claim, 0, "one", success); err != nil {
		t.Fatal(err)
	}
	// Simulate a lost response after the second external effect. No local
	// completed result exists, so recovery must reconcile, never assume failure.
	second, _, err := repo.BeginExecutionTool(ctx, claim, 0, "two", auth)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Cancel(ctx, c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(ctx, claim, 0, "two", success); err == nil {
		t.Fatal("stale worker completed cancelled call")
	}
	if _, err = repo.Resume(ctx, c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	recovered, ok, err := repo.Claim(ctx, a.RuntimeID, "worker-two", time.Minute)
	if err != nil || !ok || recovered.Fence == claim.Fence {
		t.Fatal("missing new lease", err)
	}
	// A fresh store object has no in-memory model/call state to reuse.
	repo = NewConversationStore(store)
	step, found, err = repo.ExecutionStep(ctx, recovered, 0, nil)
	if err != nil || !found || step.Result == nil || step.Input.ModelIdentity.Fingerprint != "frozen-model" {
		t.Fatal("lost step snapshot", err)
	}
	old, replayed, err := repo.BeginExecutionTool(ctx, recovered, 0, "one", auth)
	if err != nil || !replayed || old.State != "completed" || old.IdempotencyKey != first.IdempotencyKey || old.Result.ResourceID != "created-one" {
		t.Fatalf("completed write lost %+v %v", old, err)
	}
	unknown, replayed, err := repo.BeginExecutionTool(ctx, recovered, 0, "two", auth)
	if err != nil || !replayed || unknown.State != "started" || unknown.IdempotencyKey != second.IdempotencyKey {
		t.Fatalf("uncertain call lost %+v %v", unknown, err)
	}
	if err = repo.FinishExecutionTool(ctx, recovered, 0, "two", agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "upstream_timeout"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ExecutionStep(ctx, recovered, 1, &in)
	requireConversationCode(t, err, "previous_tools_incomplete")
	if err = repo.FinishExecutionTool(ctx, recovered, 0, "two", agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created-two"}`)}); err != nil {
		t.Fatal(err)
	}
	next := in
	next.IdempotencyKey = "step-1"
	if _, _, err = repo.ExecutionStep(ctx, recovered, 1, &next); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(ctx, recovered, 1, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Both created"}, FinishReason: "stop"}); err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.ExecutionStep(ctx, recovered, 2, &next)
	requireConversationCode(t, err, "previous_step_incomplete")
	other := recovered
	other.Authority.UserID = "other"
	if _, _, err = repo.ExecutionStep(ctx, other, 0, nil); err == nil {
		t.Fatal("cross-user step read")
	}
	events, err := repo.Events(ctx, c.ID, run.ID, 0, 100, a)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if json.Valid(raw) == false {
		t.Fatal("invalid events")
	}
	counts := map[string]int{}
	for _, event := range events.Items {
		counts[event.Type]++
	}
	if counts["step.started"] != 2 || counts["step.completed"] != 2 || counts["tool.started"] != 2 || counts["tool.completed"] != 2 || counts["tool.uncertain"] != 1 {
		t.Fatalf("duplicate or missing committed events: %v", counts)
	}
}

func TestConversationExecutionRejectsChangedResultsAndDeletesRecords(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	ctx := t.Context()
	a := conversationTestAuthority()
	c, _ := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "delete"}, a)
	run, _ := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "hello"}, a)
	claim, _, _ := repo.Claim(ctx, a.RuntimeID, "worker", time.Minute)
	in := executionStoreInput()
	if _, _, err := repo.ExecutionStep(ctx, claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	result := agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "hello"}, FinishReason: "stop"}
	if err := repo.CompleteExecutionStep(ctx, claim, 0, result); err != nil {
		t.Fatal(err)
	}
	result.Message.Content = "different"
	requireConversationCode(t, repo.CompleteExecutionStep(ctx, claim, 0, result), "step_result_conflict")
	if err := repo.Finish(ctx, claim, agentsdk.ConversationModelResult{Content: "hello"}, ""); err != nil {
		t.Fatal(err)
	}
	c, _ = repo.Get(ctx, c.ID, a)
	if err := repo.Delete(ctx, c.ID, c.Revision, a); err != nil {
		t.Fatal(err)
	}
	var step persistence.ConversationExecutionStep
	found, err := repo.executionRead(ctx, store.Database(), "_agent_conversation_steps", executionScope(persistence.ConversationClaim{Authority: a, Run: run}, 0), &step)
	if err != nil || found {
		t.Fatal("deleted conversation retained execution input", err)
	}
}
