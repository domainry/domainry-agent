package application

import (
	"context"
	"errors"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
)

func dependencySourceError(err error) error {
	var coded *sdk.Error
	if errors.As(err, &coded) && coded.Class == "not_found" {
		return conversationFailure("forbidden", "dependency_source_unavailable")
	}
	return err
}

func directContractSourceReferences(record persistence.ConversationContractPublicationRecord) []sdk.ConversationRunReference {
	// Dependency roots retain their upstream publication and audience.
	// They are not a new publication by the downstream contract owner.
	record.Agreement.Dependencies = nil
	return record.SourceReferences()
}

// The run freezes a flattened dependency graph, whereas the agreement stores
// its direct edges. Rebuild the graph from those exact adopted versions, never
// from today's upstream agreements, before comparing the run's frozen graph.
func (s *ConversationService) admittedContractDependencies(ctx context.Context, dependencies []sdk.ConversationTaskDependency, reader sdk.ConversationAuthority) ([]sdk.ConversationTaskDependency, error) {
	contracts, ok := s.repo.(persistence.ConversationContractPublicationRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "dependency_source_unavailable")
	}
	out := []sdk.ConversationTaskDependency{}
	queue := append([]sdk.ConversationTaskDependency{}, dependencies...)
	seen := map[string]bool{}
	for len(queue) > 0 {
		edge := queue[0]
		queue = queue[1:]
		key := conversationDigest([]any{edge.DelegationID, append([]string{}, edge.Fields...)})
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(seen) > 256 {
			return nil, conversationFailure("forbidden", "dependency_source_unavailable")
		}
		out = append(out, edge)
		// Only inspect immutable graph metadata here. A page's upstream
		// audience, publisher and source rights are checked independently by
		// admittedDependencySourceContext, not against unrelated source owners.
		revision := max(1, edge.AgreementRevision)
		original, err := contracts.ConversationContractPublicationRecord(ctx, edge.DelegationID, revision, reader)
		if err != nil {
			return nil, dependencySourceError(err)
		}
		if original.Agreement.Revision != revision || edge.BriefVersion > 0 && max(1, original.Agreement.Brief.Version) != edge.BriefVersion {
			return nil, conversationFailure("forbidden", "dependency_source_unavailable")
		}
		queue = append(queue, original.Agreement.Dependencies...)
	}
	return out, nil
}

// Older admissions may repeat the same edge because omitted and empty Fields
// had different traversal keys. Only byte-equivalent evidence may collapse;
// conflicting versions, roots or values still invalidate the frozen snapshot.
func canonicalFrozenDependencies(dependencies []sdk.ConversationTaskDependency) ([]sdk.ConversationTaskDependency, error) {
	out := []sdk.ConversationTaskDependency{}
	seen := map[string]string{}
	for _, edge := range dependencies {
		key := conversationDigest([]any{edge.DelegationID, append([]string{}, edge.Fields...)})
		digest := conversationDigest(edge)
		if prior, exists := seen[key]; exists {
			if prior != digest {
				return nil, conversationFailure("forbidden", "delegation_source_not_released")
			}
			continue
		}
		seen[key] = digest
		out = append(out, edge)
	}
	return out, nil
}

// Reading an admitted dependency checks the current upstream audience, while
// its exact original agreement selects provenance. Today's contract cannot
// silently replace the sources or field values frozen in an older run.
func (s *ConversationService) dependencyContractContext(ctx context.Context, ref sdk.ConversationDependencyInput, reader sdk.ConversationAuthority) (context.Context, persistence.ConversationContractPublicationRecord, error) {
	var record persistence.ConversationContractPublicationRecord
	repo, err := s.collaborationRepository()
	if err != nil {
		return ctx, record, err
	}
	d, err := repo.ConversationDelegation(ctx, ref.DelegationID, reader)
	if err != nil {
		return ctx, record, dependencySourceError(err)
	}
	if d.SubjectExited {
		return ctx, record, conversationFailure("forbidden", "dependency_source_unavailable")
	}
	if err := s.authorizeCollaboration(ctx, "view", &d, reader); err != nil {
		return ctx, record, err
	}
	revision := max(1, ref.AgreementRevision)
	if contracts, ok := s.repo.(persistence.ConversationContractPublicationRepository); ok {
		record, err = contracts.ConversationContractPublicationRecord(ctx, d.ID, revision, reader)
		if err != nil {
			return ctx, record, dependencySourceError(err)
		}
	} else {
		if revision != max(1, d.AgreementRevision) {
			return ctx, record, conversationFailure("forbidden", "dependency_source_unavailable")
		}
		record = persistence.ConversationContractPublicationRecord{Agreement: sdk.ConversationAgreementRevision{Revision: revision, Brief: d.Brief, StructuredInput: d.StructuredInput, InputSource: d.InputSource, Source: d.BriefSource, Dependencies: d.Dependencies}, Requirements: d.Requirements}
	}
	if record.Agreement.Revision != revision || ref.BriefVersion > 0 && max(1, record.Agreement.Brief.Version) != ref.BriefVersion {
		return ctx, record, conversationFailure("forbidden", "dependency_source_unavailable")
	}
	if d.OwnerUserID == "" {
		return ctx, record, nil // Legacy host: ordinary source checks grant no new scope.
	}
	return context.WithValue(ctx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: d.ID, roots: directContractSourceReferences(record)}), record, nil
}

func (audit *conversationSourceAudit) dependency(ctx context.Context, edge sdk.ConversationTaskDependency) ([]sdk.ConversationRunReference, error) {
	if edge.DelegationID != "" {
		var record persistence.ConversationContractPublicationRecord
		var err error
		ctx, record, err = audit.s.dependencyContractContext(ctx, edge.ConversationDependencyInput, audit.a)
		if err != nil {
			return nil, err
		}
		values, err := execution.DependencyProjection(record.Agreement.Brief, record.Agreement.StructuredInput, edge.Fields)
		if err != nil {
			return nil, err
		}
		var inputSource *sdk.ConversationRunReference
		if execution.DependencyUsesInput(edge.Fields) {
			inputSource = record.Agreement.InputSource
		}
		if conversationDigest(edge.Source) != conversationDigest(record.Agreement.Source) || conversationDigest(edge.InputSource) != conversationDigest(inputSource) || conversationDigest(values) != edge.Digest || len(edge.Values) > 0 && conversationDigest(edge.Values) != edge.Digest {
			return nil, conversationFailure("forbidden", "dependency_source_unavailable")
		}
	}
	var roots []sdk.ConversationRunReference
	for _, ref := range []*sdk.ConversationRunReference{edge.Source, edge.InputSource} {
		if ref != nil {
			part, err := audit.run(ctx, *ref)
			if err != nil {
				return nil, dependencySourceError(err)
			}
			roots = mergeConversationSources(roots, part)
		}
	}
	return roots, nil
}
