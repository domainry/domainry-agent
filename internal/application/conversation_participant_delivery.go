package application

import (
	"context"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) authorizeParticipantDeliverySharing(ctx context.Context, d sdk.ConversationDelegation, a sdk.ConversationAuthority) error {
	if err := s.authorizeCollaboration(ctx, "delivery_read", &d, a); err != nil {
		return err
	}
	repo, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository)
	if !ok {
		return conversationFailure("unavailable", "collaboration_unavailable")
	}
	roots := map[sdk.ConversationRunReference]bool{}
	add := func(record sdk.ConversationDeliveryRecord) error {
		for _, ref := range record.SourceReferences() {
			roots[ref] = true
		}
		if len(roots) > 256 {
			return conversationFailure("unavailable", "source_limit_exceeded")
		}
		return nil
	}
	if d.Delivery != nil {
		record := sdk.ConversationDeliveryRecord{Delivery: *d.Delivery}
		if d.Verification != nil {
			record.Verification = *d.Verification
		}
		if err := add(record); err != nil {
			return err
		}
	}
	for before := int64(0); ; {
		history, err := repo.ConversationDeliveryHistory(ctx, d.ID, before, a)
		if err != nil {
			return err
		}
		for _, record := range history.Items {
			if err := add(record); err != nil {
				return err
			}
		}
		if history.Complete {
			break
		}
		if history.NextBefore < 1 || before > 0 && history.NextBefore >= before {
			return conversationFailure("unavailable", "source_reference_invalid")
		}
		before = history.NextBefore
	}
	for ref := range roots {
		rootCtx := context.WithValue(deliverySourceContext(ctx, d.ID), conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "delivery", delegationID: d.ID, roots: []sdk.ConversationRunReference{ref}})
		// Own roots can be explicitly published by this granting role. A
		// different user's root retains its existing actual publication.
		if source, ok := s.repo.(persistence.ConversationSourceAuthorityRepository); ok {
			if _, err := source.ConversationSourceAuthority(ctx, ref, a); err == nil {
				rootCtx, err = s.sourceReleaseContext(rootCtx, "delivery", d.ID, a, a, []sdk.ConversationRunReference{ref})
				if err != nil {
					return err
				}
			} else {
				var coded *sdk.Error
				if !errors.As(err, &coded) || coded.Class != "not_found" {
					return err
				}
			}
		}
		if err := s.checkRunSources(rootCtx, ref, a); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) projectParticipantDelivery(ctx context.Context, d sdk.ConversationDelegation, a sdk.ConversationAuthority, out *sdk.ConversationDelegationDetail) error {
	if d.Delivery == nil || !out.Access.DeliveryRead {
		return nil
	}
	record := sdk.ConversationDeliveryRecord{Delivery: *d.Delivery}
	if d.Verification != nil {
		record.Verification = *d.Verification
	}
	deliveryCtx := context.WithValue(deliverySourceContext(ctx, d.ID), conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "delivery", delegationID: d.ID, roots: record.SourceReferences()})
	audit := s.sourceAudit(a)
	for _, ref := range mergeConversationSources(record.SourceReferences()) {
		if _, err := audit.run(deliveryCtx, ref); err != nil {
			out.DeliveryOmitted = true
			return nil
		}
	}
	if d.Verification == nil {
		if verifier, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository); ok {
			report, err := verifier.PreviewConversationDeliveryVerification(deliveryCtx, d.ID, a)
			if err != nil {
				out.DeliveryOmitted = true
				return nil
			}
			d.Verification = &report
		}
	}
	if err := s.checkVerificationSources(deliveryCtx, d.Verification, a, ""); err != nil {
		out.DeliveryOmitted = true
		return nil
	}
	out.Delivery, out.Verification = d.Delivery, d.Verification
	return nil
}
