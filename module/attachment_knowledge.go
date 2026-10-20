package module

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgeprovider "github.com/domainry/domainry-knowledge-sdk/provider"
)

func attachmentKnowledgeEnvironment(raw string) ([]KnowledgeConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("attachment knowledge configuration is too large")
	}
	var input []struct {
		WorkspaceID     string                    `json:"workspace_id"`
		BaseURL         string                    `json:"base_url"`
		TeamID          string                    `json:"team_id"`
		KBID            string                    `json:"kb_id"`
		APIKeyEnv       string                    `json:"api_key_env"`
		TopK            int                       `json:"top_k"`
		ResponseMapping *KnowledgeResponseMapping `json:"response_mapping"`
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || len(input) == 0 || len(input) > 1000 {
		return nil, fmt.Errorf("invalid attachment knowledge binding JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid trailing attachment knowledge binding JSON")
	}
	keys := regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	out := make([]KnowledgeConfig, 0, len(input))
	for _, in := range input {
		if !keys.MatchString(in.APIKeyEnv) {
			return nil, fmt.Errorf("attachment sources require an API key environment variable name")
		}
		out = append(out, KnowledgeConfig{BaseURL: in.BaseURL, TeamID: in.TeamID, KBID: in.KBID, WorkspaceID: in.WorkspaceID, APIKey: os.Getenv(in.APIKeyEnv), TopK: in.TopK, ResponseMapping: in.ResponseMapping})
	}
	return out, nil
}

func assembleAttachmentKnowledge(options *ConversationOptions, configured []KnowledgeConfig, raw, runtime string, factory knowledgeprovider.Factory) error {
	if raw != "" {
		if len(configured) > 0 {
			return fmt.Errorf("configure attachment knowledge only once")
		}
		var err error
		configured, err = attachmentKnowledgeEnvironment(raw)
		if err != nil {
			return err
		}
	}
	if len(configured) > 1000 || len(configured) > 0 && len(options.AttachmentKnowledge) > 0 {
		return fmt.Errorf("configure attachment sources using one binding mechanism")
	}
	for _, c := range configured {
		source, err := newAttachmentKnowledge(c, runtime, factory)
		if err != nil {
			return err
		}
		options.AttachmentKnowledge = append(options.AttachmentKnowledge, agentsdk.ConversationAttachmentKnowledgeBinding{WorkspaceID: strings.TrimSpace(c.WorkspaceID), Knowledge: source})
	}
	used := map[string]bool{}
	if source, ok := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); ok {
		used[source.KnowledgeDocumentSourceIdentity()] = true
	}
	for _, b := range options.LibraryKnowledge {
		if source, ok := b.Source.(agentsdk.ManagedKnowledgeDocumentSource); ok {
			used[source.KnowledgeDocumentSourceIdentity()] = true
		}
	}
	if catalog, ok := options.KnowledgeDatasources.(*knowledgeDatasourceCatalog); ok {
		for _, entry := range catalog.entries {
			used[entry.definition.SourceID] = true
		}
	}
	workspaces := map[string]bool{}
	for _, b := range options.AttachmentKnowledge {
		if b.Knowledge == nil || strings.TrimSpace(b.WorkspaceID) == "" || workspaces[b.WorkspaceID] {
			return fmt.Errorf("each workspace requires a unique attachment knowledge binding")
		}
		id := b.Knowledge.AttachmentKnowledgeSourceIdentity()
		if used[id] {
			return fmt.Errorf("attachment KB cannot be reused by a default, library, catalog or another attachment binding")
		}
		workspaces[b.WorkspaceID], used[id] = true, true
	}
	return nil
}

type attachmentKnowledge struct {
	config            KnowledgeConfig
	runtime, identity string
	factory           knowledgeprovider.Factory
}

