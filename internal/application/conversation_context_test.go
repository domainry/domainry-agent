package application

import "testing"

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
