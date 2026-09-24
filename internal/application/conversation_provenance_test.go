package application

import (
	"context"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type provenancePublicationRepository struct {
	persistence.ConversationRepository
	conversation agentsdk.Conversation
	run          agentsdk.ConversationRun
	publication  agentsdk.ConversationProvenancePublication
}

func (repository *provenancePublicationRepository) Create(_ context.Context, create agentsdk.ConversationCreate, authority agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	repository.conversation = agentsdk.Conversation{ID: "conversation-owned", RuntimeID: authority.RuntimeID, WorkspaceID: authority.WorkspaceID, UserID: authority.UserID, Title: create.Title}
	return repository.conversation, nil
}

func (repository *provenancePublicationRepository) ImportCompletedConversationRun(_ context.Context, conversationID string, publication agentsdk.ConversationProvenancePublication, _ agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	repository.publication = publication
	completedAt := time.Now().UTC()
	repository.run = agentsdk.ConversationRun{ID: "run-owned", ConversationID: conversationID, Status: "completed", CompletedAt: &completedAt}
	return repository.run, nil
}

func TestPublishConversationProvenanceReturnsOnlyAgentOwnedIdentity(t *testing.T) {
	repository := &provenancePublicationRepository{}
	service := &ConversationService{runtimeID: "agent-runtime", repo: repository}
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "agent-runtime", WorkspaceID: "workspace-a", UserID: "pm-a", RoleKey: "pm"}
	publication := agentsdk.ConversationProvenancePublication{
		ClientID: "deck-turn:1", ConversationClientID: "deck-thread:1", ConversationTitle: "Cancellation",
		UserMessage: "Allow cancellation.", AssistantMessage: "The cancellation requirement is ready.",
	}
	receipt, err := service.PublishConversationProvenance(t.Context(), publication, authority)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ConversationID != repository.conversation.ID || receipt.RunID != repository.run.ID || receipt.SourceID != "conversation://conversation-owned/turn/run-owned" || receipt.PublishedAt.IsZero() || repository.publication != publication {
		t.Fatalf("receipt=%#v publication=%#v", receipt, repository.publication)
	}
}
