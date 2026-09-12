package integration_test

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// A source predating the optional recovery contract must keep uncertain
// deletion state, even if a permission-scoped metadata query returns missing.
type noDeleteRecoverySource struct {
	agentsdk.ManagedKnowledgeDocumentSource
}
type noLibraryDeleteRecoverySource struct {
	noDeleteRecoverySource
	agentsdk.ConversationKnowledgeSource
}

func (s noDeleteRecoverySource) KnowledgeDocumentAccessPolicySHA256() string {
	return s.ManagedKnowledgeDocumentSource.(agentsdk.KnowledgeDocumentAccessPolicySource).KnowledgeDocumentAccessPolicySHA256()
}
func (s noDeleteRecoverySource) KnowledgeDocumentMaxBytes() int64 {
	return s.ManagedKnowledgeDocumentSource.(agentsdk.KnowledgeDocumentSizeLimitSource).KnowledgeDocumentMaxBytes()
}

type noAttachmentDeleteRecovery struct {
	agentsdk.ConversationAttachmentKnowledge
}

func (s noAttachmentDeleteRecovery) ResolveAttachmentKnowledge(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	out, err := s.ConversationAttachmentKnowledge.ResolveAttachmentKnowledge(ctx, id, a)
	if err == nil {
		out.Source = noDeleteRecoverySource{out.Source}
	}
	return out, err
}
