package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func personalMutationFixture(t *testing.T, repo *ConversationStore, key, name, arguments string, scoped bool) (persistence.ConversationClaim, agentsdk.ConversationToolRequest) {
	t.Helper()
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: key}, a)
	if err != nil {
		t.Fatal(err)
	}
	send := agentsdk.ConversationSend{ClientMessageID: "send", Message: "请处理指定的个人事项"}
	if scoped {
		send.WriteScope = &agentsdk.ConversationWriteScope{PersonalMemory: !strings.HasPrefix(name, "todo_") && !strings.HasPrefix(name, "artifact_"), PersonalTodos: strings.HasPrefix(name, "todo_"), PersonalArtifacts: strings.HasPrefix(name, "artifact_")}
	}
	_, err = repo.Enqueue(t.Context(), c.ID, send, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "memory-worker", time.Minute)
	if err != nil || !found {
		t.Fatal("claim", err)
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range append(agentsdk.PersonalConversationTools(), agentsdk.ArtifactConversationTools()...) {
		if tool.Key == name {
			definition = tool
		}
	}
	input := executionStoreInput()
	input.Tools = []agentsdk.ConversationToolDefinition{definition}
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
		t.Fatal(err)
	}
	call := agentsdk.ConversationToolCall{ID: "personal-call", Name: name, Arguments: arguments}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	ledger, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, agentsdk.ConversationToolAuthorization{Granted: true})
	if err != nil {
		t.Fatal(err)
	}
	return claim, agentsdk.ConversationToolRequest{Authority: a, ConversationID: c.ID, RunID: claim.Run.ID, Step: 0, Call: call, Definition: definition, IdempotencyKey: ledger.IdempotencyKey, LeaseOwner: claim.Owner, Fence: claim.Fence}
}

