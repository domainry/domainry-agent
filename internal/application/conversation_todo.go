package application

import (
	"context"
	"encoding/json"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) todoAccess(ctx context.Context, a agentsdk.ConversationAuthority, key string, input any) (persistence.ConversationTodoRepository, error) {
	if err := s.authorize(a); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationTodoRepository)
	policy := s.options.PersonalAuthorizer
	if !ok || policy == nil {
		return nil, conversationFailure("unavailable", "todos_unavailable")
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.PersonalConversationTools() {
		if tool.Key == key {
			definition = tool
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	auth, err := s.authorizeConversationTool(ctx, policy, agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: key, Arguments: string(raw)}})
	if err != nil {
		return nil, err
	}
	if !auth.Granted || auth.ConfirmationRequired {
		return nil, conversationFailure("forbidden", "tool_access_denied")
	}
	return repo, nil
}
func (s *ConversationService) Todos(ctx context.Context, in agentsdk.ConversationTodoQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoPage, error) {
	repo, err := s.todoAccess(ctx, a, "todo_list", in)
	if err != nil {
		return agentsdk.ConversationTodoPage{}, err
	}
	return repo.Todos(ctx, in, a)
}
func (s *ConversationService) Todo(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	repo, err := s.todoAccess(ctx, a, "todo_get", map[string]string{"id": id})
	if err != nil {
		return agentsdk.ConversationTodo{}, err
	}
	return repo.Todo(ctx, id, a)
}
func (s *ConversationService) CreateTodos(ctx context.Context, in agentsdk.ConversationTodoCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoBatch, error) {
	repo, err := s.todoAccess(ctx, a, "todo_create", in)
	if err != nil {
		return agentsdk.ConversationTodoBatch{}, err
	}
	return repo.CreateTodos(ctx, in, a)
}
func (s *ConversationService) UpdateTodo(ctx context.Context, id string, in agentsdk.ConversationTodoUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	repo, err := s.todoAccess(ctx, a, "todo_update", map[string]any{"id": id, "expected_revision": in.ExpectedRevision, "patch": in.Patch})
	if err != nil {
		return agentsdk.ConversationTodo{}, err
	}
	return repo.UpdateTodo(ctx, id, in, a)
}
func (s *ConversationService) DeleteTodo(ctx context.Context, id string, in agentsdk.ConversationTodoDelete, a agentsdk.ConversationAuthority) error {
	repo, err := s.todoAccess(ctx, a, "todo_delete", map[string]any{"id": id, "expected_revision": in.ExpectedRevision})
	if err != nil {
		return err
	}
	return repo.DeleteTodo(ctx, id, in, a)
}

var _ agentsdk.ConversationTodoService = (*ConversationService)(nil)
