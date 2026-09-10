package agent

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestKnowledgeAttachmentImportAtomicSourceCheckAndIndependentCopy(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	conv, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	att, err := repo.ReserveAttachment(ctx, attachmentReservation(conv.ID, "attachment"), a)
	if err != nil {
		t.Fatal(err)
	}
	att, err = repo.TransitionAttachment(ctx, att.Attachment.ID, att.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "stored", BodyRef: "source-original"}, a)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "shared", Kind: "shared", Name: "Shared"}, a)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.UserID = "second"
	lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, b.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "editor", ExpectedRevision: lib.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Repeat("f", 64)
	if err = repo.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}, source); err != nil {
		t.Fatal(err)
	}
	origin := persistence.KnowledgeAttachmentOrigin{ConversationID: conv.ID, AttachmentID: att.Attachment.ID, Revision: att.Attachment.Revision}
	in := persistence.KnowledgeDocumentReserve{AttachmentOrigin: &origin, LibraryID: lib.ID, ClientID: "copy", Filename: att.Attachment.Filename, ContentType: att.Attachment.ContentType, Bytes: att.Attachment.Bytes, SHA256: att.Attachment.SHA256, SourceID: source}
	if _, err = repo.ReserveKnowledgeDocument(ctx, in, b); err == nil {
		t.Fatal("target editor copied somebody else's private attachment")
	}
	changed := in
	changed.SHA256 = strings.Repeat("d", 64)
	_, err = repo.ReserveKnowledgeDocument(ctx, changed, a)
	requireConversationCode(t, err, "document_import_invalid")
	committed, err := repo.ReserveKnowledgeDocument(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	committed, err = repo.CommitKnowledgeDocumentContent(ctx, committed.Document.ID, committed.Document.Revision, "target-original", a)
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary upload and import receipts are separate namespaces.
	upload := in
	upload.AttachmentOrigin = nil
	separate, err := repo.ReserveKnowledgeDocument(ctx, upload, a)
	if err != nil || separate.Document.ID == committed.Document.ID {
		t.Fatal("upload collided with import", err)
	}
	in.ClientID = "interrupted"
	pending, err := repo.ReserveKnowledgeDocument(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.TransitionAttachment(ctx, att.Attachment.ID, att.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a); err != nil {
		t.Fatal(err)
	}
	_, err = repo.CommitKnowledgeDocumentContent(ctx, pending.Document.ID, pending.Document.Revision, "late-copy", a)
	requireConversationCode(t, err, "attachment_not_found")
	pending, err = repo.KnowledgeDocumentRecord(ctx, pending.Document.ID, a)
	if err != nil || pending.BodyRef != "" || pending.Document.State != "uploading" {
		t.Fatal("source deletion published late copy", err)
	}
	repo = NewConversationStore(store)
	prior, found, err := repo.FindKnowledgeAttachmentImport(ctx, lib.ID, "copy", origin, a)
	if err != nil || !found || prior.Document.ID != committed.Document.ID || prior.BodyRef != "target-original" {
		t.Fatal("restart lost independent copy receipt", err)
	}
	otherOrigin := origin
	otherOrigin.Revision++
	_, _, err = repo.FindKnowledgeAttachmentImport(ctx, lib.ID, "copy", otherOrigin, a)
	requireConversationCode(t, err, "idempotency_conflict")
	if _, found, err = repo.FindKnowledgeAttachmentImport(ctx, lib.ID, "copy", origin, b); err != nil || found {
		t.Fatal("receipt crossed user scope", err)
	}
	page, err := repo.KnowledgeDocuments(ctx, lib.ID, "", 20, b)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), conv.ID) || strings.Contains(string(raw), att.Attachment.ID) || strings.Contains(string(raw), "source-original") {
		t.Fatal("shared metadata exposed private source")
	}
}
