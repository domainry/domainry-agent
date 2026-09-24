package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	todocontract "github.com/domainry/domainry-todo-sdk/contract"
)

func (s *ConversationStore) todoService() (todocontract.TodoService, error) {
	if s == nil || s.todoBinding == nil || s.todoBinding.Todos() == nil {
		return nil, fmt.Errorf("Todo module is unavailable")
	}
	return s.todoBinding.Todos(), nil
}
func (s *ConversationStore) Todo(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTodo, error) {
	service, err := s.todoService()
	if err != nil {
		return sdk.ConversationTodo{}, err
	}
	return service.Todo(ctx, id, a)
}
func (s *ConversationStore) Todos(ctx context.Context, in sdk.ConversationTodoQuery, a sdk.ConversationAuthority) (sdk.ConversationTodoPage, error) {
	service, err := s.todoService()
	if err != nil {
		return sdk.ConversationTodoPage{}, err
	}
	return service.Todos(ctx, in, a)
}
func (s *ConversationStore) CreateTodos(ctx context.Context, in sdk.ConversationTodoCreate, a sdk.ConversationAuthority) (sdk.ConversationTodoBatch, error) {
	service, err := s.todoService()
	if err != nil {
		return sdk.ConversationTodoBatch{}, err
	}
	return service.CreateTodos(ctx, in, a)
}
func (s *ConversationStore) UpdateTodo(ctx context.Context, id string, in sdk.ConversationTodoUpdate, a sdk.ConversationAuthority) (sdk.ConversationTodo, error) {
	service, err := s.todoService()
	if err != nil {
		return sdk.ConversationTodo{}, err
	}
	return service.UpdateTodo(ctx, id, in, a)
}
func (s *ConversationStore) DeleteTodo(ctx context.Context, id string, in sdk.ConversationTodoDelete, a sdk.ConversationAuthority) error {
	service, err := s.todoService()
	if err != nil {
		return err
	}
	return service.DeleteTodo(ctx, id, in, a)
}
func (s *ConversationStore) applyTodoTool(ctx context.Context, tx *sql.Tx, in sdk.ConversationToolRequest, call persistence.ConversationToolExecution) (sdk.ConversationToolResult, error) {
	if s == nil || s.todoBinding == nil || s.todoBinding.Mutations() == nil {
		return sdk.ConversationToolResult{}, fmt.Errorf("Todo module is unavailable")
	}
	result, err := s.todoBinding.Mutations().ApplyInTransaction(ctx, tx, todocontract.Mutation{Key: call.IdempotencyKey, Operation: call.Call.Name, Data: json.RawMessage(call.Call.Arguments), SourceConversationID: in.ConversationID, SourceRunID: in.RunID}, in.Authority)
	return sdk.ConversationToolResult{Status: "completed", Content: result.Content, ResourceID: result.ResourceID}, err
}
