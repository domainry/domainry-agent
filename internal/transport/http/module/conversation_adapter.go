package module

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type conversationAdapter struct {
	service   agentsdk.ConversationService
	runtimeID string
	mux       *http.ServeMux
	routes    []modulehttp.Route
}

func NewConversationAdapter(service agentsdk.ConversationService, runtimeID string) (modulehttp.Adapter, error) {
	if service == nil || runtimeID == "" {
		return nil, fmt.Errorf("conversation service and trusted runtime identity are required")
	}
	s := &conversationAdapter{service: service, runtimeID: runtimeID, mux: http.NewServeMux()}
	actions, err := agentsdk.AgentAuthorizationActions()
	if err != nil {
		return nil, err
	}
	for _, action := range actions {
		if !strings.HasPrefix(action.Key, agentsdk.ConversationActionPrefix) {
			continue
		}
		route, err := modulehttp.RouteFromAction(action)
		if err != nil {
			return nil, err
		}
		s.routes = append(s.routes, route)
		s.mux.HandleFunc(route.Pattern(), s.handle(strings.TrimPrefix(action.Key, agentsdk.ConversationActionPrefix)))
	}
	return s, nil
}
func (*conversationAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (*conversationAdapter) Owner() string           { return agentsdk.AgentHTTPAdapterOwner }
func (*conversationAdapter) Name() string            { return "conversations" }
func (s *conversationAdapter) Handler() http.Handler { return s.mux }
func (s *conversationAdapter) Routes() []modulehttp.Route {
	return append([]modulehttp.Route(nil), s.routes...)
}
func (*conversationAdapter) OpenAPIOperations() map[string]map[string]any {
	return agentsdk.ConversationOpenAPIOperations()
}
func (s *conversationAdapter) handle(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
		if !ok || !identity.Principal.Known {
			writeCode(w, 403, "agent.conversation.principal_required")
			return
		}
		principal := identity.Principal
		in := agentsdk.ConversationRPCRequest{Authority: agentsdk.ConversationAuthority{Known: principal.Known, RuntimeID: s.runtimeID, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey}, ConversationID: r.PathValue("conversationID"), RunID: r.PathValue("runID"), MemoryID: r.PathValue("memoryID")}
		q := r.URL.Query()
		if op == "libraries_sources" || op == "libraries_bind_source" {
			for key, values := range q {
				if op != "libraries_sources" || (key != "after" && key != "limit") || len(values) != 1 {
					writeCode(w, 400, "agent.conversation.datasource_query_invalid")
					return
				}
			}
		}
		in.TodoID = r.PathValue("todoID")
		in.ArtifactID = r.PathValue("artifactID")
		in.ArtifactExportID = r.PathValue("exportID")
		in.LibraryID = r.PathValue("libraryID")
		in.LibraryUserID = r.PathValue("userID")
		in.LibraryAfter = q.Get("after")
		in.DocumentID = r.PathValue("documentID")
		in.DocumentAfter = q.Get("after")
		in.AttachmentID = r.PathValue("attachmentID")
		in.AttachmentAfter = q.Get("after")
		var parseErr error
		number := func(name string) int64 {
			raw := q.Get(name)
			if raw == "" {
				return 0
			}
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				parseErr = fmt.Errorf("invalid %s", name)
			}
			return n
		}
		in.Revision = number("expected_revision")
		if op == "attachments_index" || op == "attachments_check_index" {
			if len(q) != 1 || len(q["expected_revision"]) != 1 || in.Revision < 1 {
				writeCode(w, 400, "agent.conversation.attachment_index_request_invalid")
				return
			}
			if r.Body != nil {
				data, err := io.ReadAll(io.LimitReader(r.Body, 1))
				if err != nil || len(data) != 0 {
					writeCode(w, 400, "agent.conversation.attachment_index_request_invalid")
					return
				}
			}
		}
		in.AfterSeq = number("after_seq")
		in.Limit = int(number("limit"))
		in.ArtifactVersion = number("version")
		in.ArtifactBefore = number("before")
		archived := false
		if raw := q.Get("include_archived"); raw != "" {
			var err error
			archived, err = strconv.ParseBool(raw)
			if err != nil {
				parseErr = err
			}
		}
		in.Query = agentsdk.ConversationQuery{Search: q.Get("search"), BeforeID: q.Get("before_id"), IncludeArchived: archived, Limit: in.Limit}
		in.TodoQuery = agentsdk.ConversationTodoQuery{Query: q.Get("query"), Status: q.Get("status"), SourceConversationID: q.Get("source_conversation_id"), BatchID: q.Get("batch_id"), Cursor: q.Get("cursor"), Limit: in.Limit}
		in.ArtifactQuery = agentsdk.ConversationArtifactQuery{Query: q.Get("query"), SourceConversationID: q.Get("source_conversation_id"), Cursor: q.Get("cursor"), Limit: in.Limit}
		in.Messages = agentsdk.ConversationMessageQuery{BeforeSeq: number("before_seq"), AfterSeq: in.AfterSeq, Limit: in.Limit}
		if op == "stream" && r.Header.Get("Last-Event-ID") != "" {
			var err error
			in.AfterSeq, err = strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
			if err != nil || in.AfterSeq < 0 {
				parseErr = fmt.Errorf("invalid event cursor")
			}
		}
		if parseErr != nil {
			writeCode(w, 400, "agent.conversation.query_invalid")
			return
		}
		var body any
		if op == "attachments_upload" || op == "documents_upload" {
			fileKind := "attachment"
			if op == "documents_upload" {
				fileKind = "document"
			}
			for key, values := range q {
				if (key != "client_id" && key != "filename") || len(values) != 1 {
					writeCode(w, 400, "agent.conversation.request_invalid")
					return
				}
			}
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/octet-stream" {
				writeCode(w, 400, "agent.conversation."+fileKind+"_type_unsupported")
				return
			}
			if r.ContentLength > agentsdk.ConversationAttachmentMaxBytes {
				writeCode(w, 413, "agent.conversation."+fileKind+"_size_invalid")
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentsdk.ConversationAttachmentMaxBytes))
			if err != nil {
				writeCode(w, 413, "agent.conversation."+fileKind+"_size_invalid")
				return
			}
			if op == "documents_upload" {
				in.DocumentUpload = agentsdk.KnowledgeDocumentUpload{ClientID: q.Get("client_id"), Filename: q.Get("filename"), Data: raw}
			} else {
				in.AttachmentUpload = agentsdk.ConversationAttachmentUpload{ClientID: q.Get("client_id"), Filename: q.Get("filename"), Data: raw}
			}
		}
		switch op {
		case "libraries_bind_source":
			body = &in.LibrarySourceWrite
		case "documents_transfer":
			body = &in.DocumentTransfer
		case "documents_import_attachment":
			body = &in.DocumentImport
		case "libraries_create":
			body = &in.LibraryCreate
		case "libraries_update":
			body = &in.LibraryUpdate
		case "libraries_set_member":
			body = &in.LibraryMemberWrite
		case "create":
			body = &in.Create
		case "update":
			body = &in.Update
		case "send":
			body = &in.Send
		case "respond":
			body = &in.Response
		case "memories_write":
			body = &in.Memory
		case "todos_create":
			body = &in.TodoCreate
		case "todos_update":
			body = &in.TodoUpdate
		case "todos_delete":
			body = &in.TodoDelete
		case "artifacts_create":
			body = &in.ArtifactCreate
		case "artifacts_edit":
			body = &in.ArtifactEdit
		case "artifacts_export":
			body = &in.ArtifactExport
		}
		if body != nil {
			limit := int64(65536)
			if op == "artifacts_create" || op == "artifacts_edit" {
				limit = 2 << 20 // Content is independently limited to 1 MiB.
			}
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
			dec.DisallowUnknownFields()
			if err := dec.Decode(body); err != nil {
				writeCode(w, 400, "agent.conversation.request_invalid")
				return
			}
			var extra any
			if dec.Decode(&extra) != io.EOF {
				writeCode(w, 400, "agent.conversation.request_invalid")
				return
			}
		}
		if op == "memories_write" {
			if in.Memory.ID != "" && in.Memory.ID != in.MemoryID {
				writeCode(w, 400, "agent.conversation.memory_invalid")
				return
			}
			in.Memory.ID = in.MemoryID
		}
		if op == "stream" {
			s.stream(w, r, in)
			return
		}
		result, err := agentapplication.InvokeConversation(r.Context(), s.service, op, in)
		if err != nil {
			writeError(w, err)
			return
		}
		if op == "artifacts_download" {
			download, ok := result.(agentsdk.ConversationArtifactDownload)
			if !ok {
				writeCode(w, 500, "agent.conversation.artifact_download_invalid")
				return
			}
			w.Header().Set("Content-Type", download.Export.ContentType)
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Export.Filename}))
			w.Header().Set("Content-Length", strconv.Itoa(len(download.Data)))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(download.Data)
			return
		}
		if op == "attachments_download" {
			download, ok := result.(agentsdk.ConversationAttachmentDownload)
			if !ok {
				writeCode(w, 500, "agent.conversation.attachment_download_invalid")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Attachment.Filename}))
			w.Header().Set("X-Agent-File-SHA256", download.Attachment.SHA256)
			w.Header().Set("Content-Length", strconv.Itoa(len(download.Data)))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(download.Data)
			return
		}
		if op == "documents_download" {
			download, ok := result.(agentsdk.KnowledgeDocumentDownload)
			if !ok {
				writeCode(w, 500, "agent.conversation.document_download_invalid")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Document.Filename}))
			w.Header().Set("X-Agent-File-SHA256", download.Document.SHA256)
			w.Header().Set("Content-Length", strconv.Itoa(len(download.Data)))
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "private, no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(download.Data)
			return
		}
		status := 200
		if op == "send" {
			status = 202
		}
		writeJSON(w, status, result)
	}
}
func (s *conversationAdapter) stream(w http.ResponseWriter, r *http.Request, in agentsdk.ConversationRPCRequest) {
	// Authorize and read before committing headers. SSE is a read-only durable
	// event projection: disconnecting never starts, cancels or repeats a run.
	page, err := s.service.Events(r.Context(), in.ConversationID, in.RunID, in.AfterSeq, 100, in.Authority)
	if err != nil {
		writeError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeCode(w, 503, "agent.conversation.stream_unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, event := range page.Items {
			raw, _ := json.Marshal(event)
			if _, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, raw); err != nil {
				return
			}
			in.AfterSeq = event.Seq
		}
		if len(page.Items) == 0 {
			if _, err = fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
		}
		flusher.Flush()
		if page.Terminal {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
		page, err = s.service.Events(r.Context(), in.ConversationID, in.RunID, in.AfterSeq, 100, in.Authority)
		if err != nil {
			_, _ = fmt.Fprint(w, "event: stream.error\ndata: {\"code\":\"agent.conversation.stream_interrupted\"}\n\n")
			flusher.Flush()
			return
		}
	}
}

var _ modulehttp.Adapter = (*conversationAdapter)(nil)
var _ modulehttp.OpenAPIProvider = (*conversationAdapter)(nil)
