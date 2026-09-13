package application

import (
	"context"
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
	if err := s.authorizeConversationExecution(ctx, d.SourceConversationID, "", "transfer", a); err != nil {
		return out, err
	}
	if previous, found, err := repo.ConversationDelegationTransferReceipt(ctx, d.ID, in, a); err != nil {
		return out, err
	} else if found {
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
	audit := s.sourceAudit(a, consumer)
	for _, issue := range d.Disagreements {
		for _, ref := range issue.Sources {
			if _, err = audit.run(ctx, ref); err != nil {
				return out, err
			}
		}
	}
	for _, ref := range handoff.Runs {
		if _, err = audit.run(ctx, ref); err != nil {
			return out, err
		}
	}
	for _, ref := range []*sdk.ConversationRunReference{handoff.Source, d.BriefSource, d.InputSource} {
		if ref != nil {
			if _, err = audit.run(ctx, *ref); err != nil {
				return out, err
			}
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
	match, err := s.MatchConversationAgents(ctx, sdk.ConversationAgentMatchRequest{ConversationID: d.SourceConversationID, Requirements: d.Requirements}, a)
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
	agent, err := s.freezeConversationAgent(ctx, in.Transfer.AgentID, a)
	if err != nil {
		return out, err
	}
	targetCtx, err := s.selectConversationAgent(ctx, agent, a)
	if err != nil {
		return out, err
	}
	allowed := []string{}
	for _, key := range agent.Profile.Tools {
		if !strings.HasPrefix(key, "task_") {
			allowed = append(allowed, key)
		}
	}
	task, err := s.prepareConversationTaskStart(targetCtx, sdk.ConversationTaskStart{Goal: d.Brief.Goal, Input: d.Input, AllowedTools: allowed, Budget: d.Budget}, a, d.SourceConversationID, d.SourceRunID, nil)
	if err != nil {
		return out, err
	}
	task.MaxInputBytes = s.options.MaxInputBytes
	task.Brief = &d.Brief
	task.StructuredInput = d.StructuredInput
	task.Handoff = &handoff
	if !conversationText(sdk.ConversationTaskPrompt(task), task.MaxInputBytes, true) {
		return out, conversationFailure("bad_request", "delegation_handoff_exceeded")
	}
	value, err := repo.TransferConversationDelegation(ctx, d.ID, persistence.ConversationDelegationTransferAdmission{Request: in, Agent: *agent, Task: task, Handoff: handoff}, a)
	if err != nil {
		return out, err
	}
	s.signalConversationTasks()
	return s.projectConversationDelegation(ctx, value, a)
}

func (s *ConversationService) checkHandoffSources(ctx context.Context, handoff *sdk.ConversationDelegationHandoff, a sdk.ConversationAuthority, consumer string) error {
	if handoff == nil {
		return nil
	}
	audit := s.sourceAudit(a, consumer)
	if handoff.Source != nil {
		if _, err := audit.run(ctx, *handoff.Source); err != nil {
			return err
		}
	}
	for _, ref := range handoff.Runs {
		if _, err := audit.run(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}
