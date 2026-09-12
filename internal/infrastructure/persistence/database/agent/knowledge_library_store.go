package agent

import (
	"context"
	"database/sql"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	"github.com/domainry/domainry-orm/query"
)

// Legacy API forwarding. Knowledge owns the data implementation.
func libraryScope(a agentsdk.ConversationAuthority) string {
	return knowledgemodule.CompatLibraryScope(a)
}

func libraryPredicate(a agentsdk.ConversationAuthority, id string) query.Predicate {
	return knowledgemodule.CompatLibraryPredicate(a, id)
}

func validLibraryID(id string) bool { return knowledgemodule.CompatValidLibraryID(id) }

func validLibraryUser(id string) bool { return knowledgemodule.CompatValidLibraryUser(id) }

func validLibraryRole(role string) bool { return knowledgemodule.CompatValidLibraryRole(role) }

func (s *ConversationStore) library(ctx context.Context, db conversationDB, id string, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeStore().CompatLibrary(ctx, db, id, a)
}

func (s *ConversationStore) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	return s.knowledgeStore().KnowledgeLibrary(ctx, id, a)
}

func (s *ConversationStore) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	return s.knowledgeStore().CreateKnowledgeLibrary(ctx, in, a)
}

func libraryPageLimit(after string, limit int, users bool) (int, error) {
	return knowledgemodule.CompatLibraryPageLimit(after, limit, users)
}

func (s *ConversationStore) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryPage, err error) {
	return s.knowledgeStore().KnowledgeLibraries(ctx, after, limit, a)
}

func (s *ConversationStore) saveLibrary(ctx context.Context, tx *sql.Tx, item *agentsdk.KnowledgeLibrary, expected int64, a agentsdk.ConversationAuthority) error {
	return s.knowledgeStore().CompatSaveLibrary(ctx, tx, item, expected, a)
}

func (s *ConversationStore) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	return s.knowledgeStore().UpdateKnowledgeLibrary(ctx, id, in, a)
}

func (s *ConversationStore) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryMembers, err error) {
	return s.knowledgeStore().KnowledgeLibraryMembers(ctx, id, after, limit, a)
}

func (s *ConversationStore) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeStore().SetKnowledgeLibraryMember(ctx, id, user, in, a)
}

func (s *ConversationStore) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.KnowledgeLibrary, error) {
	return s.knowledgeStore().RemoveKnowledgeLibraryMember(ctx, id, user, expected, a)
}

func (s *ConversationStore) mutateLibraryMember(ctx context.Context, id, user, role string, expected int64, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	return s.knowledgeStore().CompatMutateLibraryMember(ctx, id, user, role, expected, a)
}
