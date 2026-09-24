package application

import (
	"context"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
)

// Compatibility forwarding; business implementation is owned by Knowledge.
func (s *ConversationService) attachmentKnowledgeBinding(ctx context.Context, conversation string, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachmentKnowledgeScope, error) {
	return s.knowledgeService().AttachmentKnowledgeBinding(ctx, conversation, a)
}

func (s *ConversationService) IndexAttachment(ctx context.Context, conversation, id string, expected int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationAttachment, error) {
	return s.knowledgeService().IndexAttachment(ctx, conversation, id, expected, a)
}

func (s *ConversationService) wakeAttachmentIndex() { s.knowledgeService().WakeAttachmentIndex() }

func validateAttachmentKnowledge(options *ConversationOptions) error {
	if options.KnowledgeRuntime == nil {
		if _, managed := options.Knowledge.(agentsdk.ManagedKnowledgeDocumentSource); managed {
			return fmt.Errorf("managed knowledge requires a factory supplied by composition")
		}
		if options.DocumentStorage != nil || options.ArtifactStorage != nil || options.LibraryAuthorizer != nil || len(options.LibraryKnowledge) > 0 || len(options.AttachmentKnowledge) > 0 || options.KnowledgeDatasources != nil {
			return fmt.Errorf("knowledge capabilities require a factory supplied by composition")
		}
		return nil
	}
	k := knowledgeOptions(*options)
	if err := options.KnowledgeRuntime.Validate(&k); err != nil {
		return err
	}
	options.AttachmentKnowledge = k.AttachmentKnowledge
	options.DocumentPoll = k.DocumentPoll
	options.ArtifactExportTTL = k.ArtifactExportTTL
	return nil
}
