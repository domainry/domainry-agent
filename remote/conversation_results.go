package remote

import (
	"context"
	sdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ReadResult(ctx context.Context, conversation, run string, in sdk.ConversationResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	var out sdk.ConversationResultSlice
	err := c.call(ctx, "result_read", sdk.ConversationRPCRequest{Authority: a, ConversationID: conversation, RunID: run, ResultRead: in}, &out)
	return out, err
}

var _ sdk.ConversationResultReader = (*conversationClient)(nil)

func (c *conversationClient) ReadConversationDeliveryResult(ctx context.Context, id string, in sdk.ConversationDeliveryResultRead, a sdk.ConversationAuthority) (sdk.ConversationResultSlice, error) {
	var out sdk.ConversationResultSlice
	err := c.call(ctx, "delegations_result", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, DeliveryResultRead: in}, &out)
	return out, err
}

var _ sdk.ConversationDeliveryResultReader = (*conversationClient)(nil)

func (c *conversationClient) ReadConversationDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactVersion, error) {
	var out sdk.ConversationArtifactVersion
	err := c.call(ctx, "delegations_artifact", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, DeliveryArtifactRead: in}, &out)
	return out, err
}

func (c *conversationClient) DownloadConversationDeliveryArtifact(ctx context.Context, id string, in sdk.ConversationDeliveryArtifactRead, a sdk.ConversationAuthority) (sdk.ConversationArtifactDownload, error) {
	var out sdk.ConversationArtifactDownload
	err := c.call(ctx, "delegations_export", sdk.ConversationRPCRequest{Authority: a, DelegationID: id, DeliveryArtifactRead: in}, &out)
	return out, err
}

var _ sdk.ConversationDeliveryArtifactReader = (*conversationClient)(nil)
