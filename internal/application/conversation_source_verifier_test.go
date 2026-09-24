package application

import (
	"context"
	"errors"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

type sourceVerifierRepository struct {
	persistence.ConversationRepository
	conversation agentsdk.Conversation
	run          agentsdk.ConversationRun
}

func (repository sourceVerifierRepository) Get(_ context.Context, id string, _ agentsdk.ConversationAuthority) (agentsdk.Conversation, error) {
	if id != repository.conversation.ID {
		return agentsdk.Conversation{}, conversationFailure("not_found", "conversation_not_found")
	}
	return repository.conversation, nil
}

func (repository sourceVerifierRepository) Run(_ context.Context, conversationID, runID string, _ agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	if conversationID != repository.run.ConversationID || runID != repository.run.ID {
		return agentsdk.ConversationRun{}, conversationFailure("not_found", "run_not_found")
	}
	return repository.run, nil
}

func TestVerifyConversationSourcesRequiresEverySourceToBelongToAnOwnedRun(t *testing.T) {
	authority := agentsdk.ConversationAuthority{Known: true, RuntimeID: "agent", WorkspaceID: "workspace-a", UserID: "reader-a"}
	reference := agentsdk.ConversationRunReference{ConversationID: "conversation-a", RunID: "run-a", BeforeStep: 2}
	completedAt := time.Now().UTC()
	service := &ConversationService{repo: sourceVerifierRepository{
		conversation: agentsdk.Conversation{ID: reference.ConversationID, WorkspaceID: authority.WorkspaceID},
		run:          agentsdk.ConversationRun{ID: reference.RunID, ConversationID: reference.ConversationID, Status: "completed", CompletedAt: &completedAt, Steps: []agentsdk.ConversationStepView{{}}},
	}}
	receipt, err := service.VerifyConversationSources(t.Context(), agentsdk.ConversationSourceVerificationRequest{
		References: []agentsdk.ConversationRunReference{reference},
		SourceIDs:  []string{"conversation://conversation-a/turn/run-a"},
		Reader:     authority,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.SourceIDs) != 1 || receipt.SourceIDs[0] != "conversation://conversation-a/turn/run-a" {
		t.Fatalf("receipt=%#v", receipt)
	}

	_, err = service.VerifyConversationSources(t.Context(), agentsdk.ConversationSourceVerificationRequest{
		References: []agentsdk.ConversationRunReference{reference},
		SourceIDs:  []string{"conversation://conversation-a/turn/run-other"},
		Reader:     authority,
	})
	var coded *agentsdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.source_run_mismatch" {
		t.Fatalf("mismatched source error=%#v", err)
	}

	service.repo = sourceVerifierRepository{
		conversation: agentsdk.Conversation{ID: reference.ConversationID, WorkspaceID: authority.WorkspaceID},
		run:          agentsdk.ConversationRun{ID: reference.RunID, ConversationID: reference.ConversationID, Status: "running", Steps: []agentsdk.ConversationStepView{{}}},
	}
	_, err = service.VerifyConversationSources(t.Context(), agentsdk.ConversationSourceVerificationRequest{
		References: []agentsdk.ConversationRunReference{reference},
		SourceIDs:  []string{"conversation://conversation-a/turn/run-a"},
		Reader:     authority,
	})
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.source_run_incomplete" {
		t.Fatalf("unfinished source error=%#v", err)
	}
}
