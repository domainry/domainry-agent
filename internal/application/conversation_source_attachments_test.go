package application

import (
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationToolSourceAttachmentsComeOnlyFromFrozenDirectUserImages(t *testing.T) {
	direct := &sdk.ConversationImageReference{
		ConversationID: "conversation-1", AttachmentID: "art_0123456789abcdef0123456789abcdef",
		Filename: "card.png", ContentType: "image/png", Bytes: 42,
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Revision: 2,
	}
	derived := *direct
	derived.AttachmentID = "art_abcdef0123456789abcdef0123456789"
	derived.Source = &sdk.ConversationRunReference{ConversationID: "source-conversation", RunID: "source-run"}
	messages := []sdk.ConversationStepMessage{
		{Role: "assistant", ContentBlocks: []sdk.ConversationContentBlock{{Type: "image", Image: direct}}},
		{Role: "user", ContentBlocks: []sdk.ConversationContentBlock{{Type: "image", Image: &derived}, {Type: "image", Image: direct}, {Type: "image", Image: direct}}},
	}
	attachments := appendConversationToolSourceAttachments(nil, map[string]bool{}, messages, "conversation-1")
	if len(attachments) != 1 || attachments[0].AttachmentID != direct.AttachmentID || attachments[0].Revision != direct.Revision || attachments[0].SHA256 != direct.SHA256 {
		t.Fatalf("attachments=%+v", attachments)
	}
}
