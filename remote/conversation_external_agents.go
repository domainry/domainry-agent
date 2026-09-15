package remote

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ConversationExternalAgentAssignments(ctx context.Context, in agentsdk.ConversationExternalAgentAssignmentQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentAssignmentPage, error) {
	var out agentsdk.ConversationExternalAgentAssignmentPage
	err := c.call(ctx, "external_agent_assignments", agentsdk.ConversationRPCRequest{Authority: a, ExternalAgentQuery: in}, &out)
	return out, err
}

func (c *conversationClient) ClaimConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentClaim, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentClaimReceipt, error) {
	var out agentsdk.ConversationExternalAgentClaimReceipt
	err := c.call(ctx, "external_agent_claim", agentsdk.ConversationRPCRequest{Authority: a, TaskID: taskID, ExternalAgentClaim: in}, &out)
	return out, err
}

func (c *conversationClient) ReportConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentReport, a agentsdk.ConversationAuthority) (agentsdk.ConversationExternalAgentReportReceipt, error) {
	var out agentsdk.ConversationExternalAgentReportReceipt
	err := c.call(ctx, "external_agent_report", agentsdk.ConversationRPCRequest{Authority: a, TaskID: taskID, ExternalAgentReport: in}, &out)
	return out, err
}

var _ agentsdk.ConversationExternalAgentService = (*conversationClient)(nil)
