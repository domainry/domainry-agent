package agent

import (
	"strconv"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentartifact "github.com/domainry/domainry-agent-sdk/artifact"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestConversationAttachmentsUsePhysicalIDCursor(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "attachment-cursor-conversation"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	reserve := func(client, filename, content string) persistence.ConversationAttachmentRecord {
		t.Helper()
		body := []byte(content)
		record, reserveErr := repository.ReserveAttachment(t.Context(), persistence.ConversationAttachmentReserve{
			ClientID: client, ConversationID: conversation.ID, Filename: filename, ContentType: "text/plain",
			SHA256: agentartifact.Hash(body), Bytes: int64(len(body)), Content: body,
		}, authority)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		return record
	}
	first := reserve("attachment-cursor-1", "first.txt", "first")
	second := reserve("attachment-cursor-2", "second.txt", "second")

	firstPage, err := repository.Attachments(t.Context(), conversation.ID, "", 1, authority)
	if err != nil || len(firstPage.Items) != 1 || firstPage.Items[0].ID != first.Attachment.ID || firstPage.Complete {
		t.Fatalf("first attachment page mismatch: page=%+v err=%v", firstPage, err)
	}
	physicalID, parseErr := strconv.ParseInt(firstPage.NextAfter, 10, 64)
	if parseErr != nil || physicalID < 1 || firstPage.NextAfter == first.Attachment.ID {
		t.Fatalf("attachment cursor is not a positive physical id: cursor=%q business_id=%q err=%v", firstPage.NextAfter, first.Attachment.ID, parseErr)
	}
	secondPage, err := repository.Attachments(t.Context(), conversation.ID, firstPage.NextAfter, 1, authority)
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID != second.Attachment.ID || !secondPage.Complete || secondPage.NextAfter != "" {
		t.Fatalf("second attachment page mismatch: page=%+v err=%v", secondPage, err)
	}
	if _, err = repository.Attachments(t.Context(), conversation.ID, first.Attachment.ID, 1, authority); err == nil {
		t.Fatal("business attachment id was accepted as a physical cursor")
	}
}
