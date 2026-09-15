package remote

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) ConversationTask(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	var out sdk.ConversationTaskDetail
	err := c.call(ctx, "tasks_get", sdk.ConversationRPCRequest{Authority: a, TaskID: id}, &out)
	return out, err
}

func (c *conversationClient) ConversationTasks(ctx context.Context, in sdk.ConversationTaskQuery, a sdk.ConversationAuthority) (sdk.ConversationTaskPage, error) {
	var out sdk.ConversationTaskPage
	err := c.call(ctx, "tasks_list", sdk.ConversationRPCRequest{Authority: a, TaskQuery: in}, &out)
	return out, err
}

func (c *conversationClient) CancelConversationTask(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	var out sdk.ConversationTaskDetail
	err := c.call(ctx, "tasks_cancel", sdk.ConversationRPCRequest{Authority: a, TaskID: id}, &out)
	return out, err
}

func (c *conversationClient) ResumeConversationTask(ctx context.Context, id string, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	var out sdk.ConversationTaskDetail
	err := c.call(ctx, "tasks_resume", sdk.ConversationRPCRequest{Authority: a, TaskID: id}, &out)
	return out, err
}

func (c *conversationClient) UpdateConversationTaskAgreement(ctx context.Context, id string, in sdk.ConversationTaskAgreementUpdate, a sdk.ConversationAuthority) (sdk.ConversationTaskDetail, error) {
	var out sdk.ConversationTaskDetail
	err := c.call(ctx, "tasks_update", sdk.ConversationRPCRequest{Authority: a, TaskID: id, TaskAgreementUpdate: in}, &out)
	return out, err
}

var _ sdk.ConversationTaskService = (*conversationClient)(nil)
var _ sdk.ConversationTaskControlService = (*conversationClient)(nil)
var _ sdk.ConversationTaskAgreementService = (*conversationClient)(nil)
