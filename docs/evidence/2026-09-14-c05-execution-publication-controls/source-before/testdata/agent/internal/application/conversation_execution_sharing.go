package application

import (
	"context"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) PublishConversationDelegationExecution(ctx context.Context, id string, in sdk.ConversationExecutionShare, a sdk.ConversationAuthority) (sdk.ConversationExecutionPublication, error) {
	var empty sdk.ConversationExecutionPublication
	peer, _ := ctx.Value(conversationPeerRequestKey{}).(conversationPeerRequest)
	in.ToolRequest = peer.ToolRequest
	if in.Reference.BeforeStep != 0 || in.Reference.ConversationID == "" || in.Reference.RunID == "" || in.ExpectedRevision < 1 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 {
		return empty, conversationFailure("bad_request", "execution_publication_invalid")
	}
	base, err := s.collaborationRepository()
	if err != nil {
		return empty, err
	}
	d, err := base.ConversationDelegation(ctx, id, a)
	if err != nil {
		return empty, err
	}
	ops := []string{"view"}
	if !in.Withdraw {
		ops = append(ops, "execution_read")
	}
	for _, op := range ops {
		if err := s.authorizeCollaboration(ctx, op, &d, a); err != nil {
			return empty, err
		}
	}
	repo, ok := s.repo.(persistence.ConversationExecutionSharingRepository)
	if !ok {
		return empty, conversationFailure("unavailable", "execution_sharing_unavailable")
	}
	// Withdrawal must remain possible without reading old business data.
	if !in.Withdraw {
		if err := s.authorizeCollaboration(ctx, "share", nil, a); err != nil {
			return empty, err
		}
		run, err := s.repo.Run(ctx, in.Reference.ConversationID, in.Reference.RunID, a)
		if err != nil {
			return empty, err
		}
		if run.BackgroundTask == nil || run.BackgroundTask.DelegationID != d.ID {
			return empty, conversationFailure("forbidden", "delegation_source_not_released")
		}
		ctx, cancel := s.sourceAccessContext(ctx)
		defer cancel()
		publicationCtx, err := s.sourceReleaseContext(ctx, "execution", "", a, a, []sdk.ConversationRunReference{in.Reference})
		if err != nil {
			return empty, err
		}
		if err := s.checkRunSources(publicationCtx, in.Reference, a); err != nil {
			return empty, err
		}
		return repo.PublishConversationDelegationExecution(publicationCtx, id, in, a)
	}
	return repo.PublishConversationDelegationExecution(ctx, id, in, a)
}

func (s *ConversationService) executionPublicationContext(ctx context.Context, id string, release persistence.ConversationSourceRelease, a sdk.ConversationAuthority) (context.Context, error) {
	if release.DelegationID != id || release.Purpose != "execution" || release.Publisher == nil || release.Reference.BeforeStep != 0 {
		return ctx, conversationFailure("forbidden", "execution_not_shared")
	}
	ctx = context.WithValue(ctx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "execution", delegationID: id, roots: []sdk.ConversationRunReference{release.Reference}})
	ctx, err := s.sourceReleaseContext(ctx, "execution", id, *release.Publisher, a, []sdk.ConversationRunReference{release.Reference})
	if err != nil {
		return ctx, err
	}
	if releasedEvidenceAuthority(ctx, release.Reference, a) != release.Producer {
		return ctx, conversationFailure("forbidden", "execution_subject_mismatch")
	}
	// A submitted business result can inherit its own delegation's working
	// context. Keep that exact provenance scope while the release itself still
	// requires execution_read. Raw collaboration fields retain typed checks.
	ctx = deliverySourceContext(ctx, id)
	return ctx, nil
}

func (s *ConversationService) ConversationDelegationExecutions(ctx context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationExecutionPublication, error) {
	if err := s.authorizeCollaborationID(ctx, id, "execution_read", a); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(persistence.ConversationExecutionSharingRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "execution_sharing_unavailable")
	}
	releases, err := repo.ConversationDelegationExecutions(ctx, id, a)
	if err != nil {
		return nil, err
	}
	out := []sdk.ConversationExecutionPublication{}
	for _, release := range releases {
		if _, err := s.executionPublicationContext(ctx, id, release, a); err != nil {
			if collaborationDenied(err) {
				continue
			}
			return nil, err
		}
		out = append(out, sdk.ConversationExecutionPublication{Reference: release.Reference, Publisher: *release.Publisher})
	}
	return out, nil
}

