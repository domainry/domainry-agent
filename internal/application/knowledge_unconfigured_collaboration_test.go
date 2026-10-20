package application

import (
	"context"
	"errors"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	knowledgesdk "github.com/domainry/domainry-knowledge-sdk"
	knowledge "github.com/domainry/domainry-knowledge-sdk/contract"
)

var _ knowledge.Service = unconfiguredKnowledge{}

func TestUnconfiguredKnowledgeCollaborationFailsClosed(t *testing.T) {
	ctx := context.Background()
	service := unconfiguredKnowledge{}
	authority := agentsdk.ConversationAuthority{}
	cases := map[string]func() error{
		"connect": func() error {
			_, err := service.ConnectKnowledgeCollaboration(ctx, "library", "page", knowledgesdk.KnowledgeCollaborationConnect{}, authority)
			return err
		},
		"sync": func() error {
			_, err := service.SyncKnowledgeCollaboration(ctx, "library", "page", knowledgesdk.KnowledgeCollaborationSync{}, authority)
			return err
		},
		"close": func() error {
			return service.CloseKnowledgeCollaboration(ctx, "library", "page", "session", authority)
		},
		"publish": func() error {
			_, err := service.PublishKnowledgeCollaboration(ctx, "library", "page", knowledgesdk.KnowledgeCollaborationPublish{}, authority)
			return err
		},
		"read access": func() error {
			_, err := service.KnowledgePageAccess(ctx, "library", "page", authority)
			return err
		},
		"write access": func() error {
			_, err := service.SetKnowledgePageAccess(ctx, "library", "page", knowledgesdk.KnowledgePageAccessWrite{}, authority)
			return err
		},
		"preview move": func() error {
			_, err := service.PreviewKnowledgePageMove(ctx, "library", "page", knowledgesdk.KnowledgePageMovePreview{}, authority)
			return err
		},
	}
	for name, invoke := range cases {
		t.Run(name, func(t *testing.T) {
			var failure *agentsdk.Error
			if err := invoke(); !errors.As(err, &failure) || failure.Class != "unavailable" || failure.Code != "agent.conversation.knowledge_not_configured" {
				t.Fatalf("expected Knowledge unavailable, got %v", err)
			}
		})
	}
}
