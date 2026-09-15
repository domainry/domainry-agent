package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) externalAgentRepository() (persistence.ConversationExternalAgentRepository, error) {
	repo, ok := s.repo.(persistence.ConversationExternalAgentRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "external_agent_unavailable")
	}
	return repo, nil
}

func (s *ConversationService) authorizeExternalAgentTask(ctx context.Context, task agentsdk.ConversationTask, agentID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegation, error) {
	if task.Agent == nil || task.Agent.ID != agentID || task.Agent.External == nil || task.ExternalExecution == nil || task.DelegationID == "" {
		return agentsdk.ConversationDelegation{}, conversationFailure("not_found", "external_agent_task_not_found")
	}
	repo, err := s.collaborationRepository()
	if err != nil {
		return agentsdk.ConversationDelegation{}, err
	}
	d, err := repo.ConversationDelegation(ctx, task.DelegationID, a)
	if err != nil {
		return d, err
	}
	if d.TaskID != task.ID || d.ToAgentID != agentID || !delegationExecutor(d, a) {
		return d, conversationFailure("forbidden", "external_agent_identity_mismatch")
	}
	if err = s.authorizeCollaboration(delegationExecutorContext(ctx), "receive", &d, a); err != nil {
		return d, err
	}
	_, err = s.selectConversationAgent(delegationExecutorContext(ctx), task.Agent, a)
	return d, err
}

func (s *ConversationService) ConversationExternalAgentAssignments(ctx context.Context, in agentsdk.ConversationExternalAgentAssignmentQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentAssignmentPage, error) {
	out := agentsdk.ConversationExternalAgentAssignmentPage{Items: []agentsdk.ConversationExternalAgentAssignment{}, Complete: true}
	if err := s.authorize(a); err != nil || !conversationKey(in.AgentID) || in.Limit < 0 || in.Limit > 32 {
		if err != nil {
			return out, err
		}
		return out, conversationFailure("bad_request", "external_agent_query_invalid")
	}
	if in.Limit == 0 {
		in.Limit = 16
	}
	repo, err := s.externalAgentRepository()
	if err != nil {
		return out, err
	}
	tasks, complete, err := repo.ConversationExternalAgentTasks(ctx, in.AgentID, in.Limit, a)
	if err != nil {
		return out, err
	}
	out.Complete = complete
	for _, task := range tasks {
		d, authErr := s.authorizeExternalAgentTask(ctx, task, in.AgentID, a)
		if authErr != nil {
			return out, authErr
		}
		detail, detailErr := s.projectConversationDelegation(ctx, d, a)
		if detailErr != nil {
			return out, detailErr
		}
		out.Items = append(out.Items, agentsdk.ConversationExternalAgentAssignment{Delegation: detail})
	}
	return out, nil
}

func (s *ConversationService) ClaimConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentClaim, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentClaimReceipt, error) {
	var out agentsdk.ConversationExternalAgentClaimReceipt
	if err := s.authorize(a); err != nil {
		return out, err
	}
	if !conversationKey(taskID) || !conversationKey(in.ClientID) || !conversationKey(in.AgentID) {
		return out, conversationFailure("bad_request", "external_agent_claim_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, taskID, a)
	if err != nil {
		return out, err
	}
	if _, err = s.authorizeExternalAgentTask(ctx, task, in.AgentID, a); err != nil {
		return out, err
	}
	if conversationDigest(in.Capabilities) != conversationDigest(task.Agent.External.Capabilities) {
		return out, conversationFailure("conflict", "external_agent_capability_mismatch")
	}
	repo, err := s.externalAgentRepository()
	if err != nil {
		return out, err
	}
	record, err := repo.ClaimConversationExternalAgentTask(ctx, taskID, in, a)
	if err != nil {
		return out, err
	}
	out.Replay = record.Replay
	out.Task, err = s.projectConversationTask(ctx, record.Task, a, true)
	return out, err
}

func (s *ConversationService) ReportConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentReport, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentReportReceipt, error) {
	var out agentsdk.ConversationExternalAgentReportReceipt
	if err := s.authorize(a); err != nil {
		return out, err
	}
	if !conversationKey(taskID) || !conversationKey(in.ClientID) || !conversationKey(in.AgentID) || !conversationKey(in.SessionID) || in.ExpectedLastEventSeq < 0 {
		return out, conversationFailure("bad_request", "external_agent_report_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, taskID, a)
	if err != nil {
		return out, err
	}
	if _, err = s.authorizeExternalAgentTask(ctx, task, in.AgentID, a); err != nil {
		return out, err
	}
	repo, err := s.externalAgentRepository()
	if err != nil {
		return out, err
	}
	record, err := repo.ReportConversationExternalAgentTask(ctx, taskID, in, a)
	if err != nil {
		return out, err
	}
	out.Replay = record.Replay
	out.Task, err = s.projectConversationTask(ctx, record.Task, a, true)
	return out, err
}

var _ agentsdk.ConversationExternalAgentService = (*ConversationService)(nil)
