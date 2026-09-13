package remote

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ConversationCollaborationAccess(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationCollaborationAuthorization, error) {
	var out agentsdk.ConversationCollaborationAuthorization
	err := c.call(ctx, "agents_access", agentsdk.ConversationRPCRequest{Authority: a}, &out)
	return out, err
}

func (c *conversationClient) ConversationAgents(ctx context.Context, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentPage, error) {
	var out agentsdk.ConversationAgentPage
	err := c.call(ctx, "agents_list", agentsdk.ConversationRPCRequest{Authority: a}, &out)
	return out, err
}
func (c *conversationClient) WriteConversationAgent(ctx context.Context, id string, in agentsdk.ConversationAgentWrite, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgent, error) {
	var out agentsdk.ConversationAgent
	op := "agents_update"
	if id == "" {
		op = "agents_create"
	}
	err := c.call(ctx, op, agentsdk.ConversationRPCRequest{Authority: a, AgentID: id, AgentWrite: in}, &out)
	return out, err
}
func (c *conversationClient) CreateConversationDelegation(ctx context.Context, in agentsdk.ConversationDelegationCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	var out agentsdk.ConversationDelegationDetail
	err := c.call(ctx, "delegations_create", agentsdk.ConversationRPCRequest{Authority: a, DelegationCreate: in}, &out)
	return out, err
}
func (c *conversationClient) ConversationDelegations(ctx context.Context, conversationID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationPage, error) {
	var out agentsdk.ConversationDelegationPage
	err := c.call(ctx, "delegations_list", agentsdk.ConversationRPCRequest{Authority: a, TaskQuery: agentsdk.ConversationTaskQuery{SourceConversationID: conversationID}}, &out)
	return out, err
}
func (c *conversationClient) ConversationDelegation(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	var out agentsdk.ConversationDelegationDetail
	err := c.call(ctx, "delegations_get", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id}, &out)
	return out, err
}
func (c *conversationClient) UpdateConversationDelegation(ctx context.Context, id string, in agentsdk.ConversationDelegationUpdate, a agentsdk.ConversationAuthority) (agentsdk.ConversationDelegationDetail, error) {
	var out agentsdk.ConversationDelegationDetail
	err := c.call(ctx, "delegations_update", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id, DelegationUpdate: in}, &out)
	return out, err
}
func (c *conversationClient) SendConversationAgentMessage(ctx context.Context, id string, in agentsdk.ConversationAgentMessageSend, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMessage, error) {
	var out agentsdk.ConversationAgentMessage
	err := c.call(ctx, "delegations_message", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id, AgentMessage: in}, &out)
	return out, err
}

var _ agentsdk.ConversationCollaborationService = (*conversationClient)(nil)

func (c *conversationClient) MatchConversationAgents(ctx context.Context, in agentsdk.ConversationAgentMatchRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgentMatchPage, error) {
	var out agentsdk.ConversationAgentMatchPage
	err := c.call(ctx, "agents_match", agentsdk.ConversationRPCRequest{Authority: a, AgentMatch: in}, &out)
	return out, err
}

func (c *conversationClient) ConversationAgreementHistory(ctx context.Context, id string, before int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAgreementHistory, error) {
	var out agentsdk.ConversationAgreementHistory
	err := c.call(ctx, "delegations_history", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id, AgreementBefore: before}, &out)
	return out, err
}

func (c *conversationClient) ConversationDeliveryHistory(ctx context.Context, id string, before int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationDeliveryHistory, error) {
	var out agentsdk.ConversationDeliveryHistory
	err := c.call(ctx, "delegations_deliveries", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id, AgreementBefore: before}, &out)
	return out, err
}

func (c *conversationClient) ConversationDisagreementHistory(ctx context.Context, id, disagreementID string, before int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationDisagreementHistory, error) {
	var out agentsdk.ConversationDisagreementHistory
	err := c.call(ctx, "delegations_disagreement", agentsdk.ConversationRPCRequest{Authority: a, DelegationID: id, DisagreementID: disagreementID, AgreementBefore: before}, &out)
	return out, err
}
