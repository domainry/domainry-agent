package remote

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) UploadKnowledgeDocument(ctx context.Context, libraryID string, in agentsdk.KnowledgeDocumentUpload, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	if len(in.Data) == 0 || int64(len(in.Data)) > agentsdk.ConversationAttachmentMaxBytes {
		return out, &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.document_size_invalid"}
	}
	err := c.call(ctx, "documents_upload", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentUpload: in}, &out)
	return out, err
}
func (c *conversationClient) KnowledgeDocuments(ctx context.Context, libraryID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentPage, error) {
	var out agentsdk.KnowledgeDocumentPage
	err := c.call(ctx, "documents_list", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentAfter: after, Limit: limit}, &out)
	return out, err
}
func (c *conversationClient) KnowledgeDocument(ctx context.Context, libraryID, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_get", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id}, &out)
	return out, err
}
func (c *conversationClient) DownloadKnowledgeDocument(ctx context.Context, libraryID, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	var out agentsdk.KnowledgeDocumentDownload
	err := c.call(ctx, "documents_download", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id}, &out)
	if err == nil && int64(len(out.Data)) > agentsdk.ConversationAttachmentMaxBytes {
		return agentsdk.KnowledgeDocumentDownload{}, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.document_content_mismatch"}
	}
	return out, err
}
func (c *conversationClient) DeleteKnowledgeDocument(ctx context.Context, libraryID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_delete", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id, Revision: expected}, &out)
	return out, err
}

var _ agentsdk.KnowledgeDocumentService = (*conversationClient)(nil)

func (c *conversationClient) ImportConversationAttachment(ctx context.Context, library string, in agentsdk.KnowledgeAttachmentImport, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_import_attachment", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: library, DocumentImport: in}, &out)
	return out, err
}

var _ agentsdk.KnowledgeAttachmentImportService = (*conversationClient)(nil)

func (c *conversationClient) TransferKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentTransfer, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_transfer", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: library, DocumentTransfer: in}, &out)
	return out, err
}

var _ agentsdk.KnowledgeDocumentTransferService = (*conversationClient)(nil)
