package application

import (
	"context"
	"fmt"
	"slices"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func validateAttachmentKnowledge(repo any, options *ConversationOptions) error {
	options.AttachmentKnowledge = slices.Clone(options.AttachmentKnowledge)
	if len(options.AttachmentKnowledge) == 0 {
		return nil
	}
	if _, ok := options.AttachmentAuthorizer.(agentsdk.ConversationAttachmentPermissionResolver); !ok {
		return fmt.Errorf("attachment knowledge requires live Identity permission resolution")
	}
	if _, ok := repo.(persistence.ConversationAttachmentIndexRepository); !ok {
		return fmt.Errorf("attachment knowledge requires durable index persistence")
	}
	workspaces, sources := map[string]bool{}, map[string]bool{}
	for _, b := range options.AttachmentKnowledge {
		if !conversationText(b.WorkspaceID, 255, true) || b.Knowledge == nil {
			return fmt.Errorf("invalid attachment knowledge Binding")
		}
		id := b.Knowledge.AttachmentKnowledgeSourceIdentity()
		if !validAttachmentSourceHash(id) || workspaces[b.WorkspaceID] || sources[id] {
			return fmt.Errorf("attachment sources must have unique workspace and physical knowledge base")
		}
		workspaces[b.WorkspaceID], sources[id] = true, true
		if defaultSource, ok := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); ok && defaultSource.KnowledgeDocumentSourceIdentity() == id {
			return fmt.Errorf("attachment and default knowledge must not share a physical knowledge base")
		}
		for _, library := range options.LibraryKnowledge {
			if source, ok := library.Source.(agentsdk.ManagedKnowledgeDocumentSource); ok && source.KnowledgeDocumentSourceIdentity() == id {
				return fmt.Errorf("attachment and library knowledge must not share a physical knowledge base")
			}
		}
	}
	return nil
}

func validAttachmentSourceHash(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Resolve only from trusted host bindings. Neither user requests nor model
// calls choose the physical KB, owner, private ACL or document request ID.
func (s *ConversationService) attachmentKnowledgeBinding(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	var zero agentsdk.ConversationAttachmentKnowledgeScope
	if err := s.authorize(a); err != nil {
		return zero, err
	}
	for _, b := range s.options.AttachmentKnowledge {
		if b.WorkspaceID != a.WorkspaceID {
			continue
		}
		resolver, ok := s.options.AttachmentAuthorizer.(agentsdk.ConversationAttachmentPermissionResolver)
		if !ok {
			return zero, conversationFailure("unavailable", "attachment_permission_resolver_unavailable")
		}
		permissions, err := resolver.ResolveConversationAttachmentPermissions(ctx, a)
		if err != nil {
			return zero, err
		}
		scope, err := b.Knowledge.ResolveAttachmentKnowledge(ctx, conversation, a, permissions)
		if err != nil {
			return zero, err
		}
		if scope.Source == nil || scope.Source.KnowledgeDocumentManagementReady() != nil || scope.Source.KnowledgeDocumentSourceIdentity() != b.Knowledge.AttachmentKnowledgeSourceIdentity() || !validAttachmentSourceHash(scope.Source.KnowledgeDocumentAccessPolicySHA256()) || !validAttachmentPermissionIDs(scope.DocumentPermissionIDs, 1) || !validAttachmentPermissionIDs(scope.ReadPermissionIDs, 100) || DocumentIndexMaximumBytes(scope.Source) < 1 {
			return zero, conversationFailure("unavailable", "attachment_source_invalid")
		}
		return scope, nil
	}
	return zero, conversationFailure("unavailable", "attachment_index_unavailable")
}

func validAttachmentPermissionIDs(ids []string, limit int) bool {
	if len(ids) == 0 || len(ids) > limit || !slices.IsSorted(ids) {
		return false
	}
	for i, id := range ids {
		if !conversationText(id, 128, true) || i > 0 && ids[i-1] == id {
			return false
		}
	}
	return true
}

func (s *ConversationService) IndexAttachment(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	var zero agentsdk.ConversationAttachment
	records, err := s.attachmentAccess(ctx, "attachments_index", a)
	if err != nil {
		return zero, err
	}
	repo, ok := s.repo.(persistence.ConversationAttachmentIndexRepository)
	if !ok {
		return zero, conversationFailure("unavailable", "attachment_index_unavailable")
	}
	r, err := s.attachmentRecord(ctx, records, conversation, id, a)
	if err != nil {
		return zero, err
	}
	if _, err = s.attachmentAccess(ctx, "attachments_download", a); err != nil {
		return zero, err
	}
	scope, err := s.attachmentKnowledgeBinding(ctx, conversation, a)
	if err != nil {
		return zero, err
	}
	if maximum := DocumentIndexMaximumBytesForFilename(scope.Source, r.Attachment.Filename); maximum < 1 || r.Attachment.Bytes > maximum {
		return zero, conversationFailure("bad_request", "attachment_size_invalid")
	}
	source := persistence.ConversationAttachmentSource{Identity: scope.Source.KnowledgeDocumentSourceIdentity(), DocumentPermissionIDs: slices.Clone(scope.DocumentPermissionIDs), AccessPolicySHA256: scope.Source.KnowledgeDocumentAccessPolicySHA256()}
	if err = repo.ActivateAttachmentKnowledgeSource(ctx, a.RuntimeID, a.WorkspaceID, source.Identity); err != nil {
		return zero, err
	}
	if _, err = s.attachmentAccess(ctx, "attachments_index", a); err != nil {
		return zero, err
	}
	r, err = repo.QueueAttachmentIndex(ctx, id, expected, source, a)
	if err != nil {
		return zero, err
	}
	s.wakeAttachmentIndex()
	return s.attachmentIndexView(ctx, r, a), nil
}

func (s *ConversationService) wakeAttachmentIndex() {
	select {
	case s.attachmentIndexWake <- struct{}{}:
	default:
	}
}

var _ agentsdk.ConversationAttachmentIndexService = (*ConversationService)(nil)
