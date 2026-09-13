package agent

import (
	"reflect"
	"strings"
	"testing"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func TestPeerDocumentReferencesPersistAndDeduplicateWithTheirMessage(t *testing.T) {
	repo, a, d := peerFixture(t)
	ref := sdk.ConversationDocumentReference{LibraryID: "lib_" + strings.Repeat("a", 32), DocumentID: "kdoc_" + strings.Repeat("b", 32), Revision: 3, SHA256: strings.Repeat("c", 64)}
	in := sdk.ConversationAgentMessageSend{ClientID: "shared-file", ToAgentID: d.ToAgentID, Content: "明确共享的资料引用", BriefVersion: 1, Documents: []sdk.ConversationDocumentReference{ref}, ExecutionAgent: &sdk.ConversationAgentSnapshot{ID: d.ToAgentID}}
	message, err := repo.SendConversationAgentMessage(t.Context(), d.ID, in, d.SourceConversationID, a)
	if err != nil || message.FromAgentID != d.FromAgentID || !reflect.DeepEqual(message.Documents, in.Documents) {
		t.Fatalf("wrong reference or actor: %+v %v", message, err)
	}
	again, err := NewConversationStore(repo.store).SendConversationAgentMessage(t.Context(), d.ID, in, d.SourceConversationID, a)
	if err != nil || !reflect.DeepEqual(again, message) {
		t.Fatalf("replay changed message: %+v %v", again, err)
	}
	items, err := NewConversationStore(repo.store).ConversationPeerInbox(t.Context(), d.ConversationID, a)
	if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0].Documents, in.Documents) {
		t.Fatalf("reopen lost exact reference: %+v %v", items, err)
	}
	in.Documents[0].Revision++
	if _, err = repo.SendConversationAgentMessage(t.Context(), d.ID, in, d.SourceConversationID, a); err == nil {
		t.Fatal("same mutation ID replaced shared version")
	}
	in.ClientID = "private-attachment"
	in.Documents[0].DocumentID = "att_" + strings.Repeat("b", 32)
	if _, err = repo.SendConversationAgentMessage(t.Context(), d.ID, in, d.SourceConversationID, a); err == nil {
		t.Fatal("private attachment accepted as library reference")
	}
	other := a
	other.UserID = "other"
	if _, err = repo.ConversationAgentMessages(t.Context(), d.ID, other); err == nil {
		t.Fatal("references exposed across user ownership")
	}
}
