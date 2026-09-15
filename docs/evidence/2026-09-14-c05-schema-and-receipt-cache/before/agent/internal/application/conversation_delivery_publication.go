package application

import (
	"context"
	"math"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) authorizeDeliveryPublication(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationDelegation, persistence.ConversationDelegationAuthorities, error) {
	var d sdk.ConversationDelegation
	var subjects persistence.ConversationDelegationAuthorities
	base, err := s.collaborationRepository()
	if err != nil {
		return d, subjects, err
	}
	d, err = base.ConversationDelegation(ctx, id, a)
	if err != nil {
		return d, subjects, err
	}
	for _, op := range []string{"view", "receive", "delivery_read"} {
		if err = s.authorizeCollaboration(ctx, op, &d, a); err != nil {
			return d, subjects, err
		}
	}
	if err = s.authorizeCollaboration(ctx, "share", nil, a); err != nil {
		return d, subjects, err
	}
	actors, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
	if !ok {
		return d, subjects, conversationFailure("unavailable", "collaboration_unavailable")
	}
	subjects, err = actors.ConversationDelegationAuthorities(ctx, id, a)
	if err != nil {
		return d, subjects, err
	}
	if subjects.Executor.UserID != a.UserID || subjects.Executor.RuntimeID != a.RuntimeID || subjects.Executor.WorkspaceID != a.WorkspaceID {
		return d, subjects, conversationFailure("forbidden", "delegation_actor_invalid")
	}
	return d, subjects, nil
}

func (s *ConversationService) ConversationDeliveryPublicationCandidates(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryPublicationCandidates, error) {
	var out sdk.ConversationDeliveryPublicationCandidates
	if before < 0 || before == math.MaxInt64 {
		return out, conversationFailure("bad_request", "delivery_revision_invalid")
	}
	d, _, err := s.authorizeDeliveryPublication(ctx, id, a)
	if err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationDeliveryVerificationRepository)
	if !ok {
		return out, conversationFailure("unavailable", "delivery_publication_unavailable")
	}
	history, err := repo.ConversationDeliveryHistory(ctx, id, before, a)
	if err != nil {
		return out, err
	}
	out = sdk.ConversationDeliveryPublicationCandidates{Items: []sdk.ConversationDeliveryPublicationCandidate{}, CurrentAvailable: d.Delivery != nil, Complete: history.Complete, NextBefore: history.NextBefore}
	for _, entry := range history.Items {
		out.Items = append(out.Items, sdk.ConversationDeliveryPublicationCandidate{Revision: entry.Revision, Kind: entry.Kind, BriefVersion: entry.Delivery.BriefVersion, AgreementRevision: max(1, entry.Delivery.AgreementRevision)})
	}
	return out, nil
}

