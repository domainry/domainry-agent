package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
)

// An Agent without Knowledge must reject collaboration just like its other
// Knowledge operations; it must not create a second collaboration owner.
func (unconfiguredKnowledgePages) ConnectKnowledgeCollaboration(context.Context, string, string, knowledgesdk.KnowledgeCollaborationConnect, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgeCollaborationSession, error) {
	return knowledgesdk.KnowledgeCollaborationSession{}, knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) SyncKnowledgeCollaboration(context.Context, string, string, knowledgesdk.KnowledgeCollaborationSync, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgeCollaborationSyncResult, error) {
	return knowledgesdk.KnowledgeCollaborationSyncResult{}, knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) CloseKnowledgeCollaboration(context.Context, string, string, string, agentsdk.ConversationAuthority) error {
	return knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) PublishKnowledgeCollaboration(context.Context, string, string, knowledgesdk.KnowledgeCollaborationPublish, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageView, error) {
	return knowledgesdk.KnowledgePageView{}, knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) KnowledgePageAccess(context.Context, string, string, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageAccessPolicy, error) {
	return knowledgesdk.KnowledgePageAccessPolicy{}, knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) SetKnowledgePageAccess(context.Context, string, string, knowledgesdk.KnowledgePageAccessWrite, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageAccessPolicy, error) {
	return knowledgesdk.KnowledgePageAccessPolicy{}, knowledgePagesUnavailable()
}

func (unconfiguredKnowledgePages) PreviewKnowledgePageMove(context.Context, string, string, knowledgesdk.KnowledgePageMovePreview, agentsdk.ConversationAuthority) (knowledgesdk.KnowledgePageMoveImpact, error) {
	return knowledgesdk.KnowledgePageMoveImpact{}, knowledgePagesUnavailable()
}