func newAttachmentKnowledge(config KnowledgeConfig, runtime string, factory knowledgeprovider.Factory) (*attachmentKnowledge, error) {
	if strings.TrimSpace(runtime) == "" || len(runtime) > 255 || config.PermissionIDs != nil || config.AuthorizeWorkspace != nil || config.DocumentPermissionIDs != nil || config.ReadPermissionIDs != nil {
		return nil, fmt.Errorf("attachment knowledge owns its private runtime/workspace/user/conversation policy")
	}
	config.DocumentManagement = true
	config.DocumentPermissionIDs = []string{"scope:agent:attachment:configuration"}
	source, err := factory.NewSource(config)
	managed, ok := source.(agentsdk.ConversationAttachmentKnowledgeSource)
	if err != nil || !ok || managed == nil || managed.KnowledgeDocumentManagementReady() != nil {
		return nil, fmt.Errorf("invalid attachment knowledge configuration")
	}
	return &attachmentKnowledge{config: config, runtime: runtime, identity: managed.KnowledgeDocumentSourceIdentity(), factory: factory}, nil
}

func (k *attachmentKnowledge) AttachmentKnowledgeSourceIdentity() string { return k.identity }

var attachmentConversationID = regexp.MustCompile(`^conv_[a-f0-9]{32}$`)

func (k *attachmentKnowledge) ResolveAttachmentKnowledge(ctx context.Context, conversation string, authority agentsdk.ConversationAuthority, permissions agentsdk.ConversationAttachmentPermissionScope) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	var zero agentsdk.ConversationAttachmentKnowledgeScope
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !authority.Known || authority.RuntimeID != k.runtime || authority.WorkspaceID != k.config.WorkspaceID || strings.TrimSpace(authority.UserID) == "" || len(authority.UserID) > 255 || !attachmentConversationID.MatchString(conversation) || len(permissions.OrganizationIDs) > 96 {
		return zero, attachmentKnowledgeFailure()
	}
	organizations := slices.Clone(permissions.OrganizationIDs)
	for _, id := range organizations {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(id) != id || len(id) > 255 || strings.ContainsAny(id, "\x00\r\n") {
			return zero, attachmentKnowledgeFailure()
		}
	}
	slices.Sort(organizations)
	organizations = slices.Compact(organizations)
	permissionID := func(kind string, values ...string) string {
		raw, _ := json.Marshal(append([]string{"attachment_permission.v1", kind}, values...))
		return fmt.Sprintf("scope:agent:attachment:%x", sha256.Sum256(raw))
	}
	conversationPermission := permissionID("conversation", authority.RuntimeID, authority.WorkspaceID, authority.UserID, conversation)
	readPermissions := []string{
		conversationPermission,
		permissionID("user", authority.RuntimeID, authority.WorkspaceID, authority.UserID),
		permissionID("workspace", authority.RuntimeID, authority.WorkspaceID),
	}
	for _, organization := range organizations {
		readPermissions = append(readPermissions, permissionID("organization", authority.RuntimeID, authority.WorkspaceID, organization))
	}
	slices.Sort(readPermissions)
	readPermissions = slices.Compact(readPermissions)
	config := k.config
	config.DocumentPermissionIDs = []string{conversationPermission}
	config.ReadPermissionIDs = readPermissions
	source, err := k.factory.NewSource(config)
	base, ok := source.(agentsdk.ConversationAttachmentKnowledgeSource)
	if err != nil || !ok || base == nil {
		return zero, fmt.Errorf("resolve attachment knowledge source: %w", err)
	}
	return agentsdk.ConversationAttachmentKnowledgeScope{
		Source:                &authorizedAttachmentKnowledgeSource{base: base, authority: authority},
		DocumentPermissionIDs: slices.Clone(config.DocumentPermissionIDs),
		ReadPermissionIDs:     slices.Clone(config.ReadPermissionIDs),
	}, nil
}

func attachmentKnowledgeFailure() error {
	return &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.knowledge_access_denied"}
}

type authorizedAttachmentKnowledgeSource struct {
	base      agentsdk.ConversationAttachmentKnowledgeSource
	authority agentsdk.ConversationAuthority
}

func (s *authorizedAttachmentKnowledgeSource) authorize(authority agentsdk.ConversationAuthority) error {
	if !authority.Known || authority.RuntimeID != s.authority.RuntimeID || authority.WorkspaceID != s.authority.WorkspaceID || authority.UserID != s.authority.UserID {
		return attachmentKnowledgeFailure()
	}
	return nil
}

