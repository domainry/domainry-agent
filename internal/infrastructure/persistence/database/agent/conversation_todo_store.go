package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	todomodule "github.com/domainry/domainry-todo/module"
)

// Compatibility adapter only: Todo owns validation, persistence and mutations.
func (s *ConversationStore) todoStore() *todomodule.Store {
	service, err := todomodule.NewStore(s.store.Database(), s.store.Renderer(), s.store.Profile(), func(ctx context.Context, db todomodule.DB, source string, a sdk.ConversationAuthority) error {
		_, err := s.get(ctx, db, source, a)
		return err
	})
	if err != nil {
		panic(err)
	}
	return service
}
func validTodo(in sdk.ConversationTodoInput) error { return todomodule.Validate(in) }
func (s *ConversationStore) Todo(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTodo, error) {
	return s.todoStore().Todo(ctx, id, a)
}
func (s *ConversationStore) Todos(ctx context.Context, in sdk.ConversationTodoQuery, a sdk.ConversationAuthority) (sdk.ConversationTodoPage, error) {
	return s.todoStore().Todos(ctx, in, a)
}
func (s *ConversationStore) CreateTodos(ctx context.Context, in sdk.ConversationTodoCreate, a sdk.ConversationAuthority) (sdk.ConversationTodoBatch, error) {
	return s.todoStore().CreateTodos(ctx, in, a)
}
func (s *ConversationStore) UpdateTodo(ctx context.Context, id string, in sdk.ConversationTodoUpdate, a sdk.ConversationAuthority) (sdk.ConversationTodo, error) {
	return s.todoStore().UpdateTodo(ctx, id, in, a)
}
func (s *ConversationStore) DeleteTodo(ctx context.Context, id string, in sdk.ConversationTodoDelete, a sdk.ConversationAuthority) error {
	return s.todoStore().DeleteTodo(ctx, id, in, a)
}
func (s *ConversationStore) applyTodoTool(ctx context.Context, tx *sql.Tx, in sdk.ConversationToolRequest, call persistence.ConversationToolExecution) (sdk.ConversationToolResult, error) {
	result, err := s.todoStore().ApplyInTransaction(ctx, tx, todomodule.Mutation{Key: call.IdempotencyKey, Operation: call.Call.Name, Data: json.RawMessage(call.Call.Arguments), SourceConversationID: in.ConversationID, SourceRunID: in.RunID}, in.Authority)
	return sdk.ConversationToolResult{Status: "completed", Content: result.Content, ResourceID: result.ResourceID}, err
}
