package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) documentAccess(ctx context.Context, library, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRepository, error) {
	return s.knowledgeService().DocumentAccess(ctx, library, op, a)
}

func (s *ConversationService) documentBinding(ctx context.Context, library string, a agentsdk.ConversationAuthority) (agentsdk.ManagedKnowledgeDocumentSource, error) {
	return s.knowledgeService().DocumentBinding(ctx, library, a)
}

func (s *ConversationService) UploadKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().UploadKnowledgeDocument(ctx, library, in, a)
}

func (s *ConversationService) uploadKnowledgeDocument(ctx context.Context, library string, in agentsdk.KnowledgeDocumentUpload, origin *persistence.KnowledgeAttachmentOrigin, documentOrigin *persistence.KnowledgeDocumentOrigin, recheck func() error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().UploadDocumentContent(ctx, library, in, origin, documentOrigin, recheck, a)
}

func (s *ConversationService) knowledgeDocumentRecord(ctx context.Context, repo persistence.KnowledgeDocumentRepository, library, id string, a agentsdk.ConversationAuthority) (persistence.KnowledgeDocumentRecord, error) {
	return s.knowledgeService().KnowledgeDocumentRecord(ctx, repo, library, id, a)
}

func (s *ConversationService) KnowledgeDocuments(ctx context.Context, library, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentPage, error) {
	return s.knowledgeService().KnowledgeDocuments(ctx, library, after, limit, a)
}

func (s *ConversationService) KnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().KnowledgeDocument(ctx, library, id, a)
}

func (s *ConversationService) DownloadKnowledgeDocument(ctx context.Context, library, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentDownload, error) {
	return s.knowledgeService().DownloadKnowledgeDocument(ctx, library, id, a)
}

func (s *ConversationService) DeleteKnowledgeDocument(ctx context.Context, library, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocument, error) {
	return s.knowledgeService().DeleteKnowledgeDocument(ctx, library, id, expected, a)
}

func (s *ConversationService) wakeKnowledgeDocuments() { s.knowledgeService().WakeKnowledgeDocuments() }

func activateDocumentSources(repo persistence.ConversationRepository, runtime string, options ConversationOptions) error {
	if options.KnowledgeFactory == nil {
		return nil
	}
	return options.KnowledgeFactory.Activate(repo, runtime, knowledgeOptions(options))
}
