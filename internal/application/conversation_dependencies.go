package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

func validateDependencyInputs(refs []sdk.ConversationDependencyInput) error {
	if len(refs) > 16 {
		return conversationFailure("bad_request", "dependencies_invalid")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !conversationKey(ref.DelegationID) || ref.BriefVersion < 1 || ref.AgreementRevision < 0 || seen[ref.DelegationID] {
			return conversationFailure("bad_request", "dependencies_invalid")
		}
		seen[ref.DelegationID] = true
		if _, err := execution.DependencyProjection(sdk.ConversationTaskBrief{}, nil, ref.Fields); err != nil {
			return conversationFailure("bad_request", "dependencies_invalid")
		}
	}
	return nil
}

func (s *ConversationService) validateDependencySources(ctx context.Context, refs []sdk.ConversationDependencyInput, consumer string, a sdk.ConversationAuthority) error {
	if err := validateDependencyInputs(refs); err != nil {
		return err
	}
	audit := s.sourceAudit(a, consumer)
	seen := map[string]bool{}
	queue := append([]sdk.ConversationDependencyInput{}, refs...)
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		key := conversationDigest(ref)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 256 {
			return conversationFailure("bad_request", "dependencies_invalid")
		}
		itemCtx, record, err := s.dependencyContractContext(ctx, ref, a)
		if err != nil {
			return err
		}
		if record.Agreement.InputSource != nil && execution.DependencyUsesInput(ref.Fields) {
			if _, err = audit.run(itemCtx, *record.Agreement.InputSource); err != nil {
				return err
			}
		}
		if record.Agreement.Source != nil {
			if _, err = audit.run(itemCtx, *record.Agreement.Source); err != nil {
				return err
			}
		}
		for _, edge := range record.Agreement.Dependencies {
			if _, err := audit.dependency(ctx, edge); err != nil {
				return err
			}
			queue = append(queue, edge.ConversationDependencyInput)
		}
	}
	return nil
}

func (s *ConversationService) ConversationAgreementHistory(ctx context.Context, id string, before int64, a sdk.ConversationAuthority) (sdk.ConversationAgreementHistory, error) {
	var out sdk.ConversationAgreementHistory
	if err := s.authorizeCollaborationID(ctx, id, "view", a); err != nil {
		return out, err
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return out, err
	}
	out, err = repo.ConversationAgreementHistory(ctx, id, before, a)
	if err != nil {
		return out, err
	}
	audit := s.sourceAudit(a)
	for _, item := range out.Items {
		itemCtx := ctx
		refs := []sdk.ConversationRunReference{}
		if publications, ok := s.repo.(persistence.ConversationContractPublicationRepository); ok {
			record, recordErr := publications.ConversationContractPublicationRecord(ctx, id, item.Revision, a)
			if recordErr != nil {
				return sdk.ConversationAgreementHistory{}, recordErr
			}
			refs = directContractSourceReferences(record)
		}
		if len(refs) > 0 {
			itemCtx = context.WithValue(ctx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: id, roots: refs})
			for _, ref := range mergeConversationSources(refs) {
				if _, err = audit.run(itemCtx, ref); err != nil {
					return sdk.ConversationAgreementHistory{}, err
				}
			}
		}
		if item.Requirements != nil {
			for _, ref := range item.Requirements.Sources {
				if _, err = audit.run(itemCtx, ref); err != nil {
					return sdk.ConversationAgreementHistory{}, err
				}
			}
		}
		if item.ChangeSource != nil {
			if _, err = audit.run(itemCtx, *item.ChangeSource); err != nil {
				return sdk.ConversationAgreementHistory{}, err
			}
		}
		if item.InputSource != nil {
			if _, err = audit.run(itemCtx, *item.InputSource); err != nil {
				return sdk.ConversationAgreementHistory{}, err
			}
		}
		if item.Source != nil {
			if _, err = audit.run(itemCtx, *item.Source); err != nil {
				return sdk.ConversationAgreementHistory{}, err
			}
		}
		for _, edge := range item.Dependencies {
			if _, err := audit.dependency(ctx, edge); err != nil {
				return sdk.ConversationAgreementHistory{}, err
			}
		}
	}
	return out, nil
}

func (s *ConversationService) dependencyStates(ctx context.Context, d sdk.ConversationDelegation, a sdk.ConversationAuthority) ([]sdk.ConversationDependencyState, error) {
	out := []sdk.ConversationDependencyState{}
	repo, err := s.collaborationRepository()
	if err != nil {
		return nil, err
	}
	for _, edge := range d.Dependencies {
		upstream, err := repo.ConversationDelegation(ctx, edge.DelegationID, a)
		if err != nil {
			return nil, dependencySourceError(err)
		}
		if upstream.SubjectExited {
			return nil, conversationFailure("forbidden", "dependency_source_unavailable")
		}
		if err := s.authorizeCollaboration(ctx, "view", &upstream, a); err != nil {
			return nil, err
		}
		state := sdk.ConversationDependencyState{DelegationID: edge.DelegationID, CurrentBriefVersion: upstream.Brief.Version, CurrentAgreementRevision: max(1, upstream.AgreementRevision), State: "current"}
		values, err := execution.DependencyProjection(upstream.Brief, upstream.StructuredInput, edge.Fields)
		if err != nil {
			return nil, err
		}
		if conversationDigest(values) != edge.Digest {
			state.State = "changed"
		}
		for _, change := range upstream.PendingChanges {
			if change.SourceDelegationID != upstream.ID {
				state.State = "needs_review"
			}
		}
		out = append(out, state)
	}
	return out, nil
}
