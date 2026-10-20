package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
)

const conversationSaaSActionPrefix = "agent.saas.conversation."

func (s *Server) conversationHandler(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.config.Conversations == nil || s.config.ConversationRuntimeID == "" {
			writeError(w, 503, "agent.conversation.unavailable", "")
			return
		}
		if op == "documents_upload" {
			s.uploadKnowledgeDocument(w, r)
			return
		}
		var in agentsdk.ConversationRPCRequest
		limit := int64(maxRequestBytes)
		if op == "attachments_upload" {
			limit = 24 << 20
		}
		if err := decodeLimit(r, &in, limit); err != nil {
			writeError(w, 400, "agent.conversation.request_invalid", "")
			return
		}
		// This service API key belongs to one runtime. A client cannot choose another
		// runtime's namespace by forging the invocation envelope.
		if in.Authority.RuntimeID != s.config.ConversationRuntimeID {
			writeError(w, 403, "agent.conversation.runtime_denied", "")
			return
		}
		if s.config.ConversationWorkspaceID != "" && in.Authority.WorkspaceID != s.config.ConversationWorkspaceID {
			writeError(w, 403, "agent.conversation.workspace_denied", "")
			return
		}
		if op == "scheduled_task_start" && in.ScheduledTask.Authority != in.Authority {
			writeError(w, 403, "agent.conversation.scheduled_authority_denied", "")
			return
		}
		if op == "business_event_task_accept" && in.BusinessEventTask.Authority != in.Authority {
			writeError(w, 403, "agent.conversation.business_event_authority_denied", "")
			return
		}
		if op == "documents_download" {
			s.downloadKnowledgeDocument(w, r, in)
			return
		}
		result, err := agentapplication.InvokeConversation(r.Context(), s.config.Conversations, op, in)
		if err != nil {
			writeConversationServiceError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}
}

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

func (s *Server) uploadKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	documents, ok := s.config.Conversations.(agentsdk.KnowledgeDocumentService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent.conversation.documents_unavailable", "")
		return
	}
	encoded := strings.TrimSpace(r.Header.Get("X-Agent-Knowledge-Document-Metadata"))
	if encoded == "" || len(encoded) > 16<<10 {
		writeError(w, http.StatusBadRequest, "agent.conversation.request_invalid", "")
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		writeError(w, http.StatusBadRequest, "agent.conversation.request_invalid", "")
		return
	}
	var envelope struct {
		Upload json.RawMessage `json:"upload"`
		Chunk  json.RawMessage `json:"chunk"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil || (len(envelope.Upload) == 0) == (len(envelope.Chunk) == 0) {
		writeError(w, http.StatusBadRequest, "agent.conversation.request_invalid", "")
		return
	}
	if len(envelope.Chunk) > 0 {
		chunks, supported := s.config.Conversations.(agentsdk.KnowledgeDocumentChunkUploadService)
		if !supported {
			writeError(w, http.StatusServiceUnavailable, "agent.conversation.document_chunk_upload_unavailable", "")
			return
		}
		var metadata knowledgeDocumentChunkUploadMetadata
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&metadata) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			writeError(w, http.StatusBadRequest, "agent.conversation.request_invalid", "")
			return
		}
		if metadata.Authority.RuntimeID != s.config.ConversationRuntimeID {
			writeError(w, http.StatusForbidden, "agent.conversation.runtime_denied", "")
			return
		}
		if s.config.ConversationWorkspaceID != "" && metadata.Authority.WorkspaceID != s.config.ConversationWorkspaceID {
			writeError(w, http.StatusForbidden, "agent.conversation.workspace_denied", "")
			return
		}
		mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaErr != nil || mediaType != "application/octet-stream" || metadata.Chunk.Content != nil || metadata.Chunk.ChunkBytes < 1 || metadata.Chunk.ChunkBytes > agentsdk.KnowledgeDocumentUploadChunkBytes || r.ContentLength != metadata.Chunk.ChunkBytes {
			writeError(w, http.StatusBadRequest, "agent.conversation.document_size_invalid", "")
			return
		}
		metadata.Chunk.Content = http.MaxBytesReader(w, r.Body, agentsdk.KnowledgeDocumentUploadChunkBytes)
		progress, uploadErr := chunks.UploadKnowledgeDocumentChunk(r.Context(), metadata.LibraryID, metadata.Chunk, metadata.Authority)
		if uploadErr != nil {
			writeConversationServiceError(w, uploadErr)
			return
		}
		writeJSON(w, http.StatusOK, progress)
		return
	}
	var metadata knowledgeDocumentUploadMetadata
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&metadata) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "agent.conversation.request_invalid", "")
		return
	}
	if metadata.Authority.RuntimeID != s.config.ConversationRuntimeID {
		writeError(w, http.StatusForbidden, "agent.conversation.runtime_denied", "")
		return
	}
	if s.config.ConversationWorkspaceID != "" && metadata.Authority.WorkspaceID != s.config.ConversationWorkspaceID {
		writeError(w, http.StatusForbidden, "agent.conversation.workspace_denied", "")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/octet-stream" || metadata.Upload.Content != nil || metadata.Upload.Bytes < 1 || metadata.Upload.Bytes > agentsdk.KnowledgeDocumentMaximumBytes || r.ContentLength != metadata.Upload.Bytes {
		writeError(w, http.StatusBadRequest, "agent.conversation.document_size_invalid", "")
		return
	}
	metadata.Upload.Content = http.MaxBytesReader(w, r.Body, agentsdk.KnowledgeDocumentMaximumBytes)
	document, err := documents.UploadKnowledgeDocument(r.Context(), metadata.LibraryID, metadata.Upload, metadata.Authority)
	if err != nil {
		writeConversationServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, document)
}

func (s *Server) downloadKnowledgeDocument(w http.ResponseWriter, r *http.Request, in agentsdk.ConversationRPCRequest) {
	documents, ok := s.config.Conversations.(agentsdk.KnowledgeDocumentService)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "agent.conversation.documents_unavailable", "")
		return
	}
	download, err := documents.DownloadKnowledgeDocument(r.Context(), in.LibraryID, in.DocumentID, in.Authority)
	if err != nil {
		writeConversationServiceError(w, err)
		return
	}
	defer download.Content.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Document.Filename}))
	w.Header().Set("X-Agent-File-SHA256", download.Document.SHA256)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, download.Document.Filename, time.Time{}, download.Content)
}

func writeConversationServiceError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "agent.conversation.internal"
	var coded *agentsdk.Error
	if errors.As(err, &coded) {
		code = coded.Code
		switch coded.Class {
		case "bad_request":
			status = http.StatusBadRequest
		case "forbidden":
			status = http.StatusForbidden
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "rate_limited":
			status = http.StatusTooManyRequests
		case "unavailable":
			status = http.StatusServiceUnavailable
		}
	}
	writeError(w, status, code, "")
}
