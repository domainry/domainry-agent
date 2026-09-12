package remote

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) UploadAttachment(ctx context.Context, conversationID string, in agentsdk.ConversationAttachmentUpload, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var out agentsdk.ConversationAttachment
	if len(in.Data) == 0 || int64(len(in.Data)) > agentsdk.ConversationAttachmentMaxBytes {
		return out, &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.attachment_size_invalid"}
	}
	err := c.call(ctx, "attachments_upload", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentUpload: in}, &out)
	return out, err
}
func (c *conversationClient) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	var out agentsdk.ConversationAttachmentPage
	err := c.call(ctx, "attachments_list", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentAfter: after, Limit: limit}, &out)
	return out, err
}
func (c *conversationClient) Attachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var out agentsdk.ConversationAttachment
	err := c.call(ctx, "attachments_get", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentID: id}, &out)
	return out, err
}
func (c *conversationClient) DownloadAttachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentDownload, error) {
	var out agentsdk.ConversationAttachmentDownload
	err := c.call(ctx, "attachments_download", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentID: id}, &out)
	if err == nil && int64(len(out.Data)) > agentsdk.ConversationAttachmentMaxBytes {
		return agentsdk.ConversationAttachmentDownload{}, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.attachment_content_mismatch"}
	}
	return out, err
}
func (c *conversationClient) DeleteAttachment(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var out agentsdk.ConversationAttachment
	err := c.call(ctx, "attachments_delete", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentID: id, Revision: expected}, &out)
	return out, err
}

var _ agentsdk.ConversationAttachmentService = (*conversationClient)(nil)

func (c *conversationClient) IndexAttachment(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var out agentsdk.ConversationAttachment
	err := c.call(ctx, "attachments_index", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentID: id, Revision: expected}, &out)
	return out, err
}

var _ agentsdk.ConversationAttachmentIndexService = (*conversationClient)(nil)

func (c *conversationClient) CheckAttachmentIndex(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var out agentsdk.ConversationAttachment
	err := c.call(ctx, "attachments_check_index", agentsdk.ConversationRPCRequest{Authority: a, ConversationID: conversationID, AttachmentID: id, Revision: expected}, &out)
	return out, err
}

var _ agentsdk.ConversationAttachmentIndexCheckService = (*conversationClient)(nil)
