package agent

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func storedImageBlock() agentsdk.ConversationContentBlock {
	return agentsdk.ConversationContentBlock{Type: "image", Image: &agentsdk.ConversationImageReference{
		AttachmentID: "att_0123456789abcdef0123456789abcdef", ConversationID: "conv_one", Filename: "proof.png",
		ContentType: "image/png", Bytes: 68, SHA256: strings.Repeat("a", 64), Revision: 1, Detail: "auto",
	}}
}

func TestValidStoredConversationContentKeepsBytesOutAndBindsSources(t *testing.T) {
	direct := storedImageBlock()
	if !validStoredConversationContent("user", "describe", []agentsdk.ConversationContentBlock{{Type: "text", Text: "describe"}, direct}, "conv_one", false) {
		t.Fatal("valid direct image rejected")
	}
	withBytes := direct
	withBytes.Image = new(agentsdk.ConversationImageReference)
	*withBytes.Image = *direct.Image
	withBytes.Image.Data = []byte("private")
	if validStoredConversationContent("user", "", []agentsdk.ConversationContentBlock{withBytes}, "conv_one", false) {
		t.Fatal("durable image bytes accepted")
	}
	sourced := direct
	sourced.Image = new(agentsdk.ConversationImageReference)
	*sourced.Image = *direct.Image
	sourced.Image.Source = &agentsdk.ConversationRunReference{ConversationID: "conv_one", RunID: "run_one", BeforeStep: 2}
	if !validStoredConversationContent("user", "", []agentsdk.ConversationContentBlock{sourced}, "receiver", true) {
		t.Fatal("valid sourced image rejected")
	}
	sourced.Image.Source.ConversationID = "other"
	if validStoredConversationContent("user", "", []agentsdk.ConversationContentBlock{sourced}, "receiver", true) {
		t.Fatal("image accepted with mismatched source conversation")
	}
}

func TestValidStoredConversationContentRejectsImageCapabilityAndRoleBypass(t *testing.T) {
	image := storedImageBlock()
	if validStoredConversationContent("assistant", "", []agentsdk.ConversationContentBlock{image}, "conv_one", false) {
		t.Fatal("assistant image accepted")
	}
	messages := []agentsdk.ConversationModelMessage{{Role: "user", ContentBlocks: []agentsdk.ConversationContentBlock{image}}}
	if !validStoredConversationModelMessages(messages, "conv_one") || !storedConversationModelMessagesHaveImages(messages) {
		t.Fatal("image model message was not recognized")
	}
}
