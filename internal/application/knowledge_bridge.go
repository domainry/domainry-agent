package application

import (
	"context"

	sdk "github.com/domainry/domainry-agent-sdk"
	knowledge "github.com/domainry/domainry-knowledge/contract"
)

func knowledgeOptions(o ConversationOptions) knowledge.Options {
	return knowledge.Options{
		DocumentStorage: o.DocumentStorage, DocumentPoll: o.DocumentPoll,
		LibraryKnowledge: o.LibraryKnowledge, KnowledgeDatasources: o.KnowledgeDatasources,
		LibraryAuthorizer: o.LibraryAuthorizer, AttachmentStorage: o.AttachmentStorage,
		AttachmentAuthorizer: o.AttachmentAuthorizer, AttachmentKnowledge: o.AttachmentKnowledge,
		ArtifactStorage: o.ArtifactStorage, ArtifactExportTTL: o.ArtifactExportTTL,
		PersonalAuthorizer: o.PersonalAuthorizer, Knowledge: o.Knowledge,
	}
}

// This adapter preserves existing public conversation routes while Knowledge
// owns business service lifetimes. It only supplies source authorization.
func (s *ConversationService) knowledgeService() knowledge.Service {
	s.knowledgeOnce.Do(func() {
		options := knowledgeOptions(s.options)
		options.Sources = knowledgeSourcePolicy{s: s}
		if s.options.KnowledgeFactory != nil {
			s.knowledgeModule = s.options.KnowledgeFactory.NewService(s.repo, s.runtimeID, options)
		} else {
			s.knowledgeModule = unconfiguredKnowledge{}
		}
	})
	return s.knowledgeModule
}

type knowledgeSourcePolicy struct{ s *ConversationService }

func (p knowledgeSourcePolicy) CheckSources(ctx context.Context, a sdk.ConversationAuthority, consumer string, sources *sdk.ConversationSources) ([]sdk.ConversationRunReference, error) {
	return p.s.sourceAudit(a, consumer).sources(ctx, sources)
}
func (p knowledgeSourcePolicy) CheckRun(ctx context.Context, a sdk.ConversationAuthority, consumer string, ref sdk.ConversationRunReference) ([]sdk.ConversationRunReference, error) {
	return p.s.sourceAudit(a, consumer).run(ctx, ref)
}
