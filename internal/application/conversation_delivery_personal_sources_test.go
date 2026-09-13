package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-tools/timeutil"
)

type personalReceiptRepository struct {
	privatePeerSources
	persistence.ConversationExecutionReadRepository
	persistence.ConversationTodoRepository
	a                   sdk.ConversationAuthority
	record              persistence.ConversationToolExecution
	memories            []sdk.ConversationMemory
	todos               map[string]sdk.ConversationTodo
	missingConversation bool
	reads               int
}

func (r *personalReceiptRepository) Get(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.Conversation, error) {
	if a != r.a || id != "producer" || r.missingConversation {
		return sdk.Conversation{}, conversationFailure("not_found", "conversation_not_found")
	}
	return sdk.Conversation{ID: id, UserID: a.UserID, WorkspaceID: a.WorkspaceID, RuntimeID: a.RuntimeID}, nil
}
func (r *personalReceiptRepository) ReadExecutionCall(_ context.Context, conversation, run string, step int, call string, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	r.reads++
	if a != r.a || conversation != "producer" || run != "run" || step != r.record.Step || call != r.record.Call.ID {
		return persistence.ConversationToolExecution{}, invalidPersonalReceipt()
	}
	return r.record, nil
}
func (r *personalReceiptRepository) Memories(_ context.Context, a sdk.ConversationAuthority) ([]sdk.ConversationMemory, error) {
	if a != r.a {
		return nil, invalidPersonalReceipt()
	}
	return r.memories, nil
}
func (r *personalReceiptRepository) Todo(_ context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTodo, error) {
	item, ok := r.todos[id]
	if a != r.a || !ok {
		return sdk.ConversationTodo{}, conversationFailure("not_found", "todo_not_found")
	}
	return item, nil
}

