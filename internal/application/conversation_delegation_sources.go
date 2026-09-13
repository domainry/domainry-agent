package application

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Introduced only after an exact admitted source and its current audience have
// been checked. It selects source-owned result reading, never raw execution.
type conversationContractResultKey struct{}

func (s *ConversationService) checkProspectiveDelegationSources(ctx context.Context, refs []sdk.ConversationRunReference, issuer, reader sdk.ConversationAuthority) error {
	if len(refs) == 0 {
		return nil
	}
	ctx, err := s.sourceReleaseContext(ctx, "contract", "", issuer, reader, refs)
	if err != nil {
		return err
	}
	audit := s.sourceAudit(reader, "new_peer_conversation")
	for _, ref := range refs {
		if _, err := audit.run(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationService) delegationOwnsSourceRun(ctx context.Context, d sdk.ConversationDelegation, run sdk.ConversationRun, reader sdk.ConversationAuthority) (bool, error) {
	task := run.BackgroundTask
	if task == nil || task.DelegationID != d.ID {
		return false, nil
	}
	if d.ConversationID == run.ConversationID && d.TaskID == task.TaskID {
		return true, nil
	}
	repo, ok := s.repo.(persistence.ConversationDelegationTransferRepository)
	if !ok {
		return false, nil
	}
	assignments, err := repo.ConversationDelegationAssignments(ctx, d.ID, reader)
	if err != nil {
		return false, err
	}
	for _, assignment := range assignments {
		if assignment.ConversationID == run.ConversationID && assignment.TaskID == task.TaskID {
			return true, nil
		}
	}
	return false, nil
}

func (s *ConversationService) delegationRunSourceContext(ctx context.Context, run sdk.ConversationRun, reader sdk.ConversationAuthority) (context.Context, error) {
	task := run.BackgroundTask
	if task == nil || task.DelegationID == "" {
		return ctx, nil
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return ctx, err
	}
	d, err := repo.ConversationDelegation(ctx, task.DelegationID, reader)
	if err != nil {
		return ctx, err
	}
	owned, err := s.delegationOwnsSourceRun(ctx, d, run, reader)
	if err != nil {
		return ctx, err
	}
	if !owned || conversationDigest(task.Requirements.Sources) != conversationDigest(d.Requirements.Sources) {
		return ctx, conversationFailure("conflict", "delegation_contract_source_unavailable")
	}
	return s.delegationContractSourceContext(ctx, d, reader)
}

func (s *ConversationService) delegationSourceResult(ctx context.Context, owner sdk.ConversationRunReference, ownerAuthority, reader sdk.ConversationAuthority, args sdk.ConversationDelegationSourceRead, parent *conversationSourceAudit) (persistence.ConversationToolExecution, []sdk.ConversationRunReference, error) {
	var empty persistence.ConversationToolExecution
	if !conversationKey(args.ID) || args.Reference.Step < 0 || args.Reference.Step > 255 {
		return empty, nil, conversationFailure("bad_request", "result_reference_invalid")
	}
	run, err := s.repo.Run(ctx, owner.ConversationID, owner.RunID, ownerAuthority)
	if err != nil {
		return empty, nil, err
	}
	if run.BackgroundTask == nil || run.BackgroundTask.DelegationID != args.ID {
		return empty, nil, conversationFailure("forbidden", "delegation_source_not_released")
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return empty, nil, err
	}
	d, err := repo.ConversationDelegation(ctx, args.ID, reader)
	if err != nil {
		return empty, nil, err
	}
	owned, err := s.delegationOwnsSourceRun(ctx, d, run, reader)
	if err != nil {
		return empty, nil, err
	}
	if !owned || conversationDigest(run.BackgroundTask.Requirements.Sources) != conversationDigest(d.Requirements.Sources) {
		return empty, nil, conversationFailure("forbidden", "delegation_source_not_released")
	}
	ref := sdk.ConversationRunReference{ConversationID: args.Reference.ConversationID, RunID: args.Reference.RunID, BeforeStep: args.Reference.Step + 2}
	matched := false
	for _, root := range run.BackgroundTask.Requirements.Sources {
		matched = matched || sourcePrefixContains(root, ref)
	}
	if !matched {
		return empty, nil, conversationFailure("forbidden", "delegation_source_not_released")
	}
	subjectRepo, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
	if !ok {
		return empty, nil, conversationFailure("unavailable", "delegation_execution_unavailable")
	}
	subjects, err := subjectRepo.ConversationDelegationAuthorities(ctx, d.ID, reader)
	if err != nil {
		return empty, nil, err
	}
	if origin, ok := s.repo.(persistence.ConversationSourceAuthorityRepository); ok {
		ownerAuthority, err = origin.ConversationSourceAuthority(ctx, owner, ownerAuthority)
		if err != nil {
			return empty, nil, err
		}
		// Assignment membership was checked above. A historical run retains
		// its own immutable execution role after another assignment takes over.
		// The live invocation entry separately requires today's exact executor.
		if err := s.authorize(ownerAuthority); err != nil {
			return empty, nil, err
		}
		if ownerAuthority.RuntimeID != reader.RuntimeID || ownerAuthority.WorkspaceID != reader.WorkspaceID || run.Agent != nil && run.Agent.DelegationRoleKey != "" && run.Agent.DelegationRoleKey != ownerAuthority.RoleKey {
			return empty, nil, conversationFailure("forbidden", "execution_subject_mismatch")
		}
	} else if ownerAuthority != subjects.Executor {
		return empty, nil, conversationFailure("forbidden", "execution_subject_mismatch")
	}
	ctx, err = s.delegationContractSourceContext(ctx, d, reader)
	if err == nil {
		ctx, err = s.publishedReferenceContext(ctx, ref, reader)
	}
	if err != nil {
		return empty, nil, err
	}
	ctx = context.WithValue(ctx, conversationContractResultKey{}, true)
	producer := releasedEvidenceAuthority(ctx, ref, reader)
	results, ok := s.repo.(persistence.ConversationResultRepository)
	if !ok {
		return empty, nil, conversationFailure("unavailable", "result_read_unavailable")
	}
	record, err := results.ConversationResult(ctx, args.Reference, producer)
	if err != nil {
		return empty, nil, err
	}
	if record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" || record.Step != args.Reference.Step || record.Call.ID != args.Reference.CallID || conversationDigest(record.Result) != args.Reference.SHA256 {
		return empty, nil, conversationFailure("conflict", "result_reference_changed")
	}
	audit := s.sourceAudit(reader, owner.ConversationID)
	if parent != nil {
		// A typed wrapper starts a separate result-reading scope, while cycle
		// guards span the whole traversal. A wrapper cannot reset those guards.
		audit.reading, audit.records = parent.reading, parent.records
	}
	roots, err := audit.run(ctx, ref)
	if err != nil {
		return empty, nil, err
	}
	audit.evidenceOwner = &producer
	part, err := audit.record(ctx, ref, record)
	return record, mergeConversationSources(roots, part, []sdk.ConversationRunReference{ref}), err
}

func (s *ConversationService) readDelegationSource(ctx context.Context, in sdk.ConversationToolRequest, args sdk.ConversationDelegationSourceRead) (sdk.ConversationDelegationSourceSlice, error) {
	var empty sdk.ConversationDelegationSourceSlice
	repo, err := s.collaborationRepository()
	if err != nil {
		return empty, err
	}
	d, err := repo.ConversationDelegation(ctx, args.ID, in.Authority)
	if err != nil {
		return empty, err
	}
	if d.ConversationID != in.ConversationID || d.Status != "running" && d.Status != "delivered" {
		return empty, conversationFailure("forbidden", "delegation_source_not_released")
	}
	if err := s.authorizeCollaboration(ctx, "receive", &d, in.Authority); err != nil {
		return empty, err
	}
	subjects, ok := s.repo.(persistence.ConversationDelegationExecutionRepository)
	if !ok {
		return empty, conversationFailure("unavailable", "delegation_execution_unavailable")
	}
	authorities, err := subjects.ConversationDelegationAuthorities(ctx, d.ID, in.Authority)
	if err != nil {
		return empty, err
	}
	if authorities.Executor != in.Authority {
		return empty, conversationFailure("forbidden", "execution_subject_mismatch")
	}
	record, _, err := s.delegationSourceResult(ctx, sdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID}, in.Authority, in.Authority, args, nil)
	if err != nil {
		return empty, err
	}
	page, err := s.conversationResultSlice(args.ConversationResultRead, *record.Result)
	return sdk.ConversationDelegationSourceSlice{DelegationID: args.ID, ConversationResultSlice: page}, err
}