func (s *authorizedAttachmentKnowledgeSource) KnowledgeDocumentSourceIdentity() string {
	return s.base.KnowledgeDocumentSourceIdentity()
}
func (s *authorizedAttachmentKnowledgeSource) KnowledgeDocumentAccessPolicySHA256() string {
	return s.base.KnowledgeDocumentAccessPolicySHA256()
}
func (s *authorizedAttachmentKnowledgeSource) KnowledgeDocumentIndexMaximumBytes() int64 {
	return s.base.KnowledgeDocumentIndexMaximumBytes()
}
func (s *authorizedAttachmentKnowledgeSource) KnowledgeDocumentFormats() []agentsdk.KnowledgeDocumentFormat {
	if source, ok := s.base.(agentsdk.KnowledgeDocumentFormatSource); ok {
		return source.KnowledgeDocumentFormats()
	}
	return nil
}
func (s *authorizedAttachmentKnowledgeSource) KnowledgeDocumentManagementReady() error {
	return s.base.KnowledgeDocumentManagementReady()
}
func (s *authorizedAttachmentKnowledgeSource) PutKnowledgeDocument(ctx context.Context, in agentsdk.KnowledgeDocumentIndexContent, authority agentsdk.ConversationAuthority) error {
	if err := s.authorize(authority); err != nil {
		return err
	}
	return s.base.PutKnowledgeDocument(ctx, in, authority)
}
func (s *authorizedAttachmentKnowledgeSource) InspectKnowledgeDocument(ctx context.Context, id string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentState, error) {
	if err := s.authorize(authority); err != nil {
		return agentsdk.KnowledgeDocumentState{}, err
	}
	return s.base.InspectKnowledgeDocument(ctx, id, authority)
}
func (s *authorizedAttachmentKnowledgeSource) DeleteKnowledgeDocument(ctx context.Context, id string, authority agentsdk.ConversationAuthority) error {
	if err := s.authorize(authority); err != nil {
		return err
	}
	return s.base.DeleteKnowledgeDocument(ctx, id, authority)
}
func (s *authorizedAttachmentKnowledgeSource) RecoverKnowledgeDocumentDelete(ctx context.Context, id string, authority agentsdk.ConversationAuthority) error {
	if err := s.authorize(authority); err != nil {
		return err
	}
	recovery, ok := s.base.(agentsdk.KnowledgeDocumentDeleteRecoverySource)
	if !ok {
		return fmt.Errorf("attachment knowledge deletion recovery is unavailable")
	}
	return recovery.RecoverKnowledgeDocumentDelete(ctx, id, authority)
}
func (s *authorizedAttachmentKnowledgeSource) SearchKnowledgeDocumentPassages(ctx context.Context, query string, authority agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	if err := s.authorize(authority); err != nil {
		return nil, err
	}
	return s.base.SearchKnowledgeDocumentPassages(ctx, query, authority)
}
func (s *authorizedAttachmentKnowledgeSource) SearchKnowledgeDocuments(ctx context.Context, query string, authority agentsdk.ConversationAuthority) (agentsdk.KnowledgeDocumentSearch, error) {
	if err := s.authorize(authority); err != nil {
		return agentsdk.KnowledgeDocumentSearch{}, err
	}
	search, ok := s.base.(agentsdk.ManagedKnowledgeDocumentSearchSource)
	if !ok {
		passages, err := s.base.SearchKnowledgeDocumentPassages(ctx, query, authority)
		return agentsdk.KnowledgeDocumentSearch{Passages: passages}, err
	}
	return search.SearchKnowledgeDocuments(ctx, query, authority)
}
func (s *authorizedAttachmentKnowledgeSource) ReadKnowledgeDocumentPassages(ctx context.Context, id string, authority agentsdk.ConversationAuthority) ([]agentsdk.KnowledgeDocumentPassage, error) {
	if err := s.authorize(authority); err != nil {
		return nil, err
	}
	return s.base.ReadKnowledgeDocumentPassages(ctx, id, authority)
}

var _ agentsdk.ConversationAttachmentKnowledge = (*attachmentKnowledge)(nil)
var _ agentsdk.ConversationAttachmentKnowledgeSource = (*authorizedAttachmentKnowledgeSource)(nil)
var _ agentsdk.ManagedKnowledgeDocumentSearchSource = (*authorizedAttachmentKnowledgeSource)(nil)
var _ agentsdk.KnowledgeDocumentDeleteRecoverySource = (*authorizedAttachmentKnowledgeSource)(nil)
