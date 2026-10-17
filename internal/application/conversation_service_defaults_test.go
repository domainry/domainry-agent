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
	if conversationHistoryContextLimit(tools.ContextBytes) != 64*1024 {
		t.Fatalf("tool history bytes = %d", conversationHistoryContextLimit(tools.ContextBytes))
	}
	explicit := ConversationOptions{ToolHost: &executionIntervalHost{}, ContextBytes: 96 * 1024}
	applyConversationContextDefault(&explicit)
	if explicit.ContextBytes != 96*1024 {
		t.Fatalf("explicit context bytes = %d", explicit.ContextBytes)
	}
	if conversationHistoryContextLimit(32*1024) != 32*1024 {
		t.Fatalf("history budget exceeded the total context limit")
	}
	if !conversationHistoryFits(64*1024*7/10, 20, 64*1024, true) || conversationHistoryFits(64*1024*7/10+1, 20, 64*1024, true) {
		t.Fatal("tool history compaction threshold is not 70 percent")
	}
	if !conversationHistoryFits(64*1024, 9, 64*1024, false) || conversationHistoryFits(64*1024, 10, 64*1024, false) {
		t.Fatal("plain short-history exception is not bounded to nine messages")
	}
}
