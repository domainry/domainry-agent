package application

import (
	"context"
	"errors"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) transferConversationDelegation(ctx context.Context, d sdk.ConversationDelegation, in sdk.ConversationDelegationUpdate, a sdk.ConversationAuthority) (sdk.ConversationDelegationDetail, error) {
	var out sdk.ConversationDelegationDetail
	if in.Transfer == nil || !conversationKey(in.Transfer.AgentID) || !conversationText(in.Transfer.RemainingWork, 8192, true) || in.Brief != nil || in.StructuredInput != nil || in.Inspection != nil || in.Delivery != nil {
		return out, conversationFailure("bad_request", "delegation_transfer_invalid")
	}
	repo, ok := s.repo.(persistence.ConversationDelegationTransferRepository)
	if !ok {
		return out, conversationFailure("unavailable", "collaboration_unavailable")
	}
	manager := d.OwnerUserID != "" && d.OwnerUserID != a.UserID
	recoverCurrent := in.Transfer.AgentID == d.ToAgentID
	matchConversation := d.SourceConversationID
	if manager {
		// The grant governs this delegation; it grants no private source control.
		matchConversation = ""
		if err := s.authorizeCollaboration(ctx, "manage", &d, a); err != nil {
			return out, err
		}
	} else if err := s.authorizeConversationExecution(ctx, d.SourceConversationID, "", "transfer", a); err != nil {
		return out, err
	}
	if recoverCurrent {
		// Cycle detection rejects assigning an ancestor to itself. Recovery is a
		// new immutable assignment of the same logical Agent's current snapshot,
		// and is validated against the old snapshot by the repository.
		matchConversation = ""
	}
	if previous, found, err := repo.ConversationDelegationTransferReceipt(ctx, d.ID, in, a); err != nil {
		return out, err
	} else if found {
		if manager {
			return delegationManagementReceipt(previous), nil
		}
		return s.ConversationDelegation(ctx, previous.ID, a)
	}
	handoff, err := repo.PrepareConversationDelegationHandoff(ctx, d.ID, in.ExpectedRevision, in.Transfer.RemainingWork, a)
	if err != nil {
		return out, err
	}
	if in.ToolRequest != nil {
		handoff.Source = &sdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
	}
	consumer := "new_transfer_conversation"
	handoffCtx := context.WithValue(ctx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "execution", delegationID: d.ID, roots: handoff.Runs})
	contractCtx := ctx
	if manager {
		var err error
		contractCtx, err = s.delegationContractSourceContext(ctx, d, a)
		if err != nil {
			return out, err
		}
	}
	audit := s.sourceAudit(a, consumer)
	for _, issue := range d.Disagreements {
		for _, ref := range issue.Sources {
			if _, err = audit.run(ctx, ref); err != nil {
				return out, err
			}
		}
	}
	for _, ref := range handoff.Runs {
		if _, err = audit.run(handoffCtx, ref); err != nil {
			return out, conversationFailure("forbidden", "delegation_handoff_source_unavailable")
		}
	}
	for _, ref := range []*sdk.ConversationRunReference{d.BriefSource, d.InputSource} {
		if ref != nil {
			if _, err = audit.run(contractCtx, *ref); err != nil {
				return out, err
			}
		}
	}
	if handoff.Source != nil {
		if _, err = audit.run(ctx, *handoff.Source); err != nil {
			return out, err
		}
	}
	dependencies := []sdk.ConversationDependencyInput{}
	if in.Dependencies != nil {
		dependencies = *in.Dependencies
	} else {
		for _, edge := range d.Dependencies {
			dependencies = append(dependencies, edge.ConversationDependencyInput)
		}
	}
	if err = s.validateDependencySources(ctx, dependencies, consumer, a); err != nil {
		return out, err
	}
	matchRequirements := d.Requirements
	if manager {
		// Existing roots use their original publication, never a new publication
		// attributed to the manager merely because they can manage the work.
		for _, ref := range d.Requirements.Sources {
			if _, err := audit.run(contractCtx, ref); err != nil {
				return out, err
			}
		}
		matchRequirements.Sources = nil
	}
	match, err := s.MatchConversationAgents(ctx, sdk.ConversationAgentMatchRequest{ConversationID: matchConversation, Requirements: matchRequirements}, a)
	if err != nil {
		return out, err
	}
	eligible := false
	for _, candidate := range match.Items {
		if candidate.AgentID == in.Transfer.AgentID && candidate.CanAccept {
			eligible = true
		}
	}
	if !eligible {
		return out, conversationFailure("conflict", "agent_requirements_unmet")
	}
	agent, executor, err := s.delegationExecutionAgent(ctx, in.Transfer.AgentID, a)
	if err != nil {
		return out, err
	}
	targetCtx, err := s.selectConversationAgent(delegationExecutorContext(ctx), agent, executor)
	if err != nil {
		return out, err
	}
	prospective := d
	prospective.ToAgentID, prospective.ExecutionSubject = agent.ID, executionSubject(executor)
	if err := s.authorizeCollaboration(targetCtx, "receive", &prospective, executor); err != nil {
		return out, err
	}
	contractRefs := append([]sdk.ConversationRunReference{}, d.Requirements.Sources...)
	for _, ref := range []*sdk.ConversationRunReference{d.BriefSource, d.InputSource} {
		if ref != nil {
			contractRefs = append(contractRefs, *ref)
		}
	}
	if manager {
		readerCtx := context.WithValue(targetCtx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "contract", delegationID: d.ID, roots: contractRefs})
		for _, ref := range mergeConversationSources(contractRefs) {
			if _, err := s.sourceAudit(executor, consumer).run(readerCtx, ref); err != nil {
				return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
			}
		}
	} else if err := s.checkProspectiveDelegationSources(targetCtx, mergeConversationSources(contractRefs), a, executor); err != nil {
		return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
	}
	if handoff.Source != nil {
		if err := s.checkProspectiveDelegationSources(targetCtx, []sdk.ConversationRunReference{*handoff.Source}, a, executor); err != nil {
			return out, conversationFailure("forbidden", "delegation_contract_source_unavailable")
		}
	}
	if executor != a {
		if err := s.validateDependencySources(targetCtx, dependencies, consumer, executor); err != nil {
			return out, err
		}
		readerCtx := context.WithValue(targetCtx, conversationPublishedSourceKey{}, conversationPublishedSource{purpose: "execution", delegationID: d.ID, roots: handoff.Runs})
		readerAudit := s.sourceAudit(executor, consumer)
		for _, ref := range handoff.Runs {
			if _, err := readerAudit.run(readerCtx, ref); err != nil {
				return out, conversationFailure("forbidden", "delegation_handoff_source_unavailable")
			}
		}
		for _, issue := range d.Disagreements {
			for _, ref := range issue.Sources {
				if _, err := readerAudit.run(targetCtx, ref); err != nil {
					return out, err
				}
			}
		}
	}
	allowed := []string{}
	for _, key := range agent.Profile.Tools {
		if !strings.HasPrefix(key, "task_") {
			allowed = append(allowed, key)
		}
	}
	authorizationConversation, authorizationRun := d.SourceConversationID, d.SourceRunID
	if executor.UserID != d.OwnerUserID {
		authorizationConversation, authorizationRun = "", ""
	}
	var model *sdk.ConversationModelRequestSelection
	if d.ModelExplicit && d.Model != nil {
		model = &sdk.ConversationModelRequestSelection{Key: d.Model.Key, ReasoningEffort: d.Model.ReasoningEffort}
	}
	task, err := s.prepareConversationTaskStart(targetCtx, sdk.ConversationTaskStart{Goal: d.Brief.Goal, Input: d.Input, AllowedTools: allowed, Budget: d.Budget, Model: model}, executor, authorizationConversation, authorizationRun, nil)
	if err != nil {
		return out, err
	}
	task.MaxInputBytes = s.options.MaxInputBytes
	task.SourceConversationID, task.SourceRunID = d.SourceConversationID, d.SourceRunID
	task.Brief = &d.Brief
	task.StructuredInput = d.StructuredInput
	task.Handoff = &handoff
	if !conversationText(sdk.ConversationTaskPrompt(task), task.MaxInputBytes, true) {
		return out, conversationFailure("bad_request", "delegation_handoff_exceeded")
	}
	admission := persistence.ConversationDelegationTransferAdmission{Request: in, Agent: *agent, Task: task, Handoff: handoff}
	if agent.DelegationRoleKey != "" {
		admission.ExecutionAuthority = &executor
	}
	value, err := repo.TransferConversationDelegation(ctx, d.ID, admission, a)
	if err != nil {
		return out, err
	}
	s.signalConversationTasks()
	if manager {
		return delegationManagementReceipt(value), nil
	}
	return s.projectConversationDelegation(ctx, value, a)
}

func (s *ConversationService) checkHandoffSources(ctx context.Context, handoff *sdk.ConversationDelegationHandoff, a sdk.ConversationAuthority, consumer string) error {
	if handoff == nil {
		return nil
	}
	audit := s.sourceAudit(a, consumer)
	if handoff.Source != nil {
		if _, err := audit.run(ctx, *handoff.Source); err != nil {
			return handoffSourceError(err)
		}
	}
	for _, ref := range handoff.Runs {
		if _, err := audit.run(ctx, ref); err != nil {
			return handoffSourceError(err)
		}
	}
	return nil
}

func handoffSourceError(err error) error {
	var coded *sdk.Error
	if errors.As(err, &coded) && coded.Class == "not_found" {
		return conversationFailure("forbidden", "delegation_handoff_source_unavailable")
	}
	return err
}