func verifyDelegationSourcePage(args sdk.ConversationDelegationSourceRead, saved sdk.ConversationDelegationSourceSlice, result sdk.ConversationToolResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if args.MaxBytes == 0 {
		args.MaxBytes = 4096
	}
	if saved.DelegationID != args.ID || saved.Reference != args.Reference || saved.Offset != args.Offset || args.MaxBytes < 256 || args.MaxBytes > 8192 || saved.TotalBytes != len(raw) || saved.Offset < 0 || saved.Offset > len(raw) || saved.NextOffset < saved.Offset || saved.NextOffset > min(len(raw), saved.Offset+args.MaxBytes) || saved.NextOffset == saved.Offset && !saved.Complete || saved.Complete != (saved.NextOffset == len(raw)) {
		return invalidPersonalReceipt()
	}
	if saved.Offset < len(raw) && !utf8.RuneStart(raw[saved.Offset]) || saved.NextOffset < len(raw) && !utf8.RuneStart(raw[saved.NextOffset]) || saved.JSONText != string(raw[saved.Offset:saved.NextOffset]) {
		return invalidPersonalReceipt()
	}
	return nil
}

func (audit *conversationSourceAudit) delegationSourceToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	definition, _ := collaborationTool("delegation_source_read")
	if conversationDigest(definition) != conversationDigest(record.Definition) {
		return nil, conversationFailure("conflict", "tool_changed")
	}
	if err := audit.connectedTool(ctx, definition.Key); err != nil {
		return nil, err
	}
	var args sdk.ConversationDelegationSourceRead
	var saved sdk.ConversationDelegationSourceSlice
	if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || record.Result.ResourceID != args.ID {
		return nil, invalidPersonalReceipt()
	}
	original, roots, err := audit.s.delegationSourceResult(ctx, owner, audit.evidenceAuthority(owner), audit.a, args, audit)
	if err != nil {
		return nil, err
	}
	if err := verifyDelegationSourcePage(args, saved, *original.Result); err != nil {
		return nil, err
	}
	return mergeConversationSources(roots, []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}), nil
}
