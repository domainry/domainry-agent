package remote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/telemetry"
)

const knowledgeDocumentMetadataHeader = "X-Agent-Knowledge-Document-Metadata"

type knowledgeDocumentUploadMetadata struct {
	LibraryID string                           `json:"library_id"`
	Upload    agentsdk.KnowledgeDocumentUpload `json:"upload"`
	Authority agentsdk.ConversationAuthority   `json:"authority"`
}

type knowledgeDocumentChunkUploadMetadata struct {
	LibraryID string                                `json:"library_id"`
	Chunk     agentsdk.KnowledgeDocumentUploadChunk `json:"chunk"`
	Authority agentsdk.ConversationAuthority        `json:"authority"`
}

func validKnowledgeDocumentSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

func readKnowledgeDocumentResponse(response *http.Response, output any) error {
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("Agent SaaS response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode/100 != 2 {
		var failure struct{ Code string }
		_ = json.Unmarshal(raw, &failure)
		class := "internal"
		switch response.StatusCode {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			class = "bad_request"
		case http.StatusForbidden, http.StatusUnauthorized:
			class = "forbidden"
		case http.StatusNotFound:
			class = "not_found"
		case http.StatusConflict:
			class = "conflict"
		case http.StatusTooManyRequests:
			class = "rate_limited"
		case http.StatusServiceUnavailable:
			class = "unavailable"
		}
		if failure.Code == "" {
			failure.Code = "agent.conversation.document_stream_unavailable"
		}
		return &agentsdk.Error{Class: class, Code: failure.Code, Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	}
	if output == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, output)
}

