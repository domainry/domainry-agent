package integration_test

import (
	"context"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
)

type faultyExecutionModel struct {
	executionModel
	failure string
}

func (m *faultyExecutionModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	switch m.failure {
	case "text_limit":
		_ = emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: strings.Repeat("x", in.MaxOutputBytes+1)})
	case "unknown_event":
		_ = emit(agentsdk.ConversationModelEvent{Type: "private_reasoning", Delta: "private"})
	case "wrong_offset":
		_ = emit(agentsdk.ConversationModelEvent{Type: "text.delta", Offset: 7, Delta: "changed"})
	case "arguments_changed":
		_ = emit(agentsdk.ConversationModelEvent{Type: "tool.started", CallID: "call-one", Name: "create_item"})
		_ = emit(agentsdk.ConversationModelEvent{Type: "tool.arguments.delta", CallID: "call-one", Name: "create_item", Delta: `{"title":"preview"}`})
	}
	// An injected model that ignores a callback error must still be unable to
	// publish a valid-looking final call or cause an effect.
	return m.callResult(`{"title":"final"}`), nil
}

func TestConversationExecutionRejectsFaultyStreamingModelsBeforeToolEffects(t *testing.T) {
	for _, failure := range []string{"text_limit", "unknown_event", "wrong_offset", "arguments_changed"} {
		t.Run(failure, func(t *testing.T) {
			repo := conversationRepository(t)
			host := &executionHost{allowed: true}
			model := &faultyExecutionModel{failure: failure}
			service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			a := conversationAuthority()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: failure}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create one"}, a)
			if err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			host.mu.Lock()
			invokes := host.invokes
			host.mu.Unlock()
			if final.Status != "failed" || invokes != 0 {
				t.Fatalf("invalid stream had an effect: %+v %d", final, invokes)
			}
		})
	}
}
