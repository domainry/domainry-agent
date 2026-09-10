package provider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Opt-in only: this test sends three real requests and may consume provider
// quota. It never prints credentials, prompts or raw provider responses.
func TestGatewayLiveConversation(t *testing.T) {
	if os.Getenv("AGENT_CONVERSATION_LIVE") != "1" {
		t.Skip("set AGENT_CONVERSATION_LIVE=1 and configure AGENT_PROVIDER_API_KEY for real model acceptance")
	}
	config := ConversationModelConfigFromEnvironment()
	if config.Provider == "" {
		config.Provider = ConversationProviderGateway
		config.APIKey = os.Getenv("AGENT_PROVIDER_API_KEY")
	}
	if config.Provider != ConversationProviderGateway {
		t.Fatal("live acceptance requires the Gateway provider")
	}
	if config.Model == "" {
		config.Model = "glm-5.3-flash-free"
	}
	config.MaxOutputTokens = 512
	model, err := NewConversationModel(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	first := agentsdk.ConversationModelRequest{Purpose: "reply", MaxOutputBytes: 4096, Messages: []agentsdk.ConversationModelMessage{{Role: "system", Content: "Follow the user's instructions concisely."}, {Role: "user", Content: "The project code is AMBER-742. Reply with that exact code only."}}}
	stream := func(input agentsdk.ConversationModelRequest) agentsdk.ConversationModelResult {
		var text strings.Builder
		chunks := 0
		result, err := model.StreamConversation(ctx, input, func(delta string) error { text.WriteString(delta); chunks++; return nil })
		if err != nil {
			t.Fatalf("live stream failed: %v", err)
		}
		if chunks < 1 || result.Content != text.String() || !strings.Contains(result.Content, "AMBER-742") {
			t.Fatal("live stream did not preserve the conversation fact")
		}
		return result
	}
	reply := stream(first)
	second := first
	second.Messages = append(append([]agentsdk.ConversationModelMessage(nil), first.Messages...), agentsdk.ConversationModelMessage{Role: "assistant", Content: reply.Content}, agentsdk.ConversationModelMessage{Role: "user", Content: "What was the project code? Reply with the exact code only."})
	stream(second)
	summary, err := model.GenerateConversation(ctx, agentsdk.ConversationModelRequest{Purpose: "summary", MaxOutputBytes: 4096, Messages: []agentsdk.ConversationModelMessage{{Role: "system", Content: "Return only a JSON object with goal, constraints, facts, decisions, open_items. goal is a string and all other fields are arrays of strings. No Markdown."}, {Role: "user", Content: "Summarize: The project code is AMBER-742. Preserve the exact code in facts."}}})
	if err != nil {
		t.Fatalf("live summary failed: %v", err)
	}
	var decoded struct {
		Goal                          string `json:"goal"`
		Constraints, Facts, Decisions []string
		OpenItems                     []string `json:"open_items"`
	}
	if json.Unmarshal([]byte(summary.Content), &decoded) != nil || !strings.Contains(strings.Join(decoded.Facts, " "), "AMBER-742") {
		t.Fatal("live summary did not return structured facts")
	}
	t.Logf("Gateway live acceptance passed: protocol=%s, model=%s; streaming, supplied history and structured summary", model.config.Protocol, model.config.Model)
}
