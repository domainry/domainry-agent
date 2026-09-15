package agentsdk

import (
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
)

const AgentCapabilityCollaboration = "agent.collaboration"
const AgentCapabilitySourcePublication = "agent.source_publication"
const AgentCapabilityConversation = "agent.conversations"
const AgentCapabilityBackgroundTasks = "agent.background_tasks"
const AgentCapabilityPersonalTodos = "agent.personal_todos"
const AgentCapabilityArtifacts = "agent.artifacts"
const AgentCapabilityKnowledgeLibraries = "agent.knowledge_libraries"
const AgentCapabilityAttachments = "agent.conversation_attachments"
const ConversationActionPrefix = "agent.conversations."

type ConversationHTTPDefinition struct {
	Operation, Pattern string
	Input, Output      any
	Query              []string
}

func ConversationHTTPDefinitions() []ConversationHTTPDefinition {
	return []ConversationHTTPDefinition{
		{"agents_access", "GET /agent/collaboration-access", nil, ConversationCollaborationAuthorization{}, nil},
		{"agents_list", "GET /agent/agents", nil, ConversationAgentPage{}, nil},
		{"agents_match", "POST /agent/agents/matches", ConversationAgentMatchRequest{}, ConversationAgentMatchPage{}, nil},
		{"agents_create", "POST /agent/agents", ConversationAgentWrite{}, ConversationAgent{}, nil},
		{"agents_update", "PUT /agent/agents/{agentID}", ConversationAgentWrite{}, ConversationAgent{}, nil},
		{"delegations_list", "GET /agent/delegations", nil, ConversationDelegationPage{}, []string{"source_conversation_id"}},
		{"delegations_create", "POST /agent/delegations", ConversationDelegationCreate{}, ConversationDelegationDetail{}, nil},
		{"delegations_get", "GET /agent/delegations/{delegationID}", nil, ConversationDelegationDetail{}, nil},
		{"delegations_history", "GET /agent/delegations/{delegationID}/requirements", nil, ConversationAgreementHistory{}, []string{"before_revision"}},
		{"delegations_disagreement", "GET /agent/delegations/{delegationID}/disagreements/{disagreementID}", nil, ConversationDisagreementHistory{}, []string{"before_revision"}},
		{"delegations_deliveries", "GET /agent/delegations/{delegationID}/deliveries", nil, ConversationDeliveryHistory{}, []string{"before_revision"}},
		{"delegations_result", "POST /agent/delegations/{delegationID}/delivery-result", ConversationDeliveryResultRead{}, ConversationResultSlice{}, nil},
		{"delegations_execution_share", "POST /agent/delegations/{delegationID}/execution-publications", ConversationExecutionShare{}, ConversationExecutionPublication{}, nil},
		{"delegations_execution_publications", "GET /agent/delegations/{delegationID}/execution-publications", nil, []ConversationExecutionPublication{}, nil},
		{"delegations_executions", "GET /agent/delegations/{delegationID}/executions", nil, []ConversationExecutionPublication{}, nil},
		{"delegations_execution", "POST /agent/delegations/{delegationID}/execution", ConversationRunReference{}, ConversationRun{}, nil},
		{"delegations_execution_result", "POST /agent/delegations/{delegationID}/execution-result", ConversationResultRead{}, ConversationResultSlice{}, nil},
		{"delegations_publication", "POST /agent/delegations/{delegationID}/delivery-publication", ConversationDeliveryPublicationRequest{}, ConversationDeliveryPublicationPreview{}, nil},
		{"delegations_publications", "GET /agent/delegations/{delegationID}/delivery-publications", nil, ConversationDeliveryPublicationCandidates{}, []string{"before_revision"}},
		{"delegations_contract_publication", "POST /agent/delegations/{delegationID}/contract-publication", ConversationContractPublicationRequest{}, ConversationContractPublicationPreview{}, nil},
		{"delegations_contract_candidates", "GET /agent/delegations/{delegationID}/contract-candidates", nil, ConversationContractPublicationCandidates{}, []string{"before_revision"}},
		{"delegations_contract_publications", "GET /agent/delegations/{delegationID}/contract-publications", nil, ConversationContractPublicationHistory{}, []string{"before_revision"}},
		{"delegations_artifact", "POST /agent/delegations/{delegationID}/delivery-artifact", ConversationDeliveryArtifactRead{}, ConversationArtifactVersion{}, nil},
		{"delegations_export", "POST /agent/delegations/{delegationID}/delivery-export", ConversationDeliveryArtifactRead{}, ConversationArtifactDownload{}, nil},
		{"delegations_update", "POST /agent/delegations/{delegationID}/decisions", ConversationDelegationUpdate{}, ConversationDelegationDetail{}, nil},
		{"delegations_message", "POST /agent/delegations/{delegationID}/messages", ConversationAgentMessageSend{}, ConversationAgentMessage{}, nil},
		{"create", "POST /agent/conversations", ConversationCreate{}, Conversation{}, nil},
		{"list", "GET /agent/conversations", nil, ConversationPage{}, []string{"search", "include_archived", "before_id", "limit"}},
		{"get", "GET /agent/conversations/{conversationID}", nil, Conversation{}, nil},
		{"update", "PATCH /agent/conversations/{conversationID}", ConversationUpdate{}, Conversation{}, nil},
		{"delete", "DELETE /agent/conversations/{conversationID}", nil, map[string]bool{}, []string{"expected_revision"}},
		{"send", "POST /agent/conversations/{conversationID}/messages", ConversationSend{}, ConversationRun{}, nil},
		{"messages", "GET /agent/conversations/{conversationID}/messages", nil, ConversationMessagePage{}, []string{"before_seq", "after_seq", "limit"}},
		{"run", "GET /agent/conversations/{conversationID}/runs/{runID}", nil, ConversationRun{}, nil},
		{"result_read", "POST /agent/conversations/{conversationID}/runs/{runID}/result", ConversationResultRead{}, ConversationResultSlice{}, nil},
		{"events", "GET /agent/conversations/{conversationID}/runs/{runID}/events", nil, ConversationEventPage{}, []string{"after_seq", "limit"}},
		{"stream", "GET /agent/conversations/{conversationID}/runs/{runID}/events/stream", nil, ConversationEvent{}, []string{"after_seq"}},
		{"cancel", "POST /agent/conversations/{conversationID}/runs/{runID}/cancel", nil, ConversationRun{}, nil},
		{"resume", "POST /agent/conversations/{conversationID}/runs/{runID}/resume", nil, ConversationRun{}, nil},
		{"respond", "POST /agent/conversations/{conversationID}/runs/{runID}/respond", ConversationInteractionResponse{}, ConversationRun{}, nil},
		{"memories_list", "GET /agent/conversations/memories", nil, []ConversationMemory{}, nil},
		{"memories_write", "PUT /agent/conversations/memories/{memoryID}", ConversationMemoryWrite{}, ConversationMemory{}, nil},
		{"memories_delete", "DELETE /agent/conversations/memories/{memoryID}", nil, map[string]bool{}, []string{"expected_revision"}},
		{"libraries_create", "POST /agent/knowledge-libraries", KnowledgeLibraryCreate{}, KnowledgeLibrary{}, nil},
		{"libraries_list", "GET /agent/knowledge-libraries", nil, KnowledgeLibraryPage{}, []string{"after", "limit"}},
		{"libraries_get", "GET /agent/knowledge-libraries/{libraryID}", nil, KnowledgeLibrary{}, nil},
		{"libraries_sources", "GET /agent/knowledge-libraries/{libraryID}/sources", nil, KnowledgeLibrarySources{}, []string{"after", "limit"}},
		{"libraries_bind_source", "PUT /agent/knowledge-libraries/{libraryID}/source", KnowledgeLibrarySourceWrite{}, KnowledgeLibrary{}, nil},
		{"libraries_update", "PATCH /agent/knowledge-libraries/{libraryID}", KnowledgeLibraryUpdate{}, KnowledgeLibrary{}, nil},
		{"libraries_members", "GET /agent/knowledge-libraries/{libraryID}/members", nil, KnowledgeLibraryMembers{}, []string{"after", "limit"}},
		{"libraries_set_member", "PUT /agent/knowledge-libraries/{libraryID}/members/{userID}", KnowledgeLibraryMemberWrite{}, KnowledgeLibrary{}, nil},
		{"libraries_remove_member", "DELETE /agent/knowledge-libraries/{libraryID}/members/{userID}", nil, KnowledgeLibrary{}, []string{"expected_revision"}},
		{"documents_transfer", "POST /agent/knowledge-libraries/{libraryID}/documents/from-document", KnowledgeDocumentTransfer{}, KnowledgeDocument{}, nil},
		{"documents_import_attachment", "POST /agent/knowledge-libraries/{libraryID}/documents/from-attachment", KnowledgeAttachmentImport{}, KnowledgeDocument{}, nil},
		{"documents_list", "GET /agent/knowledge-libraries/{libraryID}/documents", nil, KnowledgeDocumentPage{}, []string{"after", "limit"}},
		{"documents_upload", "POST /agent/knowledge-libraries/{libraryID}/documents", KnowledgeDocumentUpload{}, KnowledgeDocument{}, []string{"client_id", "filename"}},
		{"documents_get", "GET /agent/knowledge-libraries/{libraryID}/documents/{documentID}", nil, KnowledgeDocument{}, nil},
		{"documents_download", "GET /agent/knowledge-libraries/{libraryID}/documents/{documentID}/content", nil, KnowledgeDocumentDownload{}, nil},
		{"documents_delete", "DELETE /agent/knowledge-libraries/{libraryID}/documents/{documentID}", nil, KnowledgeDocument{}, []string{"expected_revision"}},
		{"attachments_list", "GET /agent/conversations/{conversationID}/attachments", nil, ConversationAttachmentPage{}, []string{"after", "limit"}},
		{"attachments_upload", "POST /agent/conversations/{conversationID}/attachments", ConversationAttachmentUpload{}, ConversationAttachment{}, []string{"client_id", "filename"}},
		{"attachments_get", "GET /agent/conversations/{conversationID}/attachments/{attachmentID}", nil, ConversationAttachment{}, nil},
		{"attachments_download", "GET /agent/conversations/{conversationID}/attachments/{attachmentID}/content", nil, ConversationAttachmentDownload{}, nil},
		{"attachments_index", "POST /agent/conversations/{conversationID}/attachments/{attachmentID}/index", nil, ConversationAttachment{}, []string{"expected_revision"}},
		{"attachments_check_index", "POST /agent/conversations/{conversationID}/attachments/{attachmentID}/index/check", nil, ConversationAttachment{}, []string{"expected_revision"}},
		{"attachments_delete", "DELETE /agent/conversations/{conversationID}/attachments/{attachmentID}", nil, ConversationAttachment{}, []string{"expected_revision"}},
		{"todos_list", "GET /agent/todos", nil, ConversationTodoPage{}, []string{"query", "status", "source_conversation_id", "batch_id", "cursor", "limit"}},
		{"todos_get", "GET /agent/todos/{todoID}", nil, ConversationTodo{}, nil},
		{"todos_create", "POST /agent/todos", ConversationTodoCreate{}, ConversationTodoBatch{}, nil},
		{"todos_update", "PATCH /agent/todos/{todoID}", ConversationTodoUpdate{}, ConversationTodo{}, nil},
		{"todos_delete", "DELETE /agent/todos/{todoID}", ConversationTodoDelete{}, map[string]bool{}, nil},
		{"tasks_list", "GET /agent/conversation-tasks", nil, ConversationTaskPage{}, []string{"query", "status", "source_conversation_id", "cursor", "limit"}},
		{"tasks_get", "GET /agent/conversation-tasks/{taskID}", nil, ConversationTaskDetail{}, nil},
		{"tasks_plans", "GET /agent/conversation-tasks/{taskID}/plans", nil, ConversationPlanHistory{}, []string{"before_version"}},
		{"tasks_completions", "GET /agent/conversation-tasks/{taskID}/completions", nil, ConversationTaskCompletionHistory{}, []string{"before_revision"}},
		{"tasks_completion_review", "POST /agent/conversation-tasks/{taskID}/completion-review", ConversationTaskCompletionReviewRequest{}, ConversationTaskDetail{}, nil},
		{"tasks_update", "PATCH /agent/conversation-tasks/{taskID}/agreement", ConversationTaskAgreementUpdate{}, ConversationTaskDetail{}, nil},
		{"tasks_cancel", "POST /agent/conversation-tasks/{taskID}/cancel", nil, ConversationTaskDetail{}, nil},
		{"tasks_resume", "POST /agent/conversation-tasks/{taskID}/resume", nil, ConversationTaskDetail{}, nil},
		{"artifacts_list", "GET /agent/artifacts", nil, ConversationArtifactPage{}, []string{"query", "source_conversation_id", "cursor", "limit"}},
		{"artifacts_get", "GET /agent/artifacts/{artifactID}", nil, ConversationArtifactVersion{}, []string{"version"}},
		{"artifacts_versions", "GET /agent/artifacts/{artifactID}/versions", nil, ConversationArtifactVersions{}, []string{"before", "limit"}},
		{"artifacts_create", "POST /agent/artifacts", ConversationArtifactCreate{}, ConversationArtifactVersion{}, nil},
		{"artifacts_edit", "PATCH /agent/artifacts/{artifactID}", ConversationArtifactEdit{}, ConversationArtifactVersion{}, nil},
		{"artifacts_export", "POST /agent/artifacts/{artifactID}/exports", ConversationArtifactExportRequest{}, ConversationArtifactExport{}, nil},
		{"artifacts_download", "GET /agent/artifact-exports/{exportID}/download", nil, ConversationArtifactDownload{}, nil},
	}
}
func conversationActions() []actioncontract.ActionDefinition {
	out := []actioncontract.ActionDefinition{}
	for _, d := range ConversationHTTPDefinitions() {
		effect, idempotency, audit := actioncontract.EffectWrite, "request_contract", "mutation_audit_required"
		if strings.HasPrefix(d.Pattern, "GET ") || d.Operation == "delegations_execution" || d.Operation == "delegations_execution_result" || d.Operation == "result_read" || d.Operation == "delegations_result" || d.Operation == "delegations_publication" || d.Operation == "delegations_contract_publication" || d.Operation == "delegations_artifact" || d.Operation == "delegations_export" || d.Operation == "agents_match" {
			effect, idempotency, audit = actioncontract.EffectRead, "not_applicable", "owner_read_audit_policy"
		}
		capability, label := AgentCapabilityConversation, "Persistent personal conversations"
		if strings.HasPrefix(d.Operation, "agents_") || strings.HasPrefix(d.Operation, "delegations_") {
			capability, label = AgentCapabilityCollaboration, "Peer Agent collaboration"
		}
		if strings.HasPrefix(d.Operation, "delegations_contract_") {
			capability, label = AgentCapabilitySourcePublication, "Delegation source publication"
		}
		if strings.HasPrefix(d.Operation, "delegations_execution") {
			capability, label = AgentCapabilitySourcePublication, "Delegation source publication"
		}
		if strings.HasPrefix(d.Operation, "todos_") {
			capability, label = AgentCapabilityPersonalTodos, "Personal todos"
		}
		if strings.HasPrefix(d.Operation, "tasks_") {
			capability, label = AgentCapabilityBackgroundTasks, "Durable background tasks"
		}
		if strings.HasPrefix(d.Operation, "artifacts_") {
			capability, label = AgentCapabilityArtifacts, "Saved artifacts"
		}
		if strings.HasPrefix(d.Operation, "attachments_") {
			capability, label = AgentCapabilityAttachments, "Private conversation attachments"
		}
		if strings.HasPrefix(d.Operation, "libraries_") || strings.HasPrefix(d.Operation, "documents_") {
			capability, label = AgentCapabilityKnowledgeLibraries, "Knowledge libraries"
		}
		action := agentPrincipalAction(ConversationActionPrefix+d.Operation, capability, label, d.Operation, d.Pattern, effect, idempotency, audit)
		// Todo HTTP commands are explicit user requests; the service authorizes
		// their registered personal tool permission before accessing storage.

		if d.Operation == "respond" {
			action.Permission = ConversationInteractionPermission()
		}
		if permission := KnowledgeLibraryPermission(d.Operation); permission != nil {
			action.Permission = permission
		}
		if permission := KnowledgeDocumentPermission(d.Operation); permission != nil {
			action.Permission = permission
		}
		if permission := ConversationAttachmentPermission(d.Operation); permission != nil {
			action.Permission = permission
		}
		out = append(out, action)
	}
	return out
}
func ConversationOpenAPIOperations() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, d := range ConversationHTTPDefinitions() {
		status := "200"
		if d.Operation == "send" {
			status = "202"
		}
		content := map[string]any{"application/json": map[string]any{"schema": modulecapability.JSONSchemaForGoValue(d.Output)}}
		if d.Operation == "stream" {
			content = map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}
		}
		if d.Operation == "artifacts_download" || d.Operation == "delegations_export" {
			binary := map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}
			content = map[string]any{"text/markdown": binary, "text/csv": binary}
		}
		if d.Operation == "attachments_download" || d.Operation == "documents_download" {
			content = map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
		}
		responses := map[string]any{status: map[string]any{"description": "Success", "content": content}}
		for _, code := range []string{"400", "403", "404", "409", "503"} {
			responses[code] = map[string]any{"description": "Conversation request error", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"code": map[string]any{"type": "string"}}, "required": []string{"code"}}}}}
		}
		op := map[string]any{"operationId": ConversationActionPrefix + d.Operation, "summary": d.Operation, "tags": []string{"Agent conversations"}, "responses": responses}
		if d.Input != nil {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": modulecapability.JSONSchemaForGoValue(d.Input)}}}
		}
		if d.Operation == "attachments_upload" || d.Operation == "documents_upload" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary", "maxLength": ConversationAttachmentMaxBytes}}}}
			op["description"] = "Upload a private original file, maximum 16 MiB. client_id makes identical retries idempotent. Filename must include an allowed document extension. Stored does not mean indexed; private retrieval must be separately verified. No server path, owner or permission IDs are accepted."
			responses["413"] = map[string]any{"description": "Attachment exceeds upload limit"}
		}
		if d.Operation == "documents_upload" {
			op["description"] = "Upload an immutable original into the selected library, maximum 16 MiB. Requires editor or manager membership, current Identity permission and an explicitly managed dedicated remote KB. All readers of the library can access it. Queued or indexing is not ready; deletion immediately revokes local retrieval and reports pending remote cleanup. No server path, remote KB, owner or ACL is accepted."
		}
		params := []any{}
		for _, part := range strings.Split(d.Pattern, "/") {
			if strings.HasPrefix(part, "{") {
				params = append(params, map[string]any{"name": strings.Trim(part, "{}"), "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
			}
		}
		for _, name := range d.Query {
			typ := "integer"
			if name == "before_id" || name == "search" || name == "query" || name == "status" || name == "source_conversation_id" || name == "batch_id" || name == "cursor" || name == "after" || name == "client_id" || name == "filename" {
				typ = "string"
			}
			if name == "include_archived" {
				typ = "boolean"
			}
			params = append(params, map[string]any{"name": name, "in": "query", "required": name == "expected_revision" || d.Operation == "attachments_upload" || d.Operation == "documents_upload", "schema": map[string]any{"type": typ}})
		}
		if d.Operation == "stream" {
			params = append(params, map[string]any{"name": "Last-Event-ID", "in": "header", "schema": map[string]any{"type": "string"}})
			op["description"] = "Replays committed run events and message.delta text chunks when conversation.stream.v1 is supported. Reconnect with Last-Event-ID; connections close after 30 seconds. Run snapshots include draft_text, draft_bytes and last_event_seq. Delta offsets are UTF-8 bytes within an attempt; run.started resets the draft. Only run.completed promotes a draft to message history. Resume adds later events to the same run."
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		out[d.Pattern] = op
	}
	return out
}

// Only authenticated service clients may supply this envelope. The server
// binds its runtime identity to the API key's configured runtime scope.
type ConversationRPCRequest struct {
	ContractPublication  ConversationContractPublicationRequest  `json:"contract_publication,omitempty"`
	DeliveryPublication  ConversationDeliveryPublicationRequest  `json:"delivery_publication,omitempty"`
	DisagreementID       string                                  `json:"disagreement_id,omitempty"`
	AgreementBefore      int64                                   `json:"agreement_before,omitempty"`
	AgentMatch           ConversationAgentMatchRequest           `json:"agent_match,omitempty"`
	AgentID              string                                  `json:"agent_id,omitempty"`
	AgentWrite           ConversationAgentWrite                  `json:"agent_write,omitempty"`
	DelegationID         string                                  `json:"delegation_id,omitempty"`
	DelegationCreate     ConversationDelegationCreate            `json:"delegation_create,omitempty"`
	DelegationUpdate     ConversationDelegationUpdate            `json:"delegation_update,omitempty"`
	AgentMessage         ConversationAgentMessageSend            `json:"agent_message,omitempty"`
	ScheduledTask        ScheduledConversationTaskRequest        `json:"scheduled_task,omitempty"`
	ResultRead           ConversationResultRead                  `json:"result_read,omitempty"`
	DeliveryResultRead   ConversationDeliveryResultRead          `json:"delivery_result_read,omitempty"`
	ExecutionShare       ConversationExecutionShare              `json:"execution_share,omitempty"`
	ExecutionReference   ConversationRunReference                `json:"execution_reference,omitempty"`
	DeliveryArtifactRead ConversationDeliveryArtifactRead        `json:"delivery_artifact_read,omitempty"`
	DocumentID           string                                  `json:"document_id,omitempty"`
	DocumentAfter        string                                  `json:"document_after,omitempty"`
	DocumentTransfer     KnowledgeDocumentTransfer               `json:"document_transfer,omitempty"`
	DocumentImport       KnowledgeAttachmentImport               `json:"document_import,omitempty"`
	DocumentUpload       KnowledgeDocumentUpload                 `json:"document_upload,omitempty"`
	LibraryID            string                                  `json:"library_id,omitempty"`
	LibraryUserID        string                                  `json:"library_user_id,omitempty"`
	LibraryAfter         string                                  `json:"library_after,omitempty"`
	LibraryCreate        KnowledgeLibraryCreate                  `json:"library_create,omitempty"`
	LibrarySourceWrite   KnowledgeLibrarySourceWrite             `json:"library_source_write,omitempty"`
	LibraryUpdate        KnowledgeLibraryUpdate                  `json:"library_update,omitempty"`
	LibraryMemberWrite   KnowledgeLibraryMemberWrite             `json:"library_member_write,omitempty"`
	AttachmentID         string                                  `json:"attachment_id,omitempty"`
	AttachmentAfter      string                                  `json:"attachment_after,omitempty"`
	AttachmentUpload     ConversationAttachmentUpload            `json:"attachment_upload,omitempty"`
	ArtifactID           string                                  `json:"artifact_id,omitempty"`
	ArtifactVersion      int64                                   `json:"artifact_version,omitempty"`
	ArtifactBefore       int64                                   `json:"artifact_before,omitempty"`
	ArtifactExportID     string                                  `json:"artifact_export_id,omitempty"`
	ArtifactQuery        ConversationArtifactQuery               `json:"artifact_query,omitempty"`
	ArtifactCreate       ConversationArtifactCreate              `json:"artifact_create,omitempty"`
	ArtifactEdit         ConversationArtifactEdit                `json:"artifact_edit,omitempty"`
	ArtifactExport       ConversationArtifactExportRequest       `json:"artifact_export,omitempty"`
	TodoID               string                                  `json:"todo_id,omitempty"`
	TodoQuery            ConversationTodoQuery                   `json:"todo_query,omitempty"`
	TodoCreate           ConversationTodoCreate                  `json:"todo_create,omitempty"`
	TodoUpdate           ConversationTodoUpdate                  `json:"todo_update,omitempty"`
	TodoDelete           ConversationTodoDelete                  `json:"todo_delete,omitempty"`
	TaskID               string                                  `json:"task_id,omitempty"`
	TaskQuery            ConversationTaskQuery                   `json:"task_query,omitempty"`
	TaskAgreementUpdate  ConversationTaskAgreementUpdate         `json:"task_agreement_update,omitempty"`
	TaskCompletionReview ConversationTaskCompletionReviewRequest `json:"task_completion_review,omitempty"`
	PlanBefore           int64                                   `json:"plan_before,omitempty"`
	CompletionBefore     int64                                   `json:"completion_before,omitempty"`
	Response             ConversationInteractionResponse         `json:"response,omitempty"`
	Authority            ConversationAuthority                   `json:"authority"`
	ConversationID       string                                  `json:"conversation_id,omitempty"`
	RunID                string                                  `json:"run_id,omitempty"`
	MemoryID             string                                  `json:"memory_id,omitempty"`
	Revision             int64                                   `json:"revision,omitempty"`
	AfterSeq             int64                                   `json:"after_seq,omitempty"`
	Limit                int                                     `json:"limit,omitempty"`
	Create               ConversationCreate                      `json:"create,omitempty"`
	Query                ConversationQuery                       `json:"query,omitempty"`
	Update               ConversationUpdate                      `json:"update,omitempty"`
	Send                 ConversationSend                        `json:"send,omitempty"`
	Messages             ConversationMessageQuery                `json:"messages,omitempty"`
	Memory               ConversationMemoryWrite                 `json:"memory,omitempty"`
}

func ConversationInteractionPermission() *actioncontract.PermissionDefinition {
	return &actioncontract.PermissionDefinition{Key: ConversationActionPrefix + "respond", Owner: AgentAuthorizationOwner, ResourceKey: AgentCapabilityConversation, OperationKey: "respond", Label: "Respond to conversation questions and confirmations", Category: "Personal work tools", LifecycleStatus: actioncontract.LifecycleActive}
}
