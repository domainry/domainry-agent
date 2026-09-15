package remote

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ForkConversation(ctx context.Context, conversationID, runID string, in sdk.ConversationForkRequest, authority sdk.ConversationAuthority) (sdk.Conversation, error) {
	var out sdk.Conversation
	err := c.call(ctx, "conversation_fork", sdk.ConversationRPCRequest{Authority: authority, ConversationID: conversationID, RunID: runID, Fork: in}, &out)
	return out, err
}

func (c *conversationClient) ConversationTrajectory(ctx context.Context, conversationID, runID string, authority sdk.ConversationAuthority) (sdk.ConversationTrajectory, error) {
	var out sdk.ConversationTrajectory
	err := c.call(ctx, "trajectory_get", sdk.ConversationRPCRequest{Authority: authority, ConversationID: conversationID, RunID: runID}, &out)
	return out, err
}

func (c *conversationClient) ExportConversationTrajectory(ctx context.Context, conversationID, runID string, authority sdk.ConversationAuthority) (sdk.ConversationTrajectoryExport, error) {
	var out sdk.ConversationTrajectoryExport
	err := c.call(ctx, "trajectory_export", sdk.ConversationRPCRequest{Authority: authority, ConversationID: conversationID, RunID: runID}, &out)
	return out, err
}

func (c *conversationClient) ReplayConversationTrajectory(ctx context.Context, conversationID, runID string, in sdk.ConversationTrajectoryReplayRequest, authority sdk.ConversationAuthority) (sdk.ConversationTrajectoryReplay, error) {
	var out sdk.ConversationTrajectoryReplay
	err := c.call(ctx, "trajectory_replay", sdk.ConversationRPCRequest{Authority: authority, ConversationID: conversationID, RunID: runID, TrajectoryReplay: in}, &out)
	return out, err
}

func (c *conversationClient) CompareConversationTrajectories(ctx context.Context, conversationID, runID string, in sdk.ConversationTrajectoryCompareRequest, authority sdk.ConversationAuthority) (sdk.ConversationTrajectoryComparison, error) {
	var out sdk.ConversationTrajectoryComparison
	err := c.call(ctx, "trajectory_compare", sdk.ConversationRPCRequest{Authority: authority, ConversationID: conversationID, RunID: runID, TrajectoryCompare: in}, &out)
	return out, err
}

var _ sdk.ConversationTrajectoryService = (*conversationClient)(nil)