func TestPersonalDeliveryReceiptsUseOriginalOwnerAndCurrentDataWithoutWriteTools(t *testing.T) {
	now := time.Date(2026, 9, 13, 1, 2, 3, 0, time.UTC)
	mem := sdk.ConversationMemory{ID: "memory", Title: "Preference", Content: "Use exact source dates", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	todo := sdk.ConversationTodo{ID: "todo", Title: "Verify source dates", Description: "Read the originals", Timezone: "Asia/Shanghai", Status: "open", Revision: 1, BatchID: "batch", Position: 1, SourceConversationID: "producer", SourceRunID: "run", CreatedAt: now, UpdatedAt: now}
	calc, _ := calculateConversation(calculationInput{Operation: "expression", Expression: "0.1 + 0.2"})
	clock, _ := timeutil.Resolve(timeutil.Input{Relative: "tomorrow"}, now, time.UTC, "Asia/Shanghai")
	for _, tc := range []struct {
		key, args, resource string
		value               any
	}{
		{"calculate", `{"operation":"expression","expression":"0.1 + 0.2"}`, "", calc},
		{"time_now", `{"relative_date":"tomorrow"}`, "", clock},
		{"ask_user", `{"question":"Which quarter?"}`, "", map[string]string{"answer": "Second quarter", "interaction_id": "answered"}},
		{"memory_search", `{}`, "", map[string]any{"items": []sdk.ConversationMemory{mem}, "complete": true, "next_cursor": ""}},
		{"memory_save", `{"title":"Preference","content":"Use exact source dates","enabled":true,"expected_revision":0}`, mem.ID, map[string]any{"memory": mem}},
		{"memory_forget", `{"id":"memory","expected_revision":1}`, mem.ID, map[string]any{"id": mem.ID, "deleted": true}},
		{"todo_get", `{"id":"todo"}`, "", todo},
		{"todo_list", `{}`, "", sdk.ConversationTodoPage{Items: []sdk.ConversationTodo{todo}, Complete: true}},
		{"todo_create", `{"items":[{"title":"Verify source dates","description":"Read the originals","timezone":"Asia/Shanghai"}]}`, "batch", sdk.ConversationTodoBatch{BatchID: "batch", Items: []sdk.ConversationTodo{todo}}},
		{"todo_update", `{"id":"todo","expected_revision":1,"patch":{"title":"Verify source dates"}}`, todo.ID, todo},
		{"todo_delete", `{"id":"todo","expected_revision":1}`, todo.ID, map[string]any{"id": todo.ID, "deleted": true}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "reader"}
			definition, _ := personalResultReadDefinition(tc.key)
			raw, _ := json.Marshal(tc.value)
			record := persistence.ConversationToolExecution{State: "completed", Definition: definition, Call: sdk.ConversationToolCall{ID: "call", Name: tc.key, Arguments: tc.args}, Result: &sdk.ConversationToolResult{Status: "completed", ResourceID: tc.resource, Content: raw}, IdempotencyKey: "original", CreatedAt: now, UpdatedAt: now}
			repo := &personalReceiptRepository{a: a, record: record, memories: []sdk.ConversationMemory{mem}, todos: map[string]sdk.ConversationTodo{todo.ID: todo}}
			policy := &deliveryArtifactPolicy{denied: map[string]bool{}, disabled: map[string]bool{}}
			for _, d := range sdk.PersonalConversationTools() {
				policy.denied[d.Key] = d.Key != "todo_get"
			}
			executor := &deliveryReadTestHost{}
			s := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{ToolHost: &profileToolHost{base: executor, allowed: map[string]bool{}}, PersonalAuthorizer: policy, ToolAvailability: policy, CollaborationAuthorizer: fixedCollaborationTestPolicy{"view", "delivery_read"}}}
			owner := sdk.ConversationRunReference{ConversationID: "producer", RunID: "run"}
			base := context.WithValue(t.Context(), conversationAgentContextKey{}, &sdk.ConversationAgentSnapshot{})
			ctx := deliverySourceContext(base, "released")
			if _, err := s.sourceAudit(a).record(base, owner, record); err == nil || repo.reads != 0 {
				t.Fatal("raw result used delivery policy", err)
			}
			checks := executor.executionChecks
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err != nil || repo.reads != 1 || executor.executionChecks != checks {
				t.Fatal("receipt required producing execution", err)
			}
			for _, request := range policy.requests {
				if request.Definition.Key != "todo_get" {
					t.Fatal("unexpected execution authorization", request.Definition.Key)
				}
			}
			policy.disabled[tc.key] = true
			if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
				t.Fatal("disabled result readable")
			}
			delete(policy.disabled, tc.key)
			for _, change := range []string{"foreign-user", "foreign-workspace", "missing-conversation", "changed-arguments", "changed-result", "extra-content", "changed-definition", "changed-key"} {
				bad := record
				p := a
				copyResult := *record.Result
				bad.Result = &copyResult
				switch change {
				case "foreign-user":
					p.UserID = "foreign"
				case "foreign-workspace":
					p.WorkspaceID = "foreign"
				case "missing-conversation":
					repo.missingConversation = true
				case "changed-arguments":
					bad.Call.Arguments = `{}`
					if bad.Call.Arguments == record.Call.Arguments {
						bad.Call.Arguments = `{"include_disabled":true}`
					}
				case "changed-result":
					copyResult.Content = json.RawMessage(`{"arbitrary":"data"}`)
				case "extra-content":
					copyResult.Content = json.RawMessage(strings.TrimSuffix(string(raw), "}") + `,"extra":"unverified"}`)
				case "changed-definition":
					bad.Definition.Version = "changed"
				case "changed-key":
					bad.IdempotencyKey = "other"
				}
				if _, err := s.sourceAudit(p).record(ctx, owner, bad); err == nil {
					t.Fatal("unverified personal receipt accepted", change)
				}
				repo.missingConversation = false
			}
			if strings.HasPrefix(tc.key, "todo_") {
				policy.denied["todo_get"] = true
				if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
					t.Fatal("todo data permission revoke ignored")
				}
				policy.denied["todo_get"] = false
				if tc.key != "todo_delete" {
					delete(repo.todos, todo.ID)
					if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
						t.Fatal("deleted source todo readable")
					}
					repo.todos[todo.ID] = todo
					changed := todo
					changed.Status = "completed"
					changed.Revision++
					repo.todos[todo.ID] = changed
					if _, err := s.sourceAudit(a).record(ctx, owner, record); err != nil {
						t.Fatal("historical todo state was replaced by current state", err)
					}
				}
			}
			if tc.key == "memory_save" || tc.key == "memory_search" {
				repo.memories = nil
				if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
					t.Fatal("forgotten memory readable")
				}
				changed := mem
				changed.Enabled = false
				changed.Revision++
				repo.memories = []sdk.ConversationMemory{changed}
				if _, err := s.sourceAudit(a).record(ctx, owner, record); err == nil {
					t.Fatal("old memory survived correction/disable")
				}
			}
		})
	}
}
