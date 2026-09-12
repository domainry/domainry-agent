package application

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) libraryAccess(ctx context.Context, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeLibraryRepository, error) {
	return s.knowledgeService().LibraryAccess(ctx, op, a)
}

func (s *ConversationService) libraryAuthorize(ctx context.Context, op string, item agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) error {
	return s.knowledgeService().LibraryAuthorize(ctx, op, item, a)
}

func (s *ConversationService) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().CreateKnowledgeLibrary(ctx, in, a)
}

func (s *ConversationService) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryPage, error) {
	return s.knowledgeService().KnowledgeLibraries(ctx, after, limit, a)
}

func (s *ConversationService) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().KnowledgeLibrary(ctx, id, a)
}

func (s *ConversationService) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().UpdateKnowledgeLibrary(ctx, id, in, a)
}

func (s *ConversationService) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryMembers, error) {
	return s.knowledgeService().KnowledgeLibraryMembers(ctx, id, after, limit, a)
}

func (s *ConversationService) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().SetKnowledgeLibraryMember(ctx, id, user, in, a)
}

func (s *ConversationService) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, revision int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().RemoveKnowledgeLibraryMember(ctx, id, user, revision, a)
}

func (s *ConversationService) libraryResult(ctx context.Context, item agentsdk.KnowledgeLibrary, err error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeService().LibraryResult(ctx, item, err, a)
}
