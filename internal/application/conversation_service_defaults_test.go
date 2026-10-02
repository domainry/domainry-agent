package application

import "testing"

func TestDefaultConversationContextBudgetSeparatesChatAndToolExecution(t *testing.T) {
	plain := ConversationOptions{}
	applyConversationContextDefault(&plain)
	if plain.ContextBytes != 64*1024 {
		t.Fatalf("plain context bytes = %d", plain.ContextBytes)
	}
	tools := ConversationOptions{ToolHost: &executionIntervalHost{}}
	applyConversationContextDefault(&tools)
	if tools.ContextBytes != 8*1024*1024 {
		t.Fatalf("tool context bytes = %d", tools.ContextBytes)
	}
	explicit := ConversationOptions{ToolHost: &executionIntervalHost{}, ContextBytes: 96 * 1024}
	applyConversationContextDefault(&explicit)
	if explicit.ContextBytes != 96*1024 {
		t.Fatalf("explicit context bytes = %d", explicit.ContextBytes)
	}
}
