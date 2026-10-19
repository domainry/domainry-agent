package application

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestExecutionLinksAreLeadingPrivateSystemMetadata(t *testing.T) {
	history := []agentsdk.ConversationMessage{
		{ID: "user-1", ConversationID: "conversation-1", Seq: 1, Role: "user", Content: "first"},
		{ID: "assistant-1", ConversationID: "conversation-1", RunID: "run-1", Seq: 2, Role: "assistant", Content: "first answer"},
		{ID: "user-2", ConversationID: "conversation-1", Seq: 3, Role: "user", Content: "second"},
		{ID: "assistant-2", ConversationID: "conversation-1", RunID: "run-2", Seq: 4, Role: "assistant", Content: "second answer"},
		{ID: "user-3", ConversationID: "conversation-1", Seq: 5, Role: "user", Content: "current request"},
	}
	messages := appendConversationHistoryMessages([]agentsdk.ConversationModelMessage{{Role: "system", Content: "stable instructions"}}, history, true)
	if len(messages) != len(history)+2 || messages[1].Role != "system" || !strings.HasPrefix(messages[1].Content, "Recent execution links") {
		t.Fatalf("execution locators were not kept in one leading system message: %#v", messages)
	}
	if strings.Index(messages[1].Content, `"run_id":"run-2"`) > strings.Index(messages[1].Content, `"run_id":"run-1"`) {
		t.Fatal("execution locators are not newest first")
	}
	for _, message := range messages[2:] {
		if strings.Contains(message.Content, "Recent execution links") {
			t.Fatal("private execution metadata leaked into public conversation roles")
		}
	}
	if messages[len(messages)-1].Role != "user" || messages[len(messages)-1].Content != "current request" {
		t.Fatal("current user request is no longer the last conversation message")
	}
}

func TestExecutionLinksAreOmittedWithoutHistoricalAssistantRun(t *testing.T) {
	history := []agentsdk.ConversationMessage{{ID: "user-1", ConversationID: "conversation-1", Seq: 1, Role: "user", Content: "current request"}}
	messages := appendConversationHistoryMessages(nil, history, true)
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("empty execution metadata was injected: %#v", messages)
	}
}
