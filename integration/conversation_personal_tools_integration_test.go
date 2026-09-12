package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
)

type personalReadAuthorizer struct{}

func (personalReadAuthorizer) AuthorizeConversationExecution(_ context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	return in.Authority.Known, nil
}

func (personalReadAuthorizer) AuthorizeConversationTool(_ context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: in.Authority.Known, Revision: "read-test"}, nil
}

func TestPersonalHostWithoutResponsePolicyDoesNotAdvertiseQuestions(t *testing.T) {
	host, err := application.NewPersonalConversationHost(conversationRepository(t), personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := host.ConversationTools(t.Context(), conversationAuthority())
	if err != nil || len(tools) != 9 {
		t.Fatalf("read-only catalog: %d %v", len(tools), err)
	}
	for _, tool := range tools {
		if tool.Key == "ask_user" || tool.Effect == "write" {
			t.Fatal("unanswerable question or write advertised")
		}
	}
	for _, definition := range agentsdk.PersonalConversationTools() {
		if definition.Key != "ask_user" {
			continue
		}
		auth, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: conversationAuthority(), Definition: definition})
		if err != nil || auth.Granted {
			t.Fatal("question authorized without response policy", err)
		}
	}
}

func TestTodoLookupExplainsFilteredEmptyResultsWithoutBroadeningAccess(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	service, err := conversationassembly.NewService(repo, nil, a.RuntimeID, application.ConversationOptions{PersonalAuthorizer: personalReadAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	_, err = service.CreateTodos(t.Context(), agentsdk.ConversationTodoCreate{ClientID: "lookup", Items: []agentsdk.ConversationTodoInput{{Title: "核对部门费用", Timezone: "Asia/Shanghai"}}}, a)
	if err != nil {
		t.Fatal(err)
	}
	other := a
	other.UserID = "other-owner"
	_, err = service.CreateTodos(t.Context(), agentsdk.ConversationTodoCreate{ClientID: "lookup-other", Items: []agentsdk.ConversationTodoInput{{Title: "青禾私有事项", Timezone: "Asia/Shanghai"}}}, other)
	if err != nil {
		t.Fatal(err)
	}
	host, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.PersonalConversationTools() {
		if tool.Key == "todo_list" {
			definition = tool
		}
	}
	call := func(arguments string) agentsdk.ConversationToolResult {
		t.Helper()
		result, err := host.InvokeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: definition.Key, Arguments: arguments}})
		if err != nil || result.Status != "completed" {
			t.Fatal("lookup failed", err)
		}
		return result
	}
	filtered := call(`{"query":"青禾","scope":"all"}`)
	var page struct {
		agentsdk.ConversationTodoPage
		Lookup struct {
			Query, Scope, Note string
			MatchFields        []string `json:"match_fields"`
		} `json:"lookup"`
	}
	if json.Unmarshal(filtered.Content, &page) != nil || !page.Complete || len(page.Items) != 0 || page.Lookup.Query != "青禾" || page.Lookup.Scope != "all" || len(page.Lookup.MatchFields) != 2 || !strings.Contains(page.Lookup.Note, "Project and conversation names") {
		t.Fatalf("missing exact lookup semantics: %s", filtered.Content)
	}
	if strings.Contains(string(filtered.Content), "私有事项") {
		t.Fatal("lookup hint leaked another owner's data")
	}
	var unfiltered agentsdk.ConversationTodoPage
	if json.Unmarshal(call(`{}`).Content, &unfiltered) != nil || len(unfiltered.Items) != 1 || unfiltered.Items[0].Title != "核对部门费用" {
		t.Fatal("unfiltered owned lookup changed")
	}
}

func TestPersonalMemoryToolPagesEscapedResultsWithoutLosingScopeOrSnapshot(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	for i := 0; i < 32; i++ {
		_, err := repo.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: fmt.Sprintf("memory-%02d", i), Title: "Saved preference", Content: strings.Repeat("\n", 500) + "quoted", Enabled: true}, a)
		if err != nil {
			t.Fatal(err)
		}
	}
	host, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.PersonalConversationTools() {
		if tool.Key == "memory_search" {
			definition = tool
		}
	}
	seen := map[string]bool{}
	cursor := ""
	firstCursor := ""
	for page := 0; page < 10; page++ {
		raw, _ := json.Marshal(map[string]string{"cursor": cursor})
		out, err := host.InvokeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: "memory_search", Arguments: string(raw)}})
		if err != nil || out.Status != "completed" || len(out.Content) > definition.MaxOutputBytes {
			t.Fatalf("memory page failed %s %v", out.Status, err)
		}
		var result struct {
			Items      []agentsdk.ConversationMemory `json:"items"`
			Complete   bool                          `json:"complete"`
			NextCursor string                        `json:"next_cursor"`
		}
		if err = json.Unmarshal(out.Content, &result); err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatal("duplicate memory")
			}
			seen[item.ID] = true
		}
		if result.Complete {
			break
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			t.Fatal("pagination made no progress")
		}
		cursor = result.NextCursor
		if firstCursor == "" {
			firstCursor = cursor
		}
	}
	if len(seen) != 32 || firstCursor == "" {
		t.Fatalf("read %d memories, missing bounded pagination", len(seen))
	}
	for _, change := range []string{"user", "snapshot"} {
		who := a
		if change == "user" {
			who.UserID = "other"
		} else {
			if err = repo.DeleteMemory(t.Context(), "memory-00", 1, a); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := json.Marshal(map[string]string{"cursor": firstCursor})
		out, err := host.InvokeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: who, Definition: definition, Call: agentsdk.ConversationToolCall{Name: "memory_search", Arguments: string(raw)}})
		if err != nil || out.ErrorCode != "memory_cursor_invalid" {
			t.Fatalf("stale/foreign cursor accepted %s %v", out.ErrorCode, err)
		}
	}
}
