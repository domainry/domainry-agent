package application

import (
	"context"
	"math"
	"slices"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) ReadConversationDeliveryResult(ctx context.Context, id string, in sdk.ConversationDeliveryResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	record, err := s.authorizedDeliveryResult(ctx, id, in, a)
	if err != nil {
		return sdk.ConversationResultSlice{}, err
	}
	return s.conversationResultSlice(in.ConversationResultRead, *record.Result)
}

func (s *ConversationService) authorizedDeliveryResult(ctx context.Context, id string, in sdk.ConversationDeliveryResultRead, a sdk.ConversationAuthority) (persistence.ConversationToolExecution, error) {
	var empty persistence.ConversationToolExecution
	if err := s.authorizeCollaborationID(ctx, id, "delivery_read", a); err != nil {
		return empty, err
	}
	if in.DeliveryRevision < 0 || in.DeliveryRevision == math.MaxInt64 {
		return empty, conversationFailure("bad_request", "delivery_revision_invalid")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	var delivery *sdk.ConversationDelegationDelivery
	var verification *sdk.ConversationDeliveryVerification
	if in.DeliveryRevision == 0 {
		repo, err := s.collaborationRepository()
		if err != nil {
			return empty, err
		}
		d, err := repo.ConversationDelegation(ctx, id, a)
		if err != nil {
			return empty, err
		}
		delivery, verification = d.Delivery, d.Verification
	} else {
		repo, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository)
		if !ok {
			return empty, conversationFailure("unavailable", "collaboration_unavailable")
		}
		history, err := repo.ConversationDeliveryHistory(ctx, id, in.DeliveryRevision+1, a)
		if err != nil {
			return empty, err
		}
		for _, item := range history.Items {
			if item.Revision == in.DeliveryRevision {
				delivery, verification = &item.Delivery, &item.Verification
				break
			}
		}
	}
	// A run-level evidence reference does not release every call in that run.
	// Only explicit result references in the submitted projection qualify.
	matched := false
	if delivery != nil {
		for _, condition := range delivery.Conditions {
			matched = matched || slices.Contains(condition.Receipts, in.Reference)
		}
		if verification != nil {
			for _, check := range verification.Checks {
				matched = matched || slices.Contains(check.Receipts, in.Reference)
			}
		}
	}
	if !matched {
		return empty, conversationFailure("forbidden", "delivery_result_not_released")
	}
	repo, ok := s.repo.(persistence.ConversationResultRepository)
	if !ok {
		return empty, conversationFailure("unavailable", "result_read_unavailable")
	}
	ref := sdk.ConversationRunReference{ConversationID: in.Reference.ConversationID, RunID: in.Reference.RunID, BeforeStep: in.Reference.Step + 2}
	if _, ok := s.repo.(persistence.ConversationSourceReleaseRepository); ok {
		ctx = context.WithValue(ctx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "delivery", delegationID: id, roots: []sdk.ConversationRunReference{ref}})
	}
	// Published evidence can have the same storage owner and a different
	// original execution role. Establish its scope before traversing the run;
	// successful user-owned storage lookup is not proof identity resolution.
	sourceAudit := s.sourceAudit(a)
	if releaseCtx, found, err := sourceAudit.publishedSourceContext(ctx, ref); err != nil {
		return empty, err
	} else if found {
		ctx = releaseCtx
	}
	producer, err := s.sharedEvidenceAuthority(ctx, ref, a)
	if err != nil {
		return empty, err
	}
	record, err := repo.ConversationResult(ctx, in.Reference, producer)
	if err != nil {
		return empty, err
	}
	if record.Result == nil || record.State != "completed" || record.Result.Status != "completed" || record.Result.ErrorCode != "" || record.Call.ID != in.Reference.CallID || record.Step != in.Reference.Step || conversationDigest(record.Result) != in.Reference.SHA256 {
		return empty, conversationFailure("conflict", "result_reference_changed")
	}
	ctx = deliveryResultSourceContext(ctx, id, record)
	audit := s.sourceAudit(a)
	if _, err = audit.run(ctx, sdk.ConversationRunReference{ConversationID: in.Reference.ConversationID, RunID: in.Reference.RunID, BeforeStep: in.Reference.Step + 2}); err != nil {
		return empty, err
	}
	if producer != a {
		audit.evidenceOwner = &producer
	}
	// Check the fetched record too, so an incomplete source snapshot cannot
	// accidentally authorize bytes that it never traversed.
	if _, err = audit.record(ctx, sdk.ConversationRunReference{ConversationID: in.Reference.ConversationID, RunID: in.Reference.RunID}, record); err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	return record, nil
}

var _ sdk.ConversationDeliveryResultReader = (*ConversationService)(nil)
