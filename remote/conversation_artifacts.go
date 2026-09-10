package remote

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func (c *conversationClient) Artifacts(ctx context.Context, in agentsdk.ConversationArtifactQuery, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactPage, error) {
	var out agentsdk.ConversationArtifactPage
	err := c.call(ctx, "artifacts_list", agentsdk.ConversationRPCRequest{Authority: a, ArtifactQuery: in}, &out)
	return out, err
}
func (c *conversationClient) Artifact(ctx context.Context, id string, version int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	err := c.call(ctx, "artifacts_get", agentsdk.ConversationRPCRequest{Authority: a, ArtifactID: id, ArtifactVersion: version}, &out)
	return out, err
}
func (c *conversationClient) ArtifactVersions(ctx context.Context, id string, before int64, limit int, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersions, error) {
	var out agentsdk.ConversationArtifactVersions
	err := c.call(ctx, "artifacts_versions", agentsdk.ConversationRPCRequest{Authority: a, ArtifactID: id, ArtifactBefore: before, Limit: limit}, &out)
	return out, err
}
func (c *conversationClient) CreateArtifact(ctx context.Context, in agentsdk.ConversationArtifactCreate, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	err := c.call(ctx, "artifacts_create", agentsdk.ConversationRPCRequest{Authority: a, ArtifactCreate: in}, &out)
	return out, err
}
func (c *conversationClient) EditArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactEdit, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactVersion, error) {
	var out agentsdk.ConversationArtifactVersion
	err := c.call(ctx, "artifacts_edit", agentsdk.ConversationRPCRequest{Authority: a, ArtifactID: id, ArtifactEdit: in}, &out)
	return out, err
}
func (c *conversationClient) ExportArtifact(ctx context.Context, id string, in agentsdk.ConversationArtifactExportRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactExport, error) {
	var out agentsdk.ConversationArtifactExport
	err := c.call(ctx, "artifacts_export", agentsdk.ConversationRPCRequest{Authority: a, ArtifactID: id, ArtifactExport: in}, &out)
	return out, err
}
func (c *conversationClient) DownloadArtifact(ctx context.Context, id string, a agentsdk.ConversationAuthority) (agentsdk.ConversationArtifactDownload, error) {
	var out agentsdk.ConversationArtifactDownload
	err := c.call(ctx, "artifacts_download", agentsdk.ConversationRPCRequest{Authority: a, ArtifactExportID: id}, &out)
	return out, err
}

var _ agentsdk.ConversationArtifactService = (*conversationClient)(nil)
