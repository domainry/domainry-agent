package application

import (
	"context"
	"sort"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) UpdateConversationTaskAgreement(ctx context.Context, id string, in agentsdk.ConversationTaskAgreementUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskDetail, error) {
	return s.updateConversationTaskAgreement(ctx, id, in, a, true)
}

func (s *ConversationService) updateConversationTaskAgreement(ctx context.Context, id string, in agentsdk.ConversationTaskAgreementUpdate, a agentsdk.ConversationAuthority, authorize bool) (agentsdk.ConversationTaskDetail, error) {
	if authorize {
		if err := s.authorizeConversationTaskControl(ctx, id, "task_update", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	if !conversationKey(id) || !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.Reason, 4096, true) {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("bad_request", "task_agreement_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	for _, operation := range []string{"manage", "execution_read"} {
		if err = s.authorizeCollaborationTask(ctx, task, operation, a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	if task.DelegationID != "" {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "task_agreement_delegated")
	}
	if !validConversationBrief(in.Brief) || !conversationText(in.Brief.Audience, 512, true) || !validConversationBriefProvenance(in.Brief, true) {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("bad_request", "task_agreement_invalid")
	}
	in.Brief.ExplicitFields = append([]string(nil), in.Brief.ExplicitFields...)
	in.Brief.InferredFields = append([]string(nil), in.Brief.InferredFields...)
	sort.Strings(in.Brief.ExplicitFields)
	sort.Strings(in.Brief.InferredFields)
	if in.Brief.DueAt != nil {
		due := in.Brief.DueAt.UTC().Truncate(time.Millisecond)
		in.Brief.DueAt = &due
	}
	repo, ok := s.repo.(persistence.ConversationTaskAgreementRepository)
	if !ok {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_agreement_unavailable")
	}
	task, _, err = repo.UpdateConversationTaskAgreement(ctx, id, in, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	return s.projectConversationTask(ctx, task, a, true)
}

var _ agentsdk.ConversationTaskAgreementService = (*ConversationService)(nil)
