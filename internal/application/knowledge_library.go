package application

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) libraryAccess(ctx context.Context, op string, a agentsdk.ConversationAuthority) (persistence.KnowledgeLibraryRepository, error) {
	if e := s.authorize(a); e != nil {
		return nil, e
	}
	repo, ok := s.repo.(persistence.KnowledgeLibraryRepository)
	if !ok || s.options.LibraryAuthorizer == nil {
		return nil, conversationFailure("unavailable", "libraries_unavailable")
	}
	// Resource-specific decisions are made after trusted facts are loaded.
	return repo, nil
}
func (s *ConversationService) libraryAuthorize(ctx context.Context, op string, item agentsdk.KnowledgeLibrary, a agentsdk.ConversationAuthority) error {
	return s.options.LibraryAuthorizer.AuthorizeKnowledgeLibrary(ctx, op, item, a)
}
func (s *ConversationService) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	repo, e := s.libraryAccess(ctx, "libraries_create", a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_create", agentsdk.KnowledgeLibrary{Kind: in.Kind, OwnerUserID: a.UserID}, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	out, err := repo.CreateKnowledgeLibrary(ctx, in, a)
	return s.libraryResult(out, err, a)
}
func (s *ConversationService) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryPage, error) {
	repo, e := s.libraryAccess(ctx, "libraries_list", a)
	if e != nil {
		return agentsdk.KnowledgeLibraryPage{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_list", agentsdk.KnowledgeLibrary{}, a); e != nil {
		return agentsdk.KnowledgeLibraryPage{}, e
	}
	page, e := repo.KnowledgeLibraries(ctx, after, limit, a)
	if e != nil {
		return page, e
	}
	// Resolve Identity again at return time. The repository only ever selects
	// the caller's memberships; knowledge provider scopes are not inferred here.
	if e = s.libraryAuthorize(ctx, "libraries_list", agentsdk.KnowledgeLibrary{}, a); e != nil {
		return agentsdk.KnowledgeLibraryPage{}, e
	}
	for i := range page.Items {
		page.Items[i], _ = s.libraryResult(page.Items[i], nil, a)
	}
	return page, nil
}
func (s *ConversationService) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	repo, e := s.libraryAccess(ctx, "libraries_get", a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	item, e := repo.KnowledgeLibrary(ctx, id, a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_get", item, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	return s.libraryResult(item, nil, a)
}
func (s *ConversationService) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	repo, e := s.libraryAccess(ctx, "libraries_update", a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	item, e := repo.KnowledgeLibrary(ctx, id, a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_update", item, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	out, err := repo.UpdateKnowledgeLibrary(ctx, id, in, a)
	return s.libraryResult(out, err, a)
}
func (s *ConversationService) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibraryMembers, error) {
	repo, e := s.libraryAccess(ctx, "libraries_members", a)
	if e != nil {
		return agentsdk.KnowledgeLibraryMembers{}, e
	}
	item, e := repo.KnowledgeLibrary(ctx, id, a)
	if e != nil {
		return agentsdk.KnowledgeLibraryMembers{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_members", item, a); e != nil {
		return agentsdk.KnowledgeLibraryMembers{}, e
	}
	return repo.KnowledgeLibraryMembers(ctx, id, after, limit, a)
}
func (s *ConversationService) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	repo, e := s.libraryAccess(ctx, "libraries_set_member", a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	item, e := repo.KnowledgeLibrary(ctx, id, a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_set_member", item, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if item.Kind != "shared" || item.Role != "manager" {
		return agentsdk.KnowledgeLibrary{}, conversationFailure("forbidden", "library_manage_required")
	}
	if e = s.options.LibraryAuthorizer.ValidateKnowledgeLibraryMember(ctx, user, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	out, err := repo.SetKnowledgeLibraryMember(ctx, id, user, in, a)
	return s.libraryResult(out, err, a)
}
func (s *ConversationService) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, revision int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	repo, e := s.libraryAccess(ctx, "libraries_remove_member", a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	item, e := repo.KnowledgeLibrary(ctx, id, a)
	if e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	if e = s.libraryAuthorize(ctx, "libraries_remove_member", item, a); e != nil {
		return agentsdk.KnowledgeLibrary{}, e
	}
	// Removing an inactive account must remain possible; validate only additions.
	out, err := repo.RemoveKnowledgeLibraryMember(ctx, id, user, revision, a)
	return s.libraryResult(out, err, a)
}

var _ agentsdk.KnowledgeLibraryService = (*ConversationService)(nil)

func (s *ConversationService) libraryResult(item agentsdk.KnowledgeLibrary, err error, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	if err != nil {
		return agentsdk.KnowledgeLibrary{}, err
	}
	if source, ok := s.options.Knowledge.(*libraryKnowledgeSource); ok {
		item.KnowledgeConfigured = source.configured(item.ID, a)
	}
	if s.options.DocumentStorage != nil {
		_, unavailable := s.documentBinding(item.ID, a)
		item.DocumentsConfigured = unavailable == nil
	}
	return item, nil
}
