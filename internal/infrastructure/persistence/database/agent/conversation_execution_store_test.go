package agent

import (
	"encoding/json"
	"reflect"
	"strings"
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
	in.ContextSources = []agentsdk.ConversationRunReference{{ConversationID: c.ID, RunID: run.ID, BeforeStep: 1}}
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
	wrongWorker := claim
	wrongWorker.Owner = "unrelated-worker"
	if err = repo.FinishExecutionTool(ctx, wrongWorker, 0, "two", success); err == nil {
		t.Fatal("unrelated worker completed cancelled call")
	}
	if _, err = repo.Resume(ctx, c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(ctx, claim, 0, "two", success); err == nil {
		t.Fatal("old receipt bypassed resume fence")
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
	if err != nil || !replayed || unknown.State != "uncertain" || unknown.IdempotencyKey != second.IdempotencyKey {
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
	if counts["step.started"] != 2 || counts["step.completed"] != 2 || counts["tool.started"] != 3 || counts["tool.completed"] != 2 || counts["tool.uncertain"] != 2 {
		t.Fatalf("duplicate or missing committed events: %v", counts)
	}
	rows, err := store.Database().QueryContext(ctx, `SELECT record_kind, COUNT(*) FROM _agent_run_steps GROUP BY record_kind`)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		stored[kind] = count
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if stored[conversationRunStepKindStep] != 2 || stored[conversationRunStepKindTool] != 2 || stored[conversationRunStepKindSources] != 2 {
		t.Fatalf("unified run-step kinds=%v", stored)
	}
	for _, retired := range []string{"_agent_conversation_steps", "_agent_conversation_tool_calls", "_agent_conversation_step_sources"} {
		var count int
		if err = store.Database().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired execution table %s count=%d err=%v", retired, count, err)
		}
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
	found, err := repo.executionRead(ctx, store.Database(), conversationRunStepKindStep, executionScope(persistence.ConversationClaim{Authority: a, Run: run}, 0), &step)
	if err != nil || found {
		t.Fatal("deleted conversation retained execution input", err)
	}
}

func TestConversationExecutionAllowsOnlyExplicitParallelReadsToBeginOutOfOrder(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	ctx, authority := t.Context(), conversationTestAuthority()
	conversation, _ := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "parallel-read"}, authority)
	_, _ = repo.Enqueue(ctx, conversation.ID, agentsdk.ConversationSend{ClientMessageID: "parallel-read", Message: "Read two sources"}, authority)
	claim, found, err := repo.Claim(ctx, authority.RuntimeID, "parallel-worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	input := executionStoreInput()
	input.MaxParallelTools = 2
	input.Tools = []agentsdk.ConversationToolDefinition{{Key: "read", Version: "1", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "things.read", Effect: "read", Idempotency: "natural", Parallelism: "independent_read", MaxOutputBytes: 1024, TimeoutMillis: 1000}}
	if _, _, err = repo.ExecutionStep(ctx, claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	calls := []agentsdk.ConversationToolCall{{ID: "first", Name: "read", Arguments: `{}`}, {ID: "second", Name: "read", Arguments: `{}`}, {ID: "third", Name: "read", Arguments: `{}`}}
	if err = repo.CompleteExecutionStep(ctx, claim, 0, agentsdk.ConversationStepResult{FinishReason: "tool_calls", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}}); err != nil {
		t.Fatal(err)
	}
	auth := agentsdk.ConversationToolAuthorization{Granted: true, Revision: "read-r1"}
	if second, replayed, beginErr := repo.BeginExecutionTool(ctx, claim, 0, "second", auth); beginErr != nil || replayed || second.State != "started" {
		t.Fatalf("parallel second begin=%+v replayed=%t err=%v", second, replayed, beginErr)
	}
	if first, replayed, beginErr := repo.BeginExecutionTool(ctx, claim, 0, "first", auth); beginErr != nil || replayed || first.State != "started" {
		t.Fatalf("parallel first begin=%+v replayed=%t err=%v", first, replayed, beginErr)
	}
	_, _, beginErr := repo.BeginExecutionTool(ctx, claim, 0, "third", auth)
	requireConversationCode(t, beginErr, "previous_tool_incomplete")
	for _, id := range []string{"second", "first"} {
		if err = repo.FinishExecutionTool(ctx, claim, 0, id, agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"ok":true}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if third, replayed, beginErr := repo.BeginExecutionTool(ctx, claim, 0, "third", auth); beginErr != nil || replayed || third.State != "started" {
		t.Fatalf("bounded third begin=%+v replayed=%t err=%v", third, replayed, beginErr)
	}
	if err = repo.FinishExecutionTool(ctx, claim, 0, "third", agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(ctx, claim, agentsdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	detail, err := repo.Run(ctx, conversation.ID, claim.Run.ID, authority)
	if err != nil || len(detail.Steps) != 1 || detail.Steps[0].ToolExecution != "parallel_read" || detail.Steps[0].ParallelToolCalls != 3 || detail.Steps[0].Calls[0].ID != "first" || detail.Steps[0].Calls[1].ID != "second" || detail.Steps[0].Calls[2].ID != "third" || detail.Metrics.ParallelToolBatches != 2 || detail.Metrics.ParallelToolCalls != 3 || detail.Metrics.PeakParallelTools != 2 {
		t.Fatalf("parallel projection=%+v err=%v", detail, err)
	}
}

func TestConversationExecutionProjectionClearsParallelModeOnRetry(t *testing.T) {
	run := agentsdk.ConversationRun{Attempt: 2, Steps: []agentsdk.ConversationStepView{{Number: 0, Attempt: 1, Status: "tools", ToolExecution: "parallel_read", ParallelToolCalls: 2}}}
	if err := projectConversationExecutionEvent(&run, "step.attempt.started", map[string]any{"step": 0, "attempt": 2}); err != nil {
		t.Fatal(err)
	}
	step := run.Steps[0]
	if step.ToolExecution != "" || step.ParallelToolCalls != 0 || step.Status != "generating" {
		t.Fatalf("retry retained stale parallel mode: %+v", step)
	}
	if err := projectConversationExecutionEvent(&run, "step.completed", map[string]any{"step": 0, "attempt": 2, "finish_reason": "stop", "text": "done"}); err != nil {
		t.Fatal(err)
	}
	step = run.Steps[0]
	if step.ToolExecution != "" || step.ParallelToolCalls != 0 || step.Status != "completed" {
		t.Fatalf("serial completion retained stale parallel mode: %+v", step)
	}
}

func TestConversationExecutionContextSnapshotValidationAndCacheProjection(t *testing.T) {
	now := time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC)
	sourceMessage := agentsdk.ConversationStepMessage{Role: "system", Content: "registered project instructions", ContextSourceKey: "project.rules"}
	input := executionStoreInput()
	input.Messages = []agentsdk.ConversationStepMessage{sourceMessage, {Role: "user", Content: "keep current correction"}}
	input.ContextWindow = &agentsdk.ConversationContextWindow{LimitBytes: 8192, InputBytes: 4096, PressurePermille: 500, ProviderSerialized: true}
	input.Context = &agentsdk.ConversationContextManifest{
		Version: 1, StablePrefixHash: strings.Repeat("a", 64), DynamicHash: strings.Repeat("b", 64), RefreshedAt: now,
		Sources: []agentsdk.ConversationContextSourceReference{{
			Key: "project.rules", Kind: agentsdk.ConversationContextKindProjectInstructions, Scope: agentsdk.ConversationContextScopeWorkspace,
			Refresh: agentsdk.ConversationContextRefreshRun, Trust: agentsdk.ConversationContextTrustInstruction, StablePrefix: true,
			DefinitionHash: strings.Repeat("c", 64), ContentHash: strings.Repeat("d", 64), MessageHash: conversationHash(sourceMessage.Content), MessageIndex: 0, Version: "v1", UpdatedAt: now,
		}},
	}
	if !validConversationStepContext(&input) {
		t.Fatal("valid frozen context snapshot was rejected")
	}
	tampered := input
	tampered.Messages = append([]agentsdk.ConversationStepMessage(nil), input.Messages...)
	tampered.Messages[0].Content += " forged"
	if validConversationStepContext(&tampered) {
		t.Fatal("tampered source message was accepted")
	}
	badWindow := input
	badWindow.ContextWindow = &agentsdk.ConversationContextWindow{LimitBytes: 8192, InputBytes: 4096, PressurePermille: 499}
	if validConversationStepContext(&badWindow) {
		t.Fatal("inconsistent context pressure was accepted")
	}
	badCompaction := input
	badCompaction.Compaction = &agentsdk.ConversationContextCompaction{Version: 1, Intervals: 1, BeforeBytes: 1000, AfterBytes: 1000}
	if validConversationStepContext(&badCompaction) {
		t.Fatal("non-shrinking context compaction was accepted")
	}

	view := &agentsdk.ConversationContextView{
		Window:  input.ContextWindow,
		Sources: []agentsdk.ConversationContextSourceView{{Key: "project.rules", Kind: agentsdk.ConversationContextKindProjectInstructions, Scope: agentsdk.ConversationContextScopeWorkspace, Refresh: agentsdk.ConversationContextRefreshRun, Version: "v1", StablePrefix: true, UpdatedAt: now}},
	}
	run := agentsdk.ConversationRun{Attempt: 1}
	if err := projectConversationExecutionEvent(&run, "step.started", map[string]any{"step": 0, "attempt": 1, "context": view}); err != nil {
		t.Fatal(err)
	}
	usage := map[string]any{"input_tokens": float64(500), "cache_creation_input_tokens": float64(11), "input_tokens_details": map[string]any{"cached_tokens": float64(37)}}
	if err := projectConversationExecutionEvent(&run, "step.completed", map[string]any{"step": 0, "attempt": 1, "finish_reason": "stop", "text": "done", "usage": usage}); err != nil {
		t.Fatal(err)
	}
	if len(run.Steps) != 1 || run.Steps[0].Context == nil || run.Steps[0].Context.CacheReadInputTokens != 37 || run.Steps[0].Context.CacheCreationInputTokens != 11 || !reflect.DeepEqual(run.Steps[0].Usage, usage) {
		t.Fatal("context or provider cache usage was not projected", run.Steps)
	}
	if read, creation := conversationCacheUsage(map[string]any{"prompt_tokens_details": map[string]any{"cached_tokens": json.Number("29")}}); read != 29 || creation != 0 {
		t.Fatal("chat completion cache usage was not recognized", read, creation)
	}
}
