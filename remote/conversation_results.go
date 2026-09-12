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
