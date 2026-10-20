package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

type unconfiguredKnowledgePages struct{}

func knowledgePagesUnavailable() error {
	return &agentsdk.Error{Class: "unavailable", Code: "agent.conversation.knowledge_not_configured"}
}

func (unconfiguredKnowledgePages) KnowledgePages(context.Context, string, string, string, int, bool, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePagePage, error) {
	return knowledgesdk.KnowledgePagePage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) KnowledgePage(context.Context, string, string, bool, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) CreateKnowledgePage(context.Context, string, knowledgesdk.KnowledgePageCreate, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) SaveKnowledgePageDraft(context.Context, string, string, knowledgesdk.KnowledgePageDraftWrite, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) MoveKnowledgePage(context.Context, string, string, knowledgesdk.KnowledgePageMove, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePage, error) {
	return knowledgesdk.KnowledgePage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) ReorderKnowledgePages(context.Context, string, knowledgesdk.KnowledgePageReorder, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePagePage, error) {
	return knowledgesdk.KnowledgePagePage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) CopyKnowledgePage(context.Context, string, string, knowledgesdk.KnowledgePageCopy, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) SetKnowledgePageArchived(context.Context, string, string, bool, knowledgesdk.KnowledgePageStateWrite, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePage, error) {
	return knowledgesdk.KnowledgePage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) PublishKnowledgePage(context.Context, string, string, knowledgesdk.KnowledgePagePublish, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) KnowledgePageVersions(context.Context, string, string, int64, int, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageVersionPage, error) {
	return knowledgesdk.KnowledgePageVersionPage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) KnowledgePageVersion(context.Context, string, string, int64, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageVersion, error) {
	return knowledgesdk.KnowledgePageVersion{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) DiffKnowledgePageVersions(context.Context, string, string, knowledgesdk.KnowledgePageVersionDiffInput, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageVersionDiff, error) {
	return knowledgesdk.KnowledgePageVersionDiff{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) RestoreKnowledgePageVersion(context.Context, string, string, int64, knowledgesdk.KnowledgePageRestoreVersion, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) KnowledgePageComments(context.Context, string, string, string, int, bool, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageCommentPage, error) {
	return knowledgesdk.KnowledgePageCommentPage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) CreateKnowledgePageComment(context.Context, string, string, knowledgesdk.KnowledgePageCommentCreate, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageComment, error) {
	return knowledgesdk.KnowledgePageComment{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) UpdateKnowledgePageComment(context.Context, string, string, string, knowledgesdk.KnowledgePageCommentUpdate, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageComment, error) {
	return knowledgesdk.KnowledgePageComment{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) ResolveKnowledgePageComment(context.Context, string, string, string, knowledgesdk.KnowledgePageCommentResolve, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageComment, error) {
	return knowledgesdk.KnowledgePageComment{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) DeleteKnowledgePageComment(context.Context, string, string, string, knowledgesdk.KnowledgePageCommentDelete, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageComment, error) {
	return knowledgesdk.KnowledgePageComment{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) KnowledgePageAttachments(context.Context, string, string, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageAttachmentPage, error) {
	return knowledgesdk.KnowledgePageAttachmentPage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) SetKnowledgePageAttachment(context.Context, string, string, string, knowledgesdk.KnowledgePageAttachmentWrite, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageAttachment, error) {
	return knowledgesdk.KnowledgePageAttachment{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) RemoveKnowledgePageAttachment(context.Context, string, string, string, knowledgesdk.KnowledgePageAttachmentRemove, agentsdk.ConversationAuthority) error {
	return knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) SearchKnowledgePrincipals(context.Context, string, string, int, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePrincipalPage, error) {
	return knowledgesdk.KnowledgePrincipalPage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) ResolveKnowledgePrincipals(context.Context, []string, agentsdk.ConversationAuthority) ([]knowledgesdk.KnowledgePrincipal, error) {
	return nil, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) SearchKnowledgeLibraryMembers(context.Context, string, string, string, int, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePrincipalPage, error) {
	return knowledgesdk.KnowledgePrincipalPage{}, knowledgePagesUnavailable()
}
func (unconfiguredKnowledgePages) ExplainKnowledgeAccess(context.Context, string, knowledgesdk.KnowledgeAccessExplainRequest, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgeAccessExplanation, error) {
	return knowledgesdk.KnowledgeAccessExplanation{}, knowledgePagesUnavailable()
}
