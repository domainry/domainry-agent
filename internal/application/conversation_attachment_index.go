package application

import (
	"context"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) attachmentKnowledgeBinding(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	return s.knowledgeService().AttachmentKnowledgeBinding(ctx, conversation, a)
}

func (s *ConversationService) IndexAttachment(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	return s.knowledgeService().IndexAttachment(ctx, conversation, id, expected, a)
}

func (s *ConversationService) wakeAttachmentIndex() { s.knowledgeService().WakeAttachmentIndex() }

func validateAttachmentKnowledge(repo persistence.ConversationRepository, options *ConversationOptions) error {
	if options.KnowledgeFactory == nil {
		if _, managed := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); managed {
			return fmt.Errorf("managed knowledge requires a factory supplied by composition")
		}
		if options.AttachmentStorage != nil || options.DocumentStorage != nil || options.ArtifactStorage != nil || options.LibraryAuthorizer != nil || len(options.LibraryKnowledge) > 0 || len(options.AttachmentKnowledge) > 0 || options.KnowledgeDatasources != nil {
			return fmt.Errorf("knowledge capabilities require a factory supplied by composition")
		}
		return nil
	}
	k := knowledgeOptions(*options)
	if err := options.KnowledgeFactory.Validate(repo, &k); err != nil {
		return err
	}
	options.AttachmentKnowledge = k.AttachmentKnowledge
	options.DocumentPoll = k.DocumentPoll
	options.ArtifactExportTTL = k.ArtifactExportTTL
	return nil
}
