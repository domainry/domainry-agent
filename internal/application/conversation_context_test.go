package application

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestShortImageHistoryDoesNotTriggerTextCompaction(t *testing.T) {
	messages := []agentsdk.ConversationModelMessage{
		{Role: "user", Content: "Analyze the business card", ContentBlocks: []agentsdk.ConversationContentBlock{{Type: "image", Image: &agentsdk.ConversationImageReference{AttachmentID: "card", Bytes: 2_500_000}}}},
		{Role: "assistant", Content: "Company name and contact information are visible."},
		{Role: "user", Content: "Which fields are missing?"},
	}
	textSize := conversationHistoryContextSize(messages)
	transportSize := conversationContextSize(messages)
	if !conversationHistoryFits(textSize, len(messages), 64*1024, true) {
		t.Fatalf("short text plus an image reference triggered text compaction: %d", textSize)
	}
	if transportSize <= 3_000_000 || transportSize >= 8*1024*1024 {
		t.Fatalf("image transfer was not independently charged to the full request: %d", transportSize)
	}
	messages[1].Content = strings.Repeat("long historical text", 4096)
	if conversationHistoryFits(conversationHistoryContextSize(messages), len(messages), 64*1024, true) {
		t.Fatal("genuinely long text history no longer triggers compaction")
	}
}

func TestConversationSummaryFenceCompatibilityKeepsStrictValidation(t *testing.T) {
	valid := `{"goal":"ship","constraints":[],"facts":["CEDAR-928"],"decisions":[],"open_items":["invoice"]}`
	for _, text := range []string{valid, "```json\n" + valid + "\n```", "```\r\n" + valid + "\r\n```"} {
		got, err := decodeConversationSummary(text)
		if err != nil || got.Goal != "ship" || got.Facts[0] != "CEDAR-928" {
			t.Fatalf("valid summary rejected: %v", err)
		}
	}
	for _, text := range []string{"Here is the summary:\n" + valid, "```json\n" + valid, "```python\n" + valid + "\n```", "```json\n" + valid + "\n```\nextra", valid + valid, "```json\n" + valid + "\n```\n```json\n" + valid + "\n```", `{"goal":"ship","tools":["execute"]}`, `{"goal":123}`, "{}", "null"} {
		if _, err := decodeConversationSummary(text); err == nil {
			t.Fatal("invalid or ambiguous summary accepted")
		}
	}
}
