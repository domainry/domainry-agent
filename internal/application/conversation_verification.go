package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

func (s *ConversationService) checkCompletionReceipts(ctx context.Context, refs []sdk.ConversationResultReference, a sdk.ConversationAuthority, consumer string) error {
	if len(refs) == 0 {
		return nil
	}
	repo, ok := s.repo.(persistence.ConversationExecutionReadRepository)
	if !ok {
		return conversationFailure("unavailable", "execution_read_unavailable")
	}
	audit := s.sourceAudit(a, consumer)
	for _, ref := range refs {
		if _, err := audit.run(ctx, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2}); err != nil {
			return err
		}
		producer, err := s.sharedEvidenceAuthority(ctx, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2}, a)
		if err != nil {
			return err
		}
		record, err := repo.ReadExecutionCall(ctx, ref.ConversationID, ref.RunID, ref.Step, ref.CallID, producer)
		if err != nil {
			return err
		}
		if record.State != "completed" || record.Result == nil || conversationDigest(record.Result) != ref.SHA256 {
			return conversationFailure("conflict", "completion_evidence_changed")
		}
	}
	return nil
}

func (s *ConversationService) checkCompletionAssessments(ctx context.Context, brief sdk.ConversationTaskBrief, entries []sdk.ConversationConditionAssessment, a sdk.ConversationAuthority, consumer string) error {
	if err := execution.ValidateConditionAssessments(brief, entries); err != nil {
		return conversationFailure("bad_request", "completion_assessment_invalid")
	}
	for _, entry := range entries {
		if err := s.checkCompletionReceipts(ctx, entry.Receipts, a, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) checkVerificationSources(ctx context.Context, report *sdk.ConversationDeliveryVerification, a sdk.ConversationAuthority, consumer string) error {
	if report == nil {
		return nil
	}
	if report.Source != nil {
		if _, err := s.sourceAudit(a, consumer).run(ctx, *report.Source); err != nil {
			return err
		}
	}
	for _, check := range report.Checks {
		if err := s.checkCompletionReceipts(ctx, check.Receipts, a, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) ConversationDeliveryHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryHistory, error) {
	var out sdk.ConversationDeliveryHistory
	if err := s.authorizeCollaborationID(ctx, id, "delivery_read", a); err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository)
	if !ok {
		return out, conversationFailure("unavailable", "collaboration_unavailable")
	}
	value, err := repo.ConversationDeliveryHistory(ctx, id, before, a)
	if err != nil {
		return out, err
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	d, err := base.ConversationDelegation(ctx, id, a)
	if err != nil {
		return out, err
	}
	for _, entry := range value.Items {
		recorded := d
		recorded.Delivery, recorded.Verification = &entry.Delivery, &entry.Verification
		entryCtx, err := s.delegationDeliverySourceContext(ctx, recorded, a)
		if err != nil {
			return out, err
		}
		if err = s.checkVerificationSources(entryCtx, &entry.Verification, a, ""); err != nil {
			return out, err
		}
		for _, ref := range entry.Delivery.Evidence {
			if _, err = s.sourceAudit(a).run(entryCtx, ref); err != nil {
				return out, err
			}
		}
		for _, claim := range entry.Delivery.Conditions {
			if err = s.checkCompletionReceipts(entryCtx, claim.Receipts, a, ""); err != nil {
				return out, err
			}
		}
	}
	return value, nil
}
