package application

import (
	"context"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

// PublishConversationProvenance persists a turn that was executed by a trusted
// local runtime without pretending Agent executed the model. Agent assigns the
// durable Conversation/Run identities and owns later access verification.
func (service *ConversationService) PublishConversationProvenance(ctx context.Context, publication agentsdk.ConversationProvenancePublication, authority agentsdk.ConversationAuthority) (agentsdk.ConversationProvenanceReceipt, error) {
	if err := service.authorize(authority); err != nil {
		return agentsdk.ConversationProvenanceReceipt{}, err
	}
	if err := publication.Validate(); err != nil {
		return agentsdk.ConversationProvenanceReceipt{}, conversationFailure("bad_request", "provenance_invalid")
	}
	repository, ok := service.repo.(agentpersistence.ConversationProvenanceRepository)
	if !ok {
		return agentsdk.ConversationProvenanceReceipt{}, conversationFailure("unavailable", "provenance_unavailable")
	}
	conversation, err := service.repo.Create(ctx, agentsdk.ConversationCreate{
		ClientID: publication.ConversationClientID,
		Title:    publication.ConversationTitle,
	}, authority)
	if err != nil {
		return agentsdk.ConversationProvenanceReceipt{}, err
	}
	run, err := repository.ImportCompletedConversationRun(ctx, conversation.ID, publication, authority)
	if err != nil {
		return agentsdk.ConversationProvenanceReceipt{}, err
	}
	sourceID, err := agentsdk.ConversationRunSourceID(agentsdk.ConversationRunReference{ConversationID: conversation.ID, RunID: run.ID})
	if err != nil || run.Status != "completed" || run.CompletedAt == nil {
		return agentsdk.ConversationProvenanceReceipt{}, conversationFailure("unavailable", "provenance_receipt_invalid")
	}
	return agentsdk.ConversationProvenanceReceipt{
		ConversationID: conversation.ID,
		RunID:          run.ID,
		SourceID:       sourceID,
		PublishedAt:    run.CompletedAt.UTC().Truncate(time.Millisecond),
	}, nil
}

var _ agentsdk.ConversationProvenancePublisher = (*ConversationService)(nil)
