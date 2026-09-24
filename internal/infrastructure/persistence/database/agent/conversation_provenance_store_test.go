package agent

import (
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestCompletedConversationProvenanceImportIsAtomicAndIdempotent(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "pm", RoleKey: "pm"}
	conversation, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "deck-thread:1", Title: "Cancellation"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	publication := agentsdk.ConversationProvenancePublication{
		ClientID: "deck-turn:1", ConversationClientID: "deck-thread:1", ConversationTitle: "Cancellation",
		UserMessage: "Allow cancellation.", AssistantMessage: "The requirement is ready.",
	}
	first, err := repository.ImportCompletedConversationRun(t.Context(), conversation.ID, publication, authority)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repository.ImportCompletedConversationRun(t.Context(), conversation.ID, publication, authority)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || replayed.ID != first.ID || first.Status != "completed" || first.CompletedAt == nil || first.AssistantMessageID == "" {
		t.Fatalf("first=%#v replayed=%#v", first, replayed)
	}
	messages, err := repository.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{Limit: 10}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages.Items) != 2 || messages.Items[0].RunID != first.ID || messages.Items[1].RunID != first.ID || messages.Items[0].Role != "user" || messages.Items[1].Role != "assistant" {
		t.Fatalf("messages=%#v", messages.Items)
	}

	changed := publication
	changed.AssistantMessage = "Different result."
	_, err = repository.ImportCompletedConversationRun(t.Context(), conversation.ID, changed, authority)
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.idempotency_conflict" {
		t.Fatalf("changed publication error=%#v", err)
	}
}
