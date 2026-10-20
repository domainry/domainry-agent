package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

func TestExecutionHistoryBatchPreservesCallsAndChecksCurrentLease(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-batch", Title: "History"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "history", Message: "Create"}, a); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "history-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	in := executionStoreInput()
	in.Messages = append(in.Messages, agentsdk.ConversationStepMessage{Role: "system", Content: strings.Repeat("unused-input-transcript", 4096)})
	if _, found, err := repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil || !found {
		t.Fatal(err)
	}
	call := agentsdk.ConversationToolCall{ID: "one", Name: "create_thing", Arguments: `{"name":"one"}`}
	if err := repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{call}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.BeginExecutionTool(t.Context(), claim, 0, call.ID, agentsdk.ConversationToolAuthorization{Granted: true, Revision: "revision"}); err != nil {
		t.Fatal(err)
	}
	result := agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created"}`)}
	if err := repo.FinishExecutionTool(t.Context(), claim, 0, call.ID, result); err != nil {
		t.Fatal(err)
	}
	steps, tools, err := repo.ExecutionHistory(t.Context(), claim, 1)
	if err != nil || len(steps) != 1 || len(tools) != 1 || steps[0].Result == nil || steps[0].Result.Message.ToolCalls[0].ID != call.ID || tools[0].State != "completed" || tools[0].Result == nil || string(tools[0].Result.Content) != string(result.Content) {
		t.Fatalf("batch lost evidence: steps=%+v tools=%+v err=%v", steps, tools, err)
	}
	if len(steps[0].Input.Messages) != 0 {
		t.Fatal("history unnecessarily decoded the growing input transcript")
	}
	statement, args, err := repo.executionHistoryStatement(claim, 1, conversationRunStepTable)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.Database().QueryContext(t.Context(), statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var number int
		var raw []byte
		if err := rows.Scan(&number, &raw); err != nil {
			t.Fatal(err)
		}
		if len(raw) > 2048 || strings.Contains(string(raw), "unused-input-transcript") || strings.Contains(string(raw), `"input"`) {
			t.Fatalf("unused frozen input crossed the database boundary: %d bytes", len(raw))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	lookup, lookupArgs, err := query.NewSelectBuilder(store.Renderer(), conversationToolCallTable).Columns("payload_json").Where(query.And(conversationScope(a, c.ID), query.Equal("run_id", claim.Run.ID), query.Equal("step_no", 0))).Build()
	if err != nil {
		t.Fatal(err)
	}
	var savedTool []byte
	if err := store.Database().QueryRowContext(t.Context(), lookup, lookupArgs...).Scan(&savedTool); err != nil {
		t.Fatal("history projection changed the opaque tool receipt bytes", err)
	}
	encoded, err := marshalDurableJSON(tools[0])
	if err != nil || string(encoded) != string(savedTool) {
		t.Fatal("opaque receipt changed on history read", err)
	}
	empty, emptyTools, err := repo.ExecutionHistory(t.Context(), claim, 0)
	if err != nil || len(empty) != 0 || len(emptyTools) != 0 {
		t.Fatal("history ignored upper bound", err)
	}
	foreign := claim
	foreign.Authority.UserID = "another-user"
	if _, _, err := repo.ExecutionHistory(t.Context(), foreign, 1); err == nil {
		t.Fatal("history crossed owner")
	}
	stale := claim
	stale.Fence++
	if _, _, err := repo.ExecutionHistory(t.Context(), stale, 1); err == nil {
		t.Fatal("history accepted stale worker lease")
	}
}

func TestExecutionHistoryReadsResultsWithoutJSONProjectionForAllDialects(t *testing.T) {
	store, _ := openAgentStore(t)
	for _, name := range []ormdialect.Name{ormdialect.SQLite, ormdialect.MySQL, ormdialect.Postgres} {
		t.Run(string(name), func(t *testing.T) {
			renderer, _ := ormdialect.ParseRenderer(string(name), "", "")
			other, err := NewStore(store.Database(), renderer, store.Profile(), "history-sql")
			if err != nil {
				t.Fatal(err)
			}
			repo := NewConversationStore(other)
			for _, table := range []string{conversationRunStepTable, conversationToolCallTable} {
				statement, args, err := repo.executionHistoryStatement(persistence.ConversationClaim{Authority: conversationTestAuthority(), Run: agentsdk.ConversationRun{ID: "run", ConversationID: "conversation"}}, 3, table)
				if err != nil || len(args) != 4 || strings.Contains(statement, "JSON") || strings.Contains(statement, "CASE") || strings.Contains(statement, conversationStepInputTable) || strings.Contains(statement, conversationToolCatalogTable) {
					t.Fatal("history still parses/joins frozen requests", statement, args, err)
				}
			}
		})
	}
}
