package agent

import (
	"bytes"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

func TestAgentSubjectLifecycleErasesOwnedExecutionGraphOnly(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	a.UserID = "alice"
	b := a
	b.UserID = "bob"
	alice, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "alice-conversation", Title: "Alice"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), alice.ID, agentsdk.ConversationSend{ClientMessageID: "alice-message", Message: "Alice execution secret"}, a); err != nil {
		t.Fatal(err)
	}
	bob, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bob-conversation", Title: "Bob"}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), bob.ID, agentsdk.ConversationSend{ClientMessageID: "bob-message", Message: "Bob execution secret"}, b); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "prefs:alice.v1", Title: "Alice preference", Content: "private", Enabled: true}, a); err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.ReserveAttachment(t.Context(), persistence.ConversationAttachmentReserve{ClientID: "alice-attachment", ConversationID: alice.ID, Filename: "private.txt", ContentType: "text/plain", SHA256: strings.Repeat("a", 64), Bytes: 7}, a)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSubjectLifecycle(store, a.RuntimeID)
	preview, err := lifecycle.PreviewSubject(t.Context(), a.WorkspaceID, a.UserID)
	if err != nil || !bytes.Contains(preview, []byte(`"_agent_conversations":1`)) || !bytes.Contains(preview, []byte(`"_agent_conversation_runs":1`)) || !bytes.Contains(preview, []byte(`"_agent_conversation_messages":1`)) || !bytes.Contains(preview, []byte(`"_agent_user_memories":1`)) {
		t.Fatalf("preview=%s err=%v", preview, err)
	}
	exported, err := lifecycle.ExportSubjectForRequest(t.Context(), "export", a.WorkspaceID, a.UserID)
	if err != nil || !bytes.Contains(exported, []byte("Alice execution secret")) || bytes.Contains(exported, []byte("Bob execution secret")) {
		t.Fatalf("export=%s err=%v", exported, err)
	}
	if _, err = lifecycle.EraseSubjectForRequest(t.Context(), "held", a.WorkspaceID, a.UserID, []lifecyclemodel.LegalHold{{ID: "hold"}}); err == nil {
		t.Fatal("legal hold did not block Agent erasure")
	}
	receipt, err := lifecycle.EraseSubjectForRequest(t.Context(), "erase-alice", a.WorkspaceID, a.UserID, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := lifecycle.EraseSubjectForRequest(t.Context(), "erase-alice", a.WorkspaceID, a.UserID, nil)
	if err != nil || !bytes.Equal(receipt, replayed) {
		t.Fatalf("receipt replay=%s err=%v", replayed, err)
	}
	if _, err = repo.Get(t.Context(), alice.ID, a); err == nil {
		t.Fatal("Alice conversation survived")
	}
	if memories, err := repo.Memories(t.Context(), a); err != nil || len(memories) != 0 {
		t.Fatalf("Alice memories=%+v err=%v", memories, err)
	}
	if _, err = repo.Get(t.Context(), bob.ID, b); err != nil {
		t.Fatal("Bob conversation was removed", err)
	}
	if record, err := repo.AttachmentRecord(t.Context(), attachment.Attachment.ID, a); err != nil || record.Attachment.ID != attachment.Attachment.ID {
		t.Fatalf("Agent mutated Knowledge-owned attachment=%+v err=%v", record, err)
	}
}
