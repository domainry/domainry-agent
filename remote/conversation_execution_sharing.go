package remote

import (
	"context"
	sdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) PublishConversationDelegationExecution(ctx context.Context, id string, in sdk.ConversationExecutionShare, a sdk.ConversationAuthority) (sdk.ConversationExecutionPublication, error) {
	var out sdk.ConversationExecutionPublication
	err := c.call(ctx, "delegations_execution_share", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, ExecutionShare: in}, &out)
	return out, err
}
func (c *conversationClient) ConversationDelegationExecutions(ctx context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationExecutionPublication, error) {
	var out []sdk.ConversationExecutionPublication
	err := c.call(ctx, "delegations_executions", sdk.ConversationRPCRequest{Authority: a, DelegationID: id}, &out)
	return out, err
}

func (c *conversationClient) ConversationDelegationExecutionPublications(ctx context.Context, id string, a sdk.ConversationAuthority) ([]sdk.ConversationExecutionPublication, error) {
	var out []sdk.ConversationExecutionPublication
	err := c.call(ctx, "delegations_execution_publications", sdk.ConversationRPCRequest{Authority: a, DelegationID: id}, &out)
	return out, err
}
func (c *conversationClient) ReadConversationDelegationExecution(ctx context.Context, id string, in sdk.ConversationRunReference, a sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	var out sdk.ConversationRun
	err := c.call(ctx, "delegations_execution", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, ExecutionReference: in}, &out)
	return out, err
}
func (c *conversationClient) ReadConversationDelegationExecutionResult(ctx context.Context, id string, in sdk.ConversationResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	var out sdk.ConversationResultSlice
	err := c.call(ctx, "delegations_execution_result", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, ResultRead: in}, &out)
	return out, err
}

var _ sdk.ConversationExecutionSharingService = (*conversationClient)(nil)