func (c *conversationClient) UploadKnowledgeDocument(ctx context.Context, libraryID string, in agentsdk.KnowledgeDocumentUpload, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	if c == nil || c.client == nil || c.client.baseURL == "" || c.client.apiKey == "" {
		return out, fmt.Errorf("Agent SaaS is not configured")
	}
	if authority.RuntimeID != c.runtimeID {
		return out, &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.runtime_denied"}
	}
	if in.Content == nil || in.Bytes < 1 || in.Bytes > agentsdk.KnowledgeDocumentMaximumBytes || !validKnowledgeDocumentSHA256(in.SHA256) {
		return out, &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.document_size_invalid"}
	}
	metadata, err := json.Marshal(knowledgeDocumentUploadMetadata{LibraryID: libraryID, Upload: in, Authority: authority})
	if err != nil {
		return out, err
	}
	ctx = telemetry.WithHTTPClientOperation(ctx, "agent.knowledge_document_upload")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/agent/v1/conversations/documents_upload", in.Content)
	if err != nil {
		return out, err
	}
	request.ContentLength = in.Bytes
	request.Header.Set("Authorization", "Bearer "+c.client.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(knowledgeDocumentMetadataHeader, base64.RawURLEncoding.EncodeToString(metadata))
	response, err := c.client.http.Do(request)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	err = readKnowledgeDocumentResponse(response, &out)
	return out, err
}

func (c *conversationClient) UploadKnowledgeDocumentChunk(ctx context.Context, libraryID string, in agentsdk.KnowledgeDocumentUploadChunk, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentUploadProgress, error) {
	var out agentsdk.KnowledgeDocumentUploadProgress
	if c == nil || c.client == nil || c.client.baseURL == "" || c.client.apiKey == "" {
		return out, fmt.Errorf("Agent SaaS is not configured")
	}
	if authority.RuntimeID != c.runtimeID {
		return out, &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.runtime_denied"}
	}
	if in.Content == nil || in.Bytes < 1 || in.Bytes > agentsdk.KnowledgeDocumentMaximumBytes || in.Offset < 0 || in.ChunkBytes < 1 || in.ChunkBytes > agentsdk.KnowledgeDocumentUploadChunkBytes || in.Offset+in.ChunkBytes > in.Bytes || !validKnowledgeDocumentSHA256(in.SHA256) || !validKnowledgeDocumentSHA256(in.ChunkSHA256) {
		return out, &agentsdk.Error{Class: "bad_request", Code: "agent.conversation.document_chunk_invalid"}
	}
	metadata, err := json.Marshal(knowledgeDocumentChunkUploadMetadata{LibraryID: libraryID, Chunk: in, Authority: authority})
	if err != nil {
		return out, err
	}
	ctx = telemetry.WithHTTPClientOperation(ctx, "agent.knowledge_document_chunk_upload")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.client.baseURL+"/agent/v1/conversations/documents_upload", in.Content)
	if err != nil {
		return out, err
	}
	request.ContentLength = in.ChunkBytes
	request.Header.Set("Authorization", "Bearer "+c.client.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(knowledgeDocumentMetadataHeader, base64.RawURLEncoding.EncodeToString(metadata))
	response, err := c.client.http.Do(request)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	err = readKnowledgeDocumentResponse(response, &out)
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
func (c *conversationClient) DownloadKnowledgeDocument(ctx context.Context, libraryID, id string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	document, err := c.KnowledgeDocument(ctx, libraryID, id, authority)
	if err != nil {
		return agentsdk.KnowledgeDocumentDownload{}, err
	}
	if document.Bytes < 1 || document.Bytes > agentsdk.KnowledgeDocumentMaximumBytes || !validKnowledgeDocumentSHA256(document.SHA256) {
		return agentsdk.KnowledgeDocumentDownload{}, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.document_content_mismatch"}
	}
	return agentsdk.KnowledgeDocumentDownload{Document: document, Content: &remoteKnowledgeDocumentReader{
		client: c.client, ctx: ctx, request: agentsdk.ConversationRPCRequest{Authority: authority, LibraryID: libraryID, DocumentID: id}, document: document,
	}}, nil
}
func (c *conversationClient) DeleteKnowledgeDocument(ctx context.Context, libraryID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_delete", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id, Revision: expected}, &out)
	return out, err
}
func (c *conversationClient) RetryKnowledgeDocument(ctx context.Context, libraryID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_retry", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id, Revision: expected}, &out)
	return out, err
}
func (c *conversationClient) ReindexKnowledgeDocument(ctx context.Context, libraryID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var out agentsdk.KnowledgeDocument
	err := c.call(ctx, "documents_reindex", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: libraryID, DocumentID: id, Revision: expected}, &out)
	return out, err
}

var _ agentsdk.KnowledgeDocumentService = (*conversationClient)(nil)
var _ agentsdk.KnowledgeDocumentChunkUploadService = (*conversationClient)(nil)
var _ agentsdk.KnowledgeDocumentRetryService = (*conversationClient)(nil)
var _ agentsdk.KnowledgeDocumentReindexService = (*conversationClient)(nil)

type remoteKnowledgeDocumentReader struct {
	client   *client
	ctx      context.Context
	request  agentsdk.ConversationRPCRequest
	document agentsdk.KnowledgeDocument
	offset   int64
	response *http.Response
	closed   bool
}

func (r *remoteKnowledgeDocumentReader) Read(output []byte) (int, error) {
	if r.closed {
		return 0, fmt.Errorf("knowledge document reader is closed")
	}
	if len(output) == 0 {
		return 0, nil
	}
	if r.offset >= r.document.Bytes {
		return 0, io.EOF
	}
	if r.response == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.response.Body.Read(output)
	r.offset += int64(n)
	if err == io.EOF {
		_ = r.response.Body.Close()
		r.response = nil
		if r.offset < r.document.Bytes {
			return n, io.ErrUnexpectedEOF
		}
	}
	return n, err
}

func (r *remoteKnowledgeDocumentReader) Seek(offset int64, whence int) (int64, error) {
	if r.closed {
		return 0, fmt.Errorf("knowledge document reader is closed")
	}
	next := offset
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		next = r.offset + offset
	case io.SeekEnd:
		next = r.document.Bytes + offset
	default:
		return 0, fmt.Errorf("invalid seek origin")
	}
	if next < 0 || next > r.document.Bytes {
		return 0, fmt.Errorf("invalid knowledge document offset")
	}
	if r.response != nil {
		_ = r.response.Body.Close()
		r.response = nil
	}
	r.offset = next
	return next, nil
}

func (r *remoteKnowledgeDocumentReader) Close() error {
	r.closed = true
	if r.response != nil {
		err := r.response.Body.Close()
		r.response = nil
		return err
	}
	return nil
}

func (r *remoteKnowledgeDocumentReader) open() error {
	raw, err := json.Marshal(r.request)
	if err != nil {
		return err
	}
	requestContext := telemetry.WithHTTPClientOperation(r.ctx, "agent.knowledge_document_download")
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, r.client.baseURL+"/agent/v1/conversations/documents_download", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+r.client.apiKey)
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Content-Type", "application/json")
	if r.offset > 0 {
		request.Header.Set("Range", "bytes="+strconv.FormatInt(r.offset, 10)+"-")
	}
	response, err := r.client.http.Do(request)
	if err != nil {
		return err
	}
	expectedStatus := http.StatusOK
	if r.offset > 0 {
		expectedStatus = http.StatusPartialContent
	}
	if response.StatusCode != expectedStatus {
		defer response.Body.Close()
		return readKnowledgeDocumentResponse(response, nil)
	}
	if response.Header.Get("X-Agent-File-SHA256") != r.document.SHA256 || response.ContentLength != r.document.Bytes-r.offset {
		_ = response.Body.Close()
		return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.document_content_mismatch"}
	}
	if r.offset > 0 && !strings.HasSuffix(response.Header.Get("Content-Range"), "/"+strconv.FormatInt(r.document.Bytes, 10)) {
		_ = response.Body.Close()
		return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.document_content_mismatch"}
	}
	r.response = response
	return nil
}

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
