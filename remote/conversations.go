package remote

import (
	"context"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type conversationClient struct {
	client    *client
	runtimeID string
}

func (c *conversationClient) call(ctx context.Context, op string, in agentsdk.ConversationRPCRequest, out any) error {
	if in.Authority.RuntimeID != c.runtimeID {
		return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.runtime_denied"}
	}
	return c.client.call(ctx, http.MethodPost, "/agent/v1/conversations/"+op, in, "", out)
}
func (c *conversationClient) Create(ctx context.Context, in agentsdk.ConversationCreate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var out agentsdk.Conversation
	err := c.call(ctx, "create", agentsdk.ConversationRPCRequest{Authority: a, Create: in}, &out)
	return out, err
}
func (c *conversationClient) List(ctx context.Context, in agentsdk.ConversationQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationPage, error) {
	var out agentsdk.ConversationPage
	err := c.call(ctx, "list", agentsdk.ConversationRPCRequest{Authority: a, Query: in}, &out)
	return out, err
}
func (c *conversationClient) Get(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var out agentsdk.Conversation
	err := c.call(ctx, "get", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id}, &out)
	return out, err
}
func (c *conversationClient) Update(ctx context.Context, id string, in agentsdk.ConversationUpdate, a agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	var out agentsdk.Conversation
	err := c.call(ctx, "update", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, Update: in}, &out)
	return out, err
}
func (c *conversationClient) Send(ctx context.Context, id string, in agentsdk.ConversationSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := c.call(ctx, "send", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, Send: in}, &out)
	return out, err
}
func (c *conversationClient) Messages(ctx context.Context, id string, in agentsdk.ConversationMessageQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationMessagePage, error) {
	var out agentsdk.ConversationMessagePage
	err := c.call(ctx, "messages", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, Messages: in}, &out)
	return out, err
}
func (c *conversationClient) Run(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := c.call(ctx, "run", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, RunID: run}, &out)
	return out, err
}
func (c *conversationClient) Events(ctx context.Context, id, run string, after int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationEventPage, error) {
	var out agentsdk.ConversationEventPage
	err := c.call(ctx, "events", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, RunID: run, AfterSeq: after, Limit: limit}, &out)
	return out, err
}
func (c *conversationClient) Cancel(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := c.call(ctx, "cancel", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, RunID: run}, &out)
	return out, err
}
func (c *conversationClient) Resume(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := c.call(ctx, "resume", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, RunID: run}, &out)
	return out, err
}
func (c *conversationClient) Memories(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationMemory, error) {
	var out []agentsdk.ConversationMemory
	err := c.call(ctx, "memories_list", agentsdk.ConversationRPCRequest{Authority: a}, &out)
	return out, err
}
func (c *conversationClient) WriteMemory(ctx context.Context, in agentsdk.ConversationMemoryWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationMemory, error) {
	var out agentsdk.ConversationMemory
	err := c.call(ctx, "memories_write", agentsdk.ConversationRPCRequest{Authority: a, Memory: in}, &out)
	return out, err
}
func (c *conversationClient) Delete(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	var out map[string]bool
	return c.call(ctx, "delete", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, Revision: revision}, &out)
}
func (c *conversationClient) DeleteMemory(ctx context.Context, id string, revision int64, a agentsdk.ConversationAuthority) error {
	var out map[string]bool
	return c.call(ctx, "memories_delete", agentsdk.ConversationRPCRequest{Authority: a, MemoryID: id, Revision: revision}, &out)
}

var _ agentsdk.ConversationService = (*conversationClient)(nil)

func (c *conversationClient) Respond(ctx context.Context, id, run string, response agentsdk.ConversationInteractionResponse, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	err := c.call(ctx, "respond", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: id, RunID: run, Response: response}, &out)
	return out, err
}

var _ agentsdk.ConversationInteractionService = (*conversationClient)(nil)

func (c *conversationClient) Todos(ctx context.Context, in agentsdk.ConversationTodoQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoPage, error) {
	var out agentsdk.ConversationTodoPage
	err := c.call(ctx, "todos_list", agentsdk.ConversationRPCRequest{Authority: a, TodoQuery: in}, &out)
	return out, err
}
func (c *conversationClient) Todo(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	var out agentsdk.ConversationTodo
	err := c.call(ctx, "todos_get", agentsdk.ConversationRPCRequest{Authority: a, TodoID: id}, &out)
	return out, err
}
func (c *conversationClient) CreateTodos(ctx context.Context, in agentsdk.ConversationTodoCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodoBatch, error) {
	var out agentsdk.ConversationTodoBatch
	err := c.call(ctx, "todos_create", agentsdk.ConversationRPCRequest{Authority: a, TodoCreate: in}, &out)
	return out, err
}
func (c *conversationClient) UpdateTodo(ctx context.Context, id string, in agentsdk.ConversationTodoUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTodo, error) {
	var out agentsdk.ConversationTodo
	err := c.call(ctx, "todos_update", agentsdk.ConversationRPCRequest{Authority: a, TodoID: id, TodoUpdate: in}, &out)
	return out, err
}
func (c *conversationClient) DeleteTodo(ctx context.Context, id string, in agentsdk.ConversationTodoDelete, a agentsdk.ConversationAuthority) error {
	var out map[string]bool
	return c.call(ctx, "todos_delete", agentsdk.ConversationRPCRequest{Authority: a, TodoID: id, TodoDelete: in}, &out)
}

var _ agentsdk.ConversationTodoService = (*conversationClient)(nil)
