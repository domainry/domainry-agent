package remote

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) CreateKnowledgeLibrary(ctx context.Context, in agentsdk.KnowledgeLibraryCreate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_create", agentsdk.ConversationRPCRequest{Authority: a, LibraryCreate: in}, &out)
	return
}
func (c *conversationClient) KnowledgeLibraries(ctx context.Context, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryPage, err error) {
	err = c.call(ctx, "libraries_list", agentsdk.ConversationRPCRequest{Authority: a, LibraryAfter: after, Limit: limit}, &out)
	return
}
func (c *conversationClient) KnowledgeLibrary(ctx context.Context, id string, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_get", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id}, &out)
	return
}
func (c *conversationClient) UpdateKnowledgeLibrary(ctx context.Context, id string, in agentsdk.KnowledgeLibraryUpdate, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_update", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibraryUpdate: in}, &out)
	return
}
func (c *conversationClient) KnowledgeLibraryMembers(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibraryMembers, err error) {
	err = c.call(ctx, "libraries_members", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibraryAfter: after, Limit: limit}, &out)
	return
}
func (c *conversationClient) SetKnowledgeLibraryMember(ctx context.Context, id, user string, in agentsdk.KnowledgeLibraryMemberWrite, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_set_member", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibraryUserID: user, LibraryMemberWrite: in}, &out)
	return
}
func (c *conversationClient) RemoveKnowledgeLibraryMember(ctx context.Context, id, user string, revision int64, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_remove_member", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibraryUserID: user, Revision: revision}, &out)
	return
}

var _ agentsdk.KnowledgeLibraryService = (*conversationClient)(nil)
