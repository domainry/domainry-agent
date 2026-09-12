package application

import (
	context "context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

// Unconfigured capabilities fail closed while the generic Agent remains usable.
type unconfiguredKnowledge struct{}

func (unconfiguredKnowledge) Artifact(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var zero1 agentsdk.ConversationArtifactVersion
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactAccess(ctx context.Context, a agentsdk.ConversationAuthority, key string, input any) (persistence.ConversationArtifactRepository, error) {
	var zero1 persistence.ConversationArtifactRepository
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactBody(ctx context.Context, record persistence.ConversationArtifactRecord, content agentsdk.ConversationArtifactContent, a agentsdk.ConversationAuthority) (persistence.ConversationArtifactRecord, error) {
	var zero1 persistence.ConversationArtifactRecord
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactContent(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactContent, error) {
	var zero1 agentsdk.ConversationArtifactContent
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactOrigin(ctx context.Context, conversationID, runID string, a agentsdk.ConversationAuthority) (*agentsdk.ConversationSources, error) {
	var zero1 *agentsdk.ConversationSources
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	var zero1 agentsdk.ConversationArtifactVersions
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ArtifactView(ctx context.Context, record persistence.ConversationArtifactRecord, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var zero1 agentsdk.ConversationArtifactVersion
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	var zero1 agentsdk.ConversationArtifactPage
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) Attachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero1 agentsdk.ConversationAttachment
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentAccess(ctx context.Context, action string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRepository, error) {
	var zero1 persistence.ConversationAttachmentRepository
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentIndexView(ctx context.Context, r persistence.ConversationAttachmentRecord, a agentsdk.ConversationAuthority) agentsdk.ConversationAttachment {
	var zero1 agentsdk.ConversationAttachment
	return zero1
}
func (unconfiguredKnowledge) AttachmentIndexWorker(ctx context.Context, repo persistence.ConversationAttachmentIndexRepository) {
}
func (unconfiguredKnowledge) AttachmentIndexWriteAllowed(ctx context.Context, r persistence.ConversationAttachmentRecord) bool {
	var zero1 bool
	return zero1
}
func (unconfiguredKnowledge) AttachmentKnowledge(ctx context.Context, conversation, op, q, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	var zero1 agentsdk.ConversationKnowledgeResult
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentKnowledgeAccess(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, map[string]persistence.ConversationAttachmentRecord, error) {
	var zero1 agentsdk.ConversationAttachmentKnowledgeScope
	var zero2 map[string]persistence.ConversationAttachmentRecord
	return zero1, zero2, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentKnowledgeBinding(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	var zero1 agentsdk.ConversationAttachmentKnowledgeScope
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentRecord(ctx context.Context, repo persistence.ConversationAttachmentRepository, conversationID, id string, a agentsdk.ConversationAuthority) (persistence.ConversationAttachmentRecord, error) {
	var zero1 persistence.ConversationAttachmentRecord
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AttachmentView(item agentsdk.ConversationAttachment) agentsdk.ConversationAttachment {
	var zero1 agentsdk.ConversationAttachment
	return zero1
}
func (unconfiguredKnowledge) Attachments(ctx context.Context, conversationID, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentPage, error) {
	var zero1 agentsdk.ConversationAttachmentPage
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) AuthorizeAttachmentKnowledgeResult(ctx context.Context, in agentsdk.ConversationToolRequest, result agentsdk.ConversationToolResult) error {
	return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) BindKnowledgeLibrarySource(ctx context.Context, id string, in agentsdk.KnowledgeLibrarySourceWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) CheckAttachmentIndex(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero1 agentsdk.ConversationAttachment
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) CleanAttachment(ctx context.Context, repo persistence.ConversationAttachmentRepository, work persistence.ConversationAttachmentCleanup) error {
	return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) CleanupAttachments(ctx context.Context, repo persistence.ConversationAttachmentRepository) {
}
func (unconfiguredKnowledge) Close() {
}
func (unconfiguredKnowledge) CreateArtifact(ctx context.Context, in agentsdk.ConversationArtifactCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var zero1 agentsdk.ConversationArtifactVersion
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DatasourceAccess(ctx context.Context, id, op string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DatasourceDefinitions(ctx context.Context, library string, a agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDatasourceDefinition, error) {
	var zero1 []agentsdk.KnowledgeDatasourceDefinition
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DeleteAttachment(ctx context.Context, conversationID, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero1 agentsdk.ConversationAttachment
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DeleteKnowledgeDocument(ctx context.Context, library, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DocumentAccess(ctx context.Context, library, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRepository, error) {
	var zero1 persistence.KnowledgeDocumentRepository
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DocumentBinding(ctx context.Context, library string, a agentsdk.ConversationAuthority) (agentsdk.ManagedKnowledgeDocumentSource, error) {
	var zero1 agentsdk.ManagedKnowledgeDocumentSource
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DownloadArtifact(ctx context.Context, exportID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactDownload, error) {
	var zero1 agentsdk.ConversationArtifactDownload
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DownloadAttachment(ctx context.Context, conversationID, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentDownload, error) {
	var zero1 agentsdk.ConversationAttachmentDownload
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) DownloadKnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	var zero1 agentsdk.KnowledgeDocumentDownload
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) EditArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactEdit, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var zero1 agentsdk.ConversationArtifactVersion
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ExportArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactExportRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var zero1 agentsdk.ConversationArtifactExport
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ImportConversationAttachment(ctx context.Context, library string, in agentsdk.KnowledgeAttachmentImport, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) IndexAttachment(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero1 agentsdk.ConversationAttachment
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeDocumentRecord(ctx context.Context, repo persistence.KnowledgeDocumentRepository, library, id string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRecord, error) {
	var zero1 persistence.KnowledgeDocumentRecord
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeDocumentWorker(ctx context.Context, repo persistence.KnowledgeDocumentRepository) {
}
func (unconfiguredKnowledge) KnowledgeDocuments(ctx context.Context, library, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentPage, error) {
	var zero1 agentsdk.KnowledgeDocumentPage
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryPage, error) {
	var zero1 agentsdk.KnowledgeLibraryPage
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryMembers, error) {
	var zero1 agentsdk.KnowledgeLibraryMembers
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) KnowledgeLibrarySources(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrarySources, error) {
	var zero1 agentsdk.KnowledgeLibrarySources
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) LibraryAccess(ctx context.Context, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeLibraryRepository, error) {
	var zero1 persistence.KnowledgeLibraryRepository
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) LibraryAuthorize(ctx context.Context, op string, item agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) error {
	return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) LibraryResult(ctx context.Context, item agentsdk.KnowledgeLibrary, err error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) ProcessAttachmentIndex(ctx context.Context, repo persistence.ConversationAttachmentIndexRepository, lease persistence.ConversationAttachmentIndexLease) {
}
func (unconfiguredKnowledge) ProcessKnowledgeDocument(ctx context.Context, repo persistence.KnowledgeDocumentRepository, lease persistence.KnowledgeDocumentLease) {
}
func (unconfiguredKnowledge) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, revision int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) Start(parent context.Context) {
}
func (unconfiguredKnowledge) TransferKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentTransfer, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	var zero1 agentsdk.KnowledgeLibrary
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) UploadAttachment(ctx context.Context, conversationID string, in agentsdk.ConversationAttachmentUpload, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero1 agentsdk.ConversationAttachment
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) UploadDocumentContent(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, origin *persistence.KnowledgeAttachmentOrigin, documentOrigin *persistence.KnowledgeDocumentOrigin, recheck func() error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) UploadKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	var zero1 agentsdk.KnowledgeDocument
	return zero1, &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}
func (unconfiguredKnowledge) WakeAttachmentCleanup() {
}
func (unconfiguredKnowledge) WakeAttachmentIndex() {
}
func (unconfiguredKnowledge) WakeKnowledgeDocuments() {
}
