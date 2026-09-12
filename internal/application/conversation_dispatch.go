package application

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Shared transport dispatch; identity is established by the owning transport.
func InvokeConversation(ctx context.Context, s agentsdk.ConversationService, op string, r agentsdk.ConversationRPCRequest) (any, error) {
	a := r.Authority
	if op == "libraries_sources" || op == "libraries_bind_source" {
		sources, ok := s.(agentsdk.KnowledgeDatasourceService)
		if !ok {
			return nil, conversationFailure("unavailable", "datasources_unavailable")
		}
		if op == "libraries_sources" {
			return sources.KnowledgeLibrarySources(ctx, r.LibraryID, r.LibraryAfter, r.Limit, a)
		}
		return sources.BindKnowledgeLibrarySource(ctx, r.LibraryID, r.LibrarySourceWrite, a)
	}
	if op == "documents_transfer" {
		transfer, ok := s.(agentsdk.KnowledgeDocumentTransferService)
		if !ok {
			return nil, conversationFailure("unavailable", "document_transfer_unavailable")
		}
		return transfer.TransferKnowledgeDocument(ctx, r.LibraryID, r.DocumentTransfer, a)
	}
	if op == "documents_import_attachment" {
		importer, ok := s.(agentsdk.KnowledgeAttachmentImportService)
		if !ok {
			return nil, conversationFailure("unavailable", "document_import_unavailable")
		}
		return importer.ImportConversationAttachment(ctx, r.LibraryID, r.DocumentImport, a)
	}
	if strings.HasPrefix(op, "libraries_") {
		libraries, ok := s.(agentsdk.KnowledgeLibraryService)
		if !ok {
			return nil, conversationFailure("unavailable", "libraries_unavailable")
		}
		switch op {
		case "libraries_create":
			return libraries.CreateKnowledgeLibrary(ctx, r.LibraryCreate, a)
		case "libraries_list":
			return libraries.KnowledgeLibraries(ctx, r.LibraryAfter, r.Limit, a)
		case "libraries_get":
			return libraries.KnowledgeLibrary(ctx, r.LibraryID, a)
		case "libraries_update":
			return libraries.UpdateKnowledgeLibrary(ctx, r.LibraryID, r.LibraryUpdate, a)
		case "libraries_members":
			return libraries.KnowledgeLibraryMembers(ctx, r.LibraryID, r.LibraryAfter, r.Limit, a)
		case "libraries_set_member":
			return libraries.SetKnowledgeLibraryMember(ctx, r.LibraryID, r.LibraryUserID, r.LibraryMemberWrite, a)
		case "libraries_remove_member":
			return libraries.RemoveKnowledgeLibraryMember(ctx, r.LibraryID, r.LibraryUserID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "documents_") {
		documents, ok := s.(agentsdk.KnowledgeDocumentService)
		if !ok {
			return nil, conversationFailure("unavailable", "documents_unavailable")
		}
		switch op {
		case "documents_upload":
			return documents.UploadKnowledgeDocument(ctx, r.LibraryID, r.DocumentUpload, a)
		case "documents_list":
			return documents.KnowledgeDocuments(ctx, r.LibraryID, r.DocumentAfter, r.Limit, a)
		case "documents_get":
			return documents.KnowledgeDocument(ctx, r.LibraryID, r.DocumentID, a)
		case "documents_download":
			return documents.DownloadKnowledgeDocument(ctx, r.LibraryID, r.DocumentID, a)
		case "documents_delete":
			return documents.DeleteKnowledgeDocument(ctx, r.LibraryID, r.DocumentID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "attachments_") {
		attachments, ok := s.(agentsdk.ConversationAttachmentService)
		if !ok {
			return nil, conversationFailure("unavailable", "attachments_unavailable")
		}
		switch op {
		case "attachments_upload":
			return attachments.UploadAttachment(ctx, r.ConversationID, r.AttachmentUpload, a)
		case "attachments_index":
			indexing, ok := s.(agentsdk.ConversationAttachmentIndexService)
			if !ok {
				return nil, conversationFailure("unavailable", "attachment_index_unavailable")
			}
			return indexing.IndexAttachment(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		case "attachments_check_index":
			checking, ok := s.(agentsdk.ConversationAttachmentIndexCheckService)
			if !ok {
				return nil, conversationFailure("unavailable", "attachment_index_unavailable")
			}
			return checking.CheckAttachmentIndex(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		case "attachments_list":
			return attachments.Attachments(ctx, r.ConversationID, r.AttachmentAfter, r.Limit, a)
		case "attachments_get":
			return attachments.Attachment(ctx, r.ConversationID, r.AttachmentID, a)
		case "attachments_download":
			return attachments.DownloadAttachment(ctx, r.ConversationID, r.AttachmentID, a)
		case "attachments_delete":
			return attachments.DeleteAttachment(ctx, r.ConversationID, r.AttachmentID, r.Revision, a)
		}
	}
	if strings.HasPrefix(op, "artifacts_") {
		artifacts, ok := s.(agentsdk.ConversationArtifactService)
		if !ok {
			return nil, conversationFailure("unavailable", "artifacts_unavailable")
		}
		switch op {
		case "artifacts_list":
			return artifacts.Artifacts(ctx, r.ArtifactQuery, a)
		case "artifacts_get":
			return artifacts.Artifact(ctx, r.ArtifactID, r.ArtifactVersion, a)
		case "artifacts_versions":
			return artifacts.ArtifactVersions(ctx, r.ArtifactID, r.ArtifactBefore, r.Limit, a)
		case "artifacts_create":
			return artifacts.CreateArtifact(ctx, r.ArtifactCreate, a)
		case "artifacts_edit":
			return artifacts.EditArtifact(ctx, r.ArtifactID, r.ArtifactEdit, a)
		case "artifacts_export":
			return artifacts.ExportArtifact(ctx, r.ArtifactID, r.ArtifactExport, a)
		case "artifacts_download":
			return artifacts.DownloadArtifact(ctx, r.ArtifactExportID, a)
		}
	}
	if strings.HasPrefix(op, "todos_") {
		todos, ok := s.(agentsdk.ConversationTodoService)
		if !ok {
			return nil, conversationFailure("unavailable", "todos_unavailable")
		}
		switch op {
		case "todos_list":
			return todos.Todos(ctx, r.TodoQuery, a)
		case "todos_get":
			return todos.Todo(ctx, r.TodoID, a)
		case "todos_create":
			return todos.CreateTodos(ctx, r.TodoCreate, a)
		case "todos_update":
			return todos.UpdateTodo(ctx, r.TodoID, r.TodoUpdate, a)
		case "todos_delete":
			err := todos.DeleteTodo(ctx, r.TodoID, r.TodoDelete, a)
			return map[string]bool{"deleted": err == nil}, err
		}
	}
	switch op {
	case "create":
		return s.Create(ctx, r.Create, a)
	case "list":
		return s.List(ctx, r.Query, a)
	case "get":
		return s.Get(ctx, r.ConversationID, a)
	case "update":
		return s.Update(ctx, r.ConversationID, r.Update, a)
	case "delete":
		err := s.Delete(ctx, r.ConversationID, r.Revision, a)
		return map[string]bool{"deleted": err == nil}, err
	case "send":
		return s.Send(ctx, r.ConversationID, r.Send, a)
	case "messages":
		return s.Messages(ctx, r.ConversationID, r.Messages, a)
	case "run":
		return s.Run(ctx, r.ConversationID, r.RunID, a)
	case "events":
		return s.Events(ctx, r.ConversationID, r.RunID, r.AfterSeq, r.Limit, a)
	case "cancel":
		return s.Cancel(ctx, r.ConversationID, r.RunID, a)
	case "resume":
		return s.Resume(ctx, r.ConversationID, r.RunID, a)
	case "respond":
		if interactions, ok := s.(agentsdk.ConversationInteractionService); ok {
			return interactions.Respond(ctx, r.ConversationID, r.RunID, r.Response, a)
		}
		return nil, conversationFailure("unavailable", "interaction_unavailable")
	case "memories_list":
		return s.Memories(ctx, a)
	case "memories_write":
		return s.WriteMemory(ctx, r.Memory, a)
	case "memories_delete":
		err := s.DeleteMemory(ctx, r.MemoryID, r.Revision, a)
		return map[string]bool{"deleted": err == nil}, err
	default:
		return nil, conversationFailure("not_found", "operation_not_found")
	}
}
