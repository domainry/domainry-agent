package integration_test

import (
	"context"
	"errors"
	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	"testing"
)

func TestGenericConversationRunsWithoutKnowledgeFactory(t *testing.T) {
	repo := conversationRepository(t)
	authority := conversationAuthority()
	model := conversationModelFunc(func(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
		return sdk.ConversationModelResult{Content: "Generic Agent completed", Model: "test"}, nil
	})
	options := conversationOptions()
	// Bypass product composition deliberately: the Agent core has no Knowledge binding.
	service, err := application.NewConversationService(repo, model, authority.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), sdk.ConversationCreate{ClientID: "generic-only"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, sdk.ConversationSend{ClientMessageID: "generic-message", Message: "Complete a generic task"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitConversation(t, service, conversation.ID, run.ID); got.Status != "completed" {
		t.Fatalf("generic Agent requires Knowledge: %+v", got)
	}
	_, err = service.KnowledgeLibraries(t.Context(), "", 10, authority)
	var failure *sdk.Error
	if !errors.As(err, &failure) || failure.Code != "agent.conversation.knowledge_not_configured" {
		t.Fatalf("unconfigured Knowledge did not fail closed: %v", err)
	}
}
