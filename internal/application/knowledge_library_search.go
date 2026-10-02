package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (s *ConversationService) libraryKnowledgeSource() (agentsdk.ConversationLibraryKnowledgeSource, error) {
	source, ok := s.options.Knowledge.(agentsdk.ConversationLibraryKnowledgeSource)
	if !ok || source == nil {
		return nil, conversationFailure("unavailable", "knowledge_unavailable")
	}
	return source, nil
}

func (s *ConversationService) ListKnowledgeLibraries(ctx context.Context, after string, limit int, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	return source.ListKnowledgeLibraries(ctx, after, limit, authority)
}

func (s *ConversationService) SearchLibraryKnowledge(ctx context.Context, libraryID, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	return source.SearchLibraryKnowledge(ctx, libraryID, query, authority)
}

func (s *ConversationService) ReadLibraryKnowledge(ctx context.Context, libraryID, documentID string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	return source.ReadLibraryKnowledge(ctx, libraryID, documentID, authority)
}

func (s *ConversationService) SearchKnowledge(ctx context.Context, query string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	return source.SearchKnowledge(ctx, query, authority)
}

func (s *ConversationService) ReadKnowledge(ctx context.Context, documentID string, authority agentsdk.ConversationAuthority) (agentsdk.ConversationKnowledgeResult, error) {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return agentsdk.ConversationKnowledgeResult{}, err
	}
	return source.ReadKnowledge(ctx, documentID, authority)
}

func (s *ConversationService) RevalidateKnowledge(ctx context.Context, saved agentsdk.ConversationKnowledgeResult, authority agentsdk.ConversationAuthority) error {
	source, err := s.libraryKnowledgeSource()
	if err != nil {
		return err
	}
	return source.RevalidateKnowledge(ctx, saved, authority)
}

var _ agentsdk.ConversationLibraryKnowledgeSource = (*ConversationService)(nil)
