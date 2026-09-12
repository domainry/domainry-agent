package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

// The independent SaaS RPC must carry all todo identifiers, filters, source
// references and mutation receipts, without requiring a chat model or run.
func TestPersonalTodoSaaSRoundTripAndScope(t *testing.T) {
	a := conversationAuthority()
	service, err := conversationassembly.NewService(conversationRepository(t), nil, a.RuntimeID, application.ConversationOptions{PersonalAuthorizer: personalReadAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "todo-saas-test-secret", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "todo-saas-test-secret", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	conversation := binding.(agentsdk.ConversationBinding).Conversations()
	todos, ok := conversation.(agentsdk.ConversationTodoService)
	if !ok {
		t.Fatal("SaaS client lost todo extension")
	}
	source, err := conversation.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "todo-source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	input := agentsdk.ConversationTodoCreate{ClientID: "saas-create", SourceConversationID: source.ID, Items: []agentsdk.ConversationTodoInput{
		{Title: "核对费用", Description: "远程接口保留完整说明", Timezone: "Asia/Shanghai", DueDate: "2026-09-11"},
		{Title: "提交周报", Timezone: "Asia/Shanghai"},
	}}
	batch, err := todos.CreateTodos(t.Context(), input, a)
	if err != nil || len(batch.Items) != 2 {
		t.Fatal("create round trip", err)
	}
	result, _ := json.Marshal(batch)
	replayed, err := todos.CreateTodos(t.Context(), input, a)
	replay, _ := json.Marshal(replayed)
	if err != nil || string(result) != string(replay) {
		t.Fatal("SaaS create receipt changed", err)
	}
	first, err := todos.Todo(t.Context(), batch.Items[0].ID, a)
	if err != nil || first.Description != input.Items[0].Description || first.SourceConversationID != source.ID {
		t.Fatal("get omitted fields", err)
	}
	query := agentsdk.ConversationTodoQuery{BatchID: batch.BatchID, SourceConversationID: source.ID, Status: "open", Limit: 1}
	page, err := todos.Todos(t.Context(), query, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Position != 1 || page.Complete || page.NextCursor == "" {
		t.Fatal("list/filter/cursor lost", err)
	}
	query.Cursor = page.NextCursor
	page, err = todos.Todos(t.Context(), query, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Position != 2 || !page.Complete {
		t.Fatal("SaaS cursor continuation failed", err)
	}
	status := "completed"
	change := agentsdk.ConversationTodoUpdate{ClientID: "saas-complete", ExpectedRevision: 1, Patch: agentsdk.ConversationTodoPatch{Status: &status}}
	updated, err := todos.UpdateTodo(t.Context(), first.ID, change, a)
	if err != nil || updated.Revision != 2 || updated.CompletedAt == nil {
		t.Fatal("update round trip", err)
	}
	updated, err = todos.UpdateTodo(t.Context(), first.ID, change, a)
	if err != nil || updated.Revision != 2 {
		t.Fatal("SaaS update receipt changed", err)
	}
	for _, scope := range []string{"user", "workspace", "runtime"} {
		other := a
		switch scope {
		case "user":
			other.UserID = "another-user"
		case "workspace":
			other.WorkspaceID = "another-workspace"
		case "runtime":
			other.RuntimeID = "another-runtime"
		}
		_, err := todos.Todo(t.Context(), first.ID, other)
		var coded *agentsdk.Error
		if !errors.As(err, &coded) || scope == "runtime" && coded.Class != "forbidden" || scope != "runtime" && coded.Class != "not_found" {
			t.Fatalf("%s scope not enforced: %v", scope, err)
		}
	}
	remove := agentsdk.ConversationTodoDelete{ClientID: "saas-delete", ExpectedRevision: 2}
	if err = todos.DeleteTodo(t.Context(), first.ID, remove, a); err != nil {
		t.Fatal(err)
	}
	if err = todos.DeleteTodo(t.Context(), first.ID, remove, a); err != nil {
		t.Fatal("delete replay", err)
	}
	page, err = todos.Todos(t.Context(), agentsdk.ConversationTodoQuery{BatchID: batch.BatchID}, a)
	if err != nil || len(page.Items) != 1 || page.Items[0].Position != 2 {
		t.Fatal("delete did not preserve original remaining item", err)
	}
}