func (s *ConversationService) prepareDeliveryPublication(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (sdk.ConversationDeliveryPublicationPreview, error) {
	var out sdk.ConversationDeliveryPublicationPreview
	if revision < 0 || revision == math.MaxInt64 {
		return out, conversationFailure("bad_request", "delivery_revision_invalid")
	}
	d, subjects, err := s.authorizeDeliveryPublication(ctx, id, a)
	if err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationDeliveryPublicationRepository)
	if !ok {
		return out, conversationFailure("unavailable", "delivery_publication_unavailable")
	}
	record, err := repo.ConversationDeliveryPublicationRecord(ctx, id, revision, a)
	if err != nil {
		return out, err
	}
	refs := append([]sdk.ConversationRunReference{}, record.Delivery.Evidence...)
	for _, condition := range record.Delivery.Conditions {
		for _, ref := range condition.Receipts {
			if ref.Step < 0 || ref.Step > 255 {
				return out, conversationFailure("bad_request", "result_reference_invalid")
			}
			refs = append(refs, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	for _, check := range record.Verification.Checks {
		for _, ref := range check.Receipts {
			if ref.Step < 0 || ref.Step > 255 {
				return out, conversationFailure("bad_request", "result_reference_invalid")
			}
			refs = append(refs, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID, BeforeStep: ref.Step + 2})
		}
	}
	if record.Verification.Source != nil && record.Verification.ActorID == a.UserID {
		refs = append(refs, *record.Verification.Source)
	}
	if len(refs) > 0 {
		if _, ok := s.repo.(persistence.ConversationSourceRepository); !ok {
			return out, conversationFailure("unavailable", "delivery_publication_source_unavailable")
		}
		if _, ok := s.repo.(persistence.ConversationSourceAuthorityRepository); !ok {
			return out, conversationFailure("unavailable", "delivery_publication_source_unavailable")
		}
	}
	// Prospective sharing is authorized as a new publication by the current
	// actor. An unverifiable old publisher cannot manufacture or block this
	// explicit decision. Every immutable root must still belong to that actor.
	ctx, err = s.sourceReleaseContext(ctx, "delivery", id, a, a, mergeConversationSources(refs))
	if err != nil {
		return out, err
	}
	for _, ref := range mergeConversationSources(refs) {
		if err = s.checkRunSources(ctx, ref, a); err != nil {
			return out, err
		}
	}
	for _, condition := range record.Delivery.Conditions {
		if err = s.checkCompletionReceipts(ctx, condition.Receipts, a, ""); err != nil {
			return out, err
		}
	}
	for _, check := range record.Verification.Checks {
		if err = s.checkCompletionReceipts(ctx, check.Receipts, a, ""); err != nil {
			return out, err
		}
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return sdk.ConversationDeliveryPublicationPreview{Record: record, RecordDigest: conversationDigest(record), ExpectedRevision: d.Revision, Publisher: a, RecipientUserID: subjects.Issuer.UserID}, nil
}

func (s *ConversationService) PreviewConversationDeliveryPublication(ctx context.Context, id string, in sdk.ConversationDeliveryPublicationRequest, a sdk.ConversationAuthority) (sdk.ConversationDeliveryPublicationPreview, error) {
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	out, err := s.prepareDeliveryPublication(ctx, id, in.DeliveryRevision, a)
	if err != nil {
		return sdk.ConversationDeliveryPublicationPreview{}, err
	}
	// Another actor's private assessment run is not exposed by preparation.
	// The commit digest still binds the untouched canonical record.
	if out.Record.Verification.ActorID != a.UserID {
		out.Record.Verification.Source = nil
	}
	return out, nil
}

func (s *ConversationService) republishConversationDelivery(ctx context.Context, id string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	if in.Publication == nil || in.ContractPublication != nil || len(in.Publication.RecordDigest) != 64 || in.ToolRequest != nil || peer.ConversationID != "" || in.Delivery != nil || in.Brief != nil || in.Review != nil || in.Disagreement != nil || in.Transfer != nil || in.Inspection != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Participants != nil {
		return out, conversationFailure("bad_request", "delivery_publication_invalid")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	prepared, err := s.prepareDeliveryPublication(ctx, id, in.Publication.DeliveryRevision, a)
	if err != nil {
		return out, err
	}
	if prepared.RecordDigest != in.Publication.RecordDigest {
		return out, conversationFailure("conflict", "delivery_record_changed")
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	d, err := base.UpdateConversationDelegation(ctx, id, in, a)
	if err != nil {
		return out, err
	}
	entry, err := s.repo.(persistence.ConversationDeliveryPublicationRepository).ConversationDeliveryPublicationRecord(ctx, id, d.Revision, a)
	if err != nil {
		return out, err
	}
	// Return a narrow publication receipt. This mutation is not a read grant
	// for the relationship's conversation, task, messages or other deliveries.
	out.ConversationDelegation = sdk.ConversationDelegation{ID: d.ID, Revision: d.Revision, Status: d.Status}
	out.Publication = entry.Publication
	return out, nil
}

var _ sdk.ConversationDeliveryPublicationReader = (*ConversationService)(nil)
