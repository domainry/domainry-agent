package remote

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) KnowledgeLibrarySources(ctx context.Context, id, after string, limit int, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrarySources, err error) {
	err = c.call(ctx, "libraries_sources", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibraryAfter: after, Limit: limit}, &out)
	return
}
func (c *conversationClient) BindKnowledgeLibrarySource(ctx context.Context, id string, in agentsdk.KnowledgeLibrarySourceWrite, a agentsdk.ConversationAuthority) (out agentsdk.KnowledgeLibrary, err error) {
	err = c.call(ctx, "libraries_bind_source", agentsdk.ConversationRPCRequest{Authority: a, LibraryID: id, LibrarySourceWrite: in}, &out)
	return
}