func (s *ConversationService) sharedDelegationRun(ctx context.Context, id string, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (context.Context, sdk.ConversationAuthority, error) {
	if err := s.authorizeCollaborationID(ctx, id, "execution_read", a); err != nil {
		return ctx, a, err
	}
	if ref.BeforeStep != 0 || ref.ConversationID == "" || ref.RunID == "" {
		return ctx, a, conversationFailure("bad_request", "execution_reference_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationExecutionSharingRepository)
	if !ok {
		return ctx, a, conversationFailure("unavailable", "execution_sharing_unavailable")
	}
	releases, err := repo.ConversationDelegationExecutions(ctx, id, a)
	if err != nil {
		return ctx, a, err
	}
	for _, release := range releases {
		if release.Reference != ref {
			continue
		}
		candidate, err := s.executionPublicationContext(ctx, id, release, a)
		if err != nil {
			return ctx, a, err
		}
		return candidate, release.Producer, nil
	}
	return ctx, a, conversationFailure("forbidden", "execution_not_shared")
}

func (s *ConversationService) ReadConversationDelegationExecution(ctx context.Context, id string, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	return s.readDelegationExecution(ctx, id, ref, a, s.sourceAudit(a))
}

func (s *ConversationService) readDelegationExecution(ctx context.Context, id string, ref sdk.ConversationRunReference, a sdk.ConversationAuthority, audit *conversationSourceAudit) (sdk.ConversationRun, error) {
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	ctx, producer, err := s.sharedDelegationRun(ctx, id, ref, a)
	if err != nil {
		return sdk.ConversationRun{}, err
	}
	run, err := s.repo.Run(ctx, ref.ConversationID, ref.RunID, producer)
	if err != nil {
		return sdk.ConversationRun{}, err
	}
	if run.BackgroundTask == nil || run.BackgroundTask.DelegationID != id {
		return sdk.ConversationRun{}, conversationFailure("forbidden", "delegation_source_not_released")
	}
	if _, err := audit.run(ctx, ref); err != nil {
		return sdk.ConversationRun{}, err
	}
	// Receipt-reading policy proves completed outcomes. Pending parameters and
	// failed payloads have no successful receipt proof and remain private.
	if source, ok := s.repo.(persistence.ConversationSourceRepository); ok {
		snapshot, err := source.ConversationSourceSnapshot(ctx, ref, producer)
		if err != nil {
			return sdk.ConversationRun{}, err
		}
		redactUnverifiedExecutionCalls(&run, snapshot.Calls)
	}
	// Reading does not carry an interactive confirmation or mutation scope.
	run.WriteScope = nil
	run.Interaction = nil
	return run, nil
}

func (s *ConversationService) ReadConversationDelegationExecutionResult(ctx context.Context, id string, in sdk.ConversationResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	return s.readDelegationExecutionResult(ctx, id, in, a, s.sourceAudit(a))
}

func (s *ConversationService) readDelegationExecutionResult(ctx context.Context, id string, in sdk.ConversationResultRead, a sdk.ConversationAuthority, audit *conversationSourceAudit) (sdk.ConversationResultSlice, error) {
	record, err := s.delegationExecutionResultRecord(ctx, id, in.Reference, a, audit)
	if err != nil {
		return sdk.ConversationResultSlice{}, err
	}
	return s.conversationResultSlice(in, *record.Result)
}

func (s *ConversationService) delegationExecutionResultRecord(ctx context.Context, id string, reference sdk.ConversationResultReference, a sdk.ConversationAuthority, audit *conversationSourceAudit) (persistence.ConversationToolExecution, error) {
	ctx, cancel := s.sourceAccessContext(ctx)
	defer cancel()
	ref := sdk.ConversationRunReference{ConversationID: reference.ConversationID, RunID: reference.RunID}
	ctx, producer, err := s.sharedDelegationRun(ctx, id, ref, a)
	if err != nil {
		return persistence.ConversationToolExecution{}, err
	}
	repo, ok := s.repo.(persistence.ConversationResultRepository)
	if !ok {
		return persistence.ConversationToolExecution{}, conversationFailure("unavailable", "result_read_unavailable")
	}
	record, err := repo.ConversationResult(ctx, reference, producer)
	if err != nil {
		return persistence.ConversationToolExecution{}, err
	}
	if record.Result == nil || record.State != "completed" || record.Step != reference.Step || record.Call.ID != reference.CallID || conversationDigest(record.Result) != reference.SHA256 {
		return persistence.ConversationToolExecution{}, conversationFailure("conflict", "result_reference_changed")
	}
	if record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return persistence.ConversationToolExecution{}, conversationFailure("forbidden", "execution_result_not_readable")
	}
	ctx = deliveryResultSourceContext(ctx, id, record)
	ref.BeforeStep = reference.Step + 2
	if _, err := audit.run(ctx, ref); err != nil {
		return persistence.ConversationToolExecution{}, err
	}
	audit.evidenceOwner = &producer
	if _, err := audit.record(ctx, ref, record); err != nil {
		return persistence.ConversationToolExecution{}, err
	}
	return record, nil
}

var _ sdk.ConversationExecutionSharingService = (*ConversationService)(nil)

func redactUnverifiedExecutionCalls(run *sdk.ConversationRun, records []persistence.ConversationToolExecution) {
	type callKey struct {
		step int
		id   string
	}
	verified := map[callKey]bool{}
	for _, record := range records {
		verified[callKey{record.Step, record.Call.ID}] = record.State == "completed" && record.Result != nil && record.Result.Status == "completed" && record.Result.ErrorCode == ""
	}
	omitted := false
	for i := range run.Steps {
		for j := range run.Steps[i].Calls {
			call := &run.Steps[i].Calls[j]
			if !verified[callKey{run.Steps[i].Number, call.ID}] {
				omitted = true
				*call = sdk.ConversationToolView{ID: call.ID, Name: call.Name, Status: call.Status, Effect: call.Effect, StartedAt: call.StartedAt, CompletedAt: call.CompletedAt, DurationMilliseconds: call.DurationMilliseconds, AccessError: "execution_result_not_readable"}
			}
		}
	}
	if omitted {
		run.DraftText = ""
		run.DraftBytes = 0
		for i := range run.Steps {
			run.Steps[i].Text = ""
		}
	}
}