func TestPersonalTodoBatchEffectReceiptAndEventAreAtomic(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	claim, request := personalMutationFixture(t, repo, "todo-atomic", "todo_create", `{"items":[{"title":"访谈","timezone":"Asia/Shanghai"},{"title":"费用","description":"必须持久保存的完整说明","timezone":"Asia/Shanghai"},{"title":"周报","timezone":"Asia/Shanghai"}]}`, true)
	// Fail after every item has been inserted. Neither a partial batch nor a
	// completed receipt may survive if the corresponding event did not commit.
	_, err := store.Database().ExecContext(t.Context(), `CREATE TRIGGER fail_todo_receipt BEFORE INSERT ON _agent_conversation_events WHEN CAST(NEW.payload_json AS TEXT) LIKE '%tool.completed%' BEGIN SELECT RAISE(ABORT, 'injected todo receipt failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyPersonalTool(t.Context(), request); err == nil {
		t.Fatal("injected failure was ignored")
	}
	page, err := repo.Todos(t.Context(), agentsdk.ConversationTodoQuery{}, request.Authority)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("partial batch committed", err)
	}
	ledger, err := repo.ExecutionTools(t.Context(), claim, 0)
	if err != nil || len(ledger) != 1 || ledger[0].State != "started" {
		t.Fatal("receipt survived rollback", err)
	}
	if _, err = store.Database().ExecContext(t.Context(), `DROP TRIGGER fail_todo_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ApplyPersonalTool(t.Context(), request)
	if err != nil || result.Status != "completed" {
		t.Fatal("create batch", result.Status, err)
	}
	repo = NewConversationStore(store)
	replayed, err := repo.ApplyPersonalTool(t.Context(), request)
	if err != nil || conversationHash(result) != conversationHash(replayed) {
		t.Fatal("durable batch receipt changed", err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, request.Call.ID, result); err != nil {
		t.Fatal(err)
	}
	page, err = repo.Todos(t.Context(), agentsdk.ConversationTodoQuery{}, request.Authority)
	if err != nil || len(page.Items) != 3 {
		t.Fatal("batch duplicated on replay", err)
	}
	if page.Items[1].Description != "必须持久保存的完整说明" || strings.Contains(string(result.Content), "必须持久保存的完整说明") {
		t.Fatal("full content must persist while model receives bounded summaries")
	}
	events, err := repo.Events(t.Context(), request.ConversationID, request.RunID, 0, 100, request.Authority)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, event := range events.Items {
		if event.Type == "tool.completed" {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completion events=%d", completed)
	}
	if err = repo.DeleteTodo(t.Context(), page.Items[0].ID, agentsdk.ConversationTodoDelete{ClientID: "delete-after-tool", ExpectedRevision: 1}, request.Authority); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyPersonalTool(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	page, err = repo.Todos(t.Context(), agentsdk.ConversationTodoQuery{}, request.Authority)
	if err != nil || len(page.Items) != 2 || page.Items[0].Position != 2 {
		t.Fatal("old batch replay resurrected deleted item", err)
	}
}

func TestPersonalMemoryEffectReceiptAndEventAreAtomic(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	claim, request := personalMutationFixture(t, repo, "atomic", "memory_save", `{"title":"周报格式","content":"按项目组织","enabled":true,"expected_revision":0}`, true)
	// SQLite fault injection at the final event write, after the memory UPDATE.
	// A failure must roll back the resource and receipt together.
	_, err := store.Database().ExecContext(t.Context(), `CREATE TRIGGER fail_personal_receipt BEFORE INSERT ON _agent_conversation_events WHEN CAST(NEW.payload_json AS TEXT) LIKE '%tool.completed%' BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyPersonalTool(t.Context(), request); err == nil {
		t.Fatal("injected transaction failure was ignored")
	}
	items, _ := repo.Memories(t.Context(), request.Authority)
	ledger, _ := repo.ExecutionTools(t.Context(), claim, 0)
	if len(items) != 0 || len(ledger) != 1 || ledger[0].State != "started" {
		t.Fatal("effect or receipt committed without event")
	}
	if _, err = store.Database().ExecContext(t.Context(), `DROP TRIGGER fail_personal_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := repo.ApplyPersonalTool(t.Context(), request)
	if err != nil || result.Status != "completed" || result.ResourceID == "" {
		t.Fatal("save", result.Status, err)
	}
	// Simulate loss of the host return and executor FinishExecutionTool call.
	repo = NewConversationStore(store)
	replayed, err := repo.ApplyPersonalTool(t.Context(), request)
	if err != nil || conversationHash(result) != conversationHash(replayed) {
		t.Fatal("lost durable receipt", err)
	}
	if err = repo.FinishExecutionTool(t.Context(), claim, 0, request.Call.ID, result); err != nil {
		t.Fatal("executor finish must be idempotent", err)
	}
	items, _ = repo.Memories(t.Context(), request.Authority)
	if len(items) != 1 || items[0].Revision != 1 {
		t.Fatal("repeated local effect")
	}
	events, _ := repo.Events(t.Context(), request.ConversationID, request.RunID, 0, 100, request.Authority)
	completed := 0
	for _, event := range events.Items {
		if event.Type == "tool.completed" {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completion events=%d", completed)
	}
	if err = repo.DeleteMemory(t.Context(), result.ResourceID, 1, request.Authority); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyPersonalTool(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	items, _ = repo.Memories(t.Context(), request.Authority)
	if len(items) != 0 {
		t.Fatal("replaying an old create resurrected a forgotten memory")
	}
}

func TestPersonalMemoryFrozenInputScopeOwnerAndCancellation(t *testing.T) {
	for _, change := range []string{"arguments", "definition", "key", "user", "fence", "cancel", "unscoped"} {
		t.Run(change, func(t *testing.T) {
			store, _ := openAgentStore(t)
			repo := NewConversationStore(store)
			_, in := personalMutationFixture(t, repo, change, "memory_save", `{"title":"偏好","content":"简洁","enabled":true,"expected_revision":0}`, change != "unscoped")
			a := in.Authority
			switch change {
			case "arguments":
				in.Call.Arguments = `{"title":"替换","content":"绕过冻结","enabled":true,"expected_revision":0}`
			case "definition":
				in.Definition.Key = "memory_forget"
			case "key":
				in.IdempotencyKey += "changed"
			case "user":
				in.Authority.UserID = "another"
			case "fence":
				in.Fence++
			case "cancel":
				if _, err := repo.Cancel(t.Context(), in.ConversationID, in.RunID, a); err != nil {
					t.Fatal(err)
				}
			case "unscoped":
				in.Confirmation = &agentsdk.ConversationConfirmation{ID: "forged", UserID: a.UserID}
			}
			if _, err := repo.ApplyPersonalTool(t.Context(), in); err == nil {
				t.Fatal("untrusted mutation was accepted")
			}
			items, _ := repo.Memories(t.Context(), a)
			if len(items) != 0 {
				t.Fatal("unauthorized memory write")
			}
		})
	}
}

func TestPersonalMemoryStrictRevisionsAndDeleteReceipts(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	m, err := repo.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "prefs:weekly.v1", Title: "周报", Content: "按项目组织", Enabled: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	for index, operation := range []struct {
		tool     string
		revision int64
		enabled  bool
		status   string
	}{
		{"memory_save", 1, false, "completed"},
		{"memory_save", 1, false, "failed"}, // Same value cannot bypass a stale revision.
		{"memory_forget", 1, false, "failed"},
		{"memory_forget", 2, false, "completed"},
	} {
		arguments := map[string]any{"id": m.ID, "expected_revision": operation.revision}
		if operation.tool == "memory_save" {
			arguments["title"], arguments["content"], arguments["enabled"] = m.Title, m.Content, operation.enabled
		}
		raw, _ := json.Marshal(arguments)
		claim, request := personalMutationFixture(t, repo, "revision-"+string(rune('a'+index)), operation.tool, string(raw), true)
		result, err := repo.ApplyPersonalTool(t.Context(), request)
		if err != nil || result.Status != operation.status {
			t.Fatalf("operation %d: %+v %v", index, result, err)
		}
		if _, err = repo.ApplyPersonalTool(t.Context(), request); err != nil {
			t.Fatal("duplicate effect", err)
		}
		if err = repo.Finish(t.Context(), claim, agentsdk.ConversationModelResult{Content: "done"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := repo.Memories(t.Context(), a)
	if len(items) != 0 {
		t.Fatal("memory not deleted")
	}
}
