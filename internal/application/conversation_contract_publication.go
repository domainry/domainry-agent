package application

import (
	"context"
	"math"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) authorizeContractPublication(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationDelegation, persistence.ConversationDelegationAuthorities, error) {
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
	for _, op := range []string{"view", "manage"} {
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
	if subjects.Issuer.UserID != a.UserID || subjects.Issuer.RuntimeID != a.RuntimeID || subjects.Issuer.WorkspaceID != a.WorkspaceID {
		return d, subjects, conversationFailure("forbidden", "delegation_actor_invalid")
	}
	return d, subjects, nil
}

func (s *ConversationService) ConversationContractPublicationCandidates(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationCandidates, error) {
	var out sdk.ConversationContractPublicationCandidates
	if before < 0 || before == math.MaxInt64 {
		return out, conversationFailure("bad_request", "cursor_invalid")
	}
	d, _, err := s.authorizeContractPublication(ctx, id, a)
	if err != nil {
		return out, err
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	history, err := base.ConversationAgreementHistory(ctx, id, before, a)
	if err != nil {
		return out, err
	}
	out = sdk.ConversationContractPublicationCandidates{Items: []sdk.ConversationContractPublicationCandidate{}, CurrentAgreementRevision: max(1, d.AgreementRevision), Complete: history.Complete, NextBefore: history.NextBefore}
	for _, entry := range history.Items {
		out.Items = append(out.Items, sdk.ConversationContractPublicationCandidate{Revision: entry.Revision, BriefVersion: entry.Brief.Version})
	}
	return out, nil
}

func (s *ConversationService) prepareContractPublication(ctx context.Context, id string, revision int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationPreview, error) {
	var out sdk.ConversationContractPublicationPreview
	if revision < 0 || revision == math.MaxInt64 {
		return out, conversationFailure("bad_request", "agreement_revision_invalid")
	}
	d, subjects, err := s.authorizeContractPublication(ctx, id, a)
	if err != nil {
		return out, err
	}
	repo, ok := s.repo.(persistence.ConversationContractPublicationRepository)
	if !ok {
		return out, conversationFailure("unavailable", "contract_publication_unavailable")
	}
	record, err := repo.ConversationContractPublicationRecord(ctx, id, revision, a)
	if err != nil {
		return out, err
	}
	selected := revision
	if selected == 0 {
		selected = max(1, d.AgreementRevision)
	}
	if record.Agreement.Revision != selected {
		return out, conversationFailure("forbidden", "contract_sources_unverified")
	}
	refs := mergeConversationSources(directContractSourceReferences(record))
	if len(refs) > 0 {
		if _, ok := s.repo.(persistence.ConversationSourceRepository); !ok {
			return out, conversationFailure("unavailable", "contract_publication_source_unavailable")
		}
		if _, ok := s.repo.(persistence.ConversationSourceAuthorityRepository); !ok {
			return out, conversationFailure("unavailable", "contract_publication_source_unavailable")
		}
	}
	ctx, err = s.sourceReleaseContext(ctx, "contract", id, a, a, refs)
	if err != nil {
		return out, err
	}
	for _, ref := range refs {
		if err = s.checkRunSources(ctx, ref, a); err != nil {
			return out, err
		}
	}
	for _, reader := range []sdk.ConversationAuthority{a, subjects.Executor} {
		audit := s.sourceAudit(reader)
		for _, edge := range record.Agreement.Dependencies {
			if _, err = audit.dependency(ctx, edge); err != nil {
				return out, err
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	return sdk.ConversationContractPublicationPreview{Agreement: record.Agreement, Requirements: record.Requirements, Sources: refs, RecordDigest: conversationDigest(record), ExpectedRevision: d.Revision, Publisher: a, RecipientUserID: subjects.Executor.UserID}, nil
}

func (s *ConversationService) PreviewConversationContractPublication(ctx context.Context, id string, in sdk.ConversationContractPublicationRequest, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationPreview, error) {
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	return s.prepareContractPublication(ctx, id, in.AgreementRevision, a)
}

func (s *ConversationService) ConversationContractPublicationHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationContractPublicationHistory, error) {
	if err := s.authorizeCollaborationID(ctx, id, "view", a); err != nil {
		return sdk.ConversationContractPublicationHistory{}, err
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return sdk.ConversationContractPublicationHistory{}, err
	}
	d, err := base.ConversationDelegation(ctx, id, a)
	if err != nil {
		return sdk.ConversationContractPublicationHistory{}, err
	}
	if d.OwnerUserID != "" && d.OwnerUserID != a.UserID && !delegationExecutor(d, a) {
		return sdk.ConversationContractPublicationHistory{}, conversationFailure("forbidden", "collaboration_access_denied")
	}
	repo, ok := s.repo.(persistence.ConversationContractPublicationRepository)
	if !ok {
		return sdk.ConversationContractPublicationHistory{}, conversationFailure("unavailable", "contract_publication_unavailable")
	}
	return repo.ConversationContractPublicationHistory(ctx, id, before, a)
}

func (s *ConversationService) republishConversationContract(ctx context.Context, id string, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	if in.ContractPublication == nil || len(in.ContractPublication.RecordDigest) != 64 || in.ToolRequest != nil || peer.ConversationID != "" || in.Publication != nil || in.Delivery != nil || in.Brief != nil || in.Review != nil || in.Disagreement != nil || in.Transfer != nil || in.Inspection != nil || in.StructuredInput != nil || in.Dependencies != nil || in.Participants != nil {
		return out, conversationFailure("bad_request", "contract_publication_invalid")
	}
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	prepared, err := s.prepareContractPublication(ctx, id, in.ContractPublication.AgreementRevision, a)
	if err != nil {
		return out, err
	}
	if prepared.RecordDigest != in.ContractPublication.RecordDigest {
		return out, conversationFailure("conflict", "contract_record_changed")
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	d, err := base.UpdateConversationDelegation(ctx, id, in, a)
	if err != nil {
		return out, err
	}
	history, err := s.repo.(persistence.ConversationContractPublicationRepository).ConversationContractPublicationHistory(ctx, id, d.Revision+1, a)
	if err != nil {
		return out, err
	}
	for _, item := range history.Items {
		if item.Revision == d.Revision {
			receipt := item
			out.ContractPublication = &receipt
			break
		}
	}
	if out.ContractPublication == nil {
		return out, conversationFailure("unavailable", "contract_publication_unavailable")
	}
	out.ConversationDelegation = sdk.ConversationDelegation{ID: d.ID, Revision: d.Revision, Status: d.Status}
	return out, nil
}

var _ sdk.ConversationContractPublicationReader = (*ConversationService)(nil)
