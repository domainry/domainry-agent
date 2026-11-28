package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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
	if err != nil || len(tools) != 8 {
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

func TestPersonalMemorySummaryIsSourceBoundAndHidesSuppressedByDefault(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	now := time.Now().UTC()
	for _, memory := range []agentsdk.ConversationMemoryWrite{
		{ID: "visible", Kind: agentsdk.ConversationMemoryKindUserPreference, Title: "Style", Content: "Lead with the conclusion", Enabled: true, Status: agentsdk.AgentMemoryStatusActive, Importance: 0.9},
		{ID: "suppressed", Kind: agentsdk.ConversationMemoryKindUserPreference, Title: "Hidden", Content: "Do not surface", Enabled: true, Status: agentsdk.AgentMemoryStatusActive, DoNotMention: true, Importance: 1},
	} {
		memory.Scope = agentsdk.ConversationMemoryScope{Kind: agentsdk.ConversationMemoryScopeWorkspace}
		memory.LastVerifiedAt = &now
		if _, err := repo.WriteMemory(t.Context(), memory, a); err != nil {
			t.Fatal(err)
		}
	}
	host, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var definition agentsdk.ConversationToolDefinition
	for _, tool := range agentsdk.PersonalConversationTools() {
		if tool.Key == "memory_summary" {
			definition = tool
		}
	}
	invoke := func(arguments string) agentsdk.AgentMemorySummary {
		out, invokeErr := host.InvokeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition, Call: agentsdk.ConversationToolCall{Name: "memory_summary", Arguments: arguments}})
		if invokeErr != nil || out.Status != "completed" {
			t.Fatalf("summary failed: %+v %v", out, invokeErr)
		}
		var result struct {
			Summary agentsdk.AgentMemorySummary `json:"summary"`
		}
		if unmarshalErr := json.Unmarshal(out.Content, &result); unmarshalErr != nil {
			t.Fatal(unmarshalErr)
		}
		return result.Summary
	}
	visible := invoke(`{}`)
	if visible.ItemCount != 1 || len(visible.Groups) != 1 || visible.Groups[0].Items[0].MemoryID != "visible" || visible.Groups[0].Items[0].Source.SourceRevision != "1" || visible.Revision == "" {
		t.Fatalf("default summary=%+v", visible)
	}
	managed := invoke(`{"include_suppressed":true}`)
	if managed.ItemCount != 2 || managed.Revision == visible.Revision {
		t.Fatalf("management summary=%+v", managed)
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
