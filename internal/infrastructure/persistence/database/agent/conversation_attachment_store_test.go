package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func attachmentReservation(c, client string) persistence.ConversationAttachmentReserve {
	return persistence.ConversationAttachmentReserve{ClientID: client, ConversationID: c, Filename: "合同.pdf", ContentType: "application/pdf", SHA256: strings.Repeat("a", 64), Bytes: 2048}
}

func deleteConversationAcrossOwners(t *testing.T, repo *ConversationStore, conversation agentsdk.Conversation, a agentsdk.ConversationAuthority) {
	t.Helper()
	requestID := "test-conversation-delete:" + conversationHash([]any{conversationOwner(a), conversation.ID, conversation.Revision})
	agentReceipt, err := repo.DeleteForRequest(t.Context(), requestID, conversation.ID, conversation.Revision, a)
	if err != nil {
		t.Fatal(err)
	}
	knowledgeReceipt, err := repo.DeleteConversationReferencesForRequest(t.Context(), requestID, conversation.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	replayedAgent, err := repo.DeleteForRequest(t.Context(), requestID, conversation.ID, conversation.Revision, a)
	if err != nil || string(replayedAgent) != string(agentReceipt) {
		t.Fatal("Agent deletion receipt did not replay", err)
	}
	replayedKnowledge, err := repo.DeleteConversationReferencesForRequest(t.Context(), requestID, conversation.ID, a)
	if err != nil || string(replayedKnowledge) != string(knowledgeReceipt) {
		t.Fatal("Knowledge deletion receipt did not replay", err)
	}
}

func TestAttachmentReservationStateFencesAndOwnerIsolation(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "attachment-conversation"}, a)
	if err != nil {
		t.Fatal(err)
	}
	in := attachmentReservation(c.ID, "upload")
	var wg sync.WaitGroup
	results := make(chan persistence.ConversationAttachmentRecord, 4)
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := repo.ReserveAttachment(ctx, in, a)
			results <- value
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var current persistence.ConversationAttachmentRecord
	for value := range results {
		if current.Attachment.ID != "" && current.Attachment.ID != value.Attachment.ID {
			t.Fatal("duplicate reservation")
		}
		current = value
	}
	if current.Attachment.Visibility != "conversation_private" || current.Attachment.State != "uploading" || current.Source != nil {
		t.Fatal(current)
	}
	changed := in
	changed.Filename = "different.pdf"
	_, err = repo.ReserveAttachment(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}} {
		if _, err := repo.ReserveAttachment(ctx, in, other); err == nil {
			t.Fatal("cross-owner reserve")
		}
		if _, err := repo.AttachmentRecord(ctx, current.Attachment.ID, other); err == nil {
			t.Fatal("cross-owner read")
		}
		if _, err := repo.Attachments(ctx, c.ID, "", 10, other); err == nil {
			t.Fatal("cross-owner list")
		}
		if _, err := repo.TransitionAttachment(ctx, current.Attachment.ID, 1, persistence.ConversationAttachmentTransition{State: "deleting"}, other); err == nil {
			t.Fatal("cross-owner delete")
		}
	}
	transition := func(in persistence.ConversationAttachmentTransition) {
		t.Helper()
		value, err := repo.TransitionAttachment(ctx, current.Attachment.ID, current.Attachment.Revision, in, a)
		if err != nil {
			t.Fatal(err)
		}
		current = value
	}
	_, err = repo.TransitionAttachment(ctx, current.Attachment.ID, 1, persistence.ConversationAttachmentTransition{State: "ready"}, a)
	requireConversationCode(t, err, "attachment_transition_invalid")
	transition(persistence.ConversationAttachmentTransition{State: "stored", BodyRef: "private-file-ref"})
	_, err = repo.TransitionAttachment(ctx, current.Attachment.ID, 1, persistence.ConversationAttachmentTransition{State: "failed", ErrorCode: "upload_failed"}, a)
	requireConversationCode(t, err, "revision_conflict")
	_, err = repo.TransitionAttachment(ctx, current.Attachment.ID, current.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "indexing", Source: &persistence.ConversationAttachmentSource{Identity: "knowledge", DocID: "private-doc"}}, a)
	requireConversationCode(t, err, "attachment_source_invalid")
	transition(persistence.ConversationAttachmentTransition{State: "indexing", Source: &persistence.ConversationAttachmentSource{Identity: "knowledge", DocID: "private-doc", PermissionID: "user:workspace:user"}})
	_, err = repo.TransitionAttachment(ctx, current.Attachment.ID, current.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "failed", ErrorCode: "upstream body: do not expose this"}, a)
	requireConversationCode(t, err, "attachment_transition_invalid")
	transition(persistence.ConversationAttachmentTransition{State: "failed", ErrorCode: "index_unavailable"})
	transition(persistence.ConversationAttachmentTransition{State: "indexing"})
	transition(persistence.ConversationAttachmentTransition{State: "ready"})
	repo = NewConversationStore(store)
	replay, err := repo.ReserveAttachment(ctx, in, a)
	if err != nil || replay.Attachment.Revision != current.Attachment.Revision || replay.Attachment.State != "ready" {
		t.Fatal("replay reset state", replay, err)
	}
	page, err := repo.Attachments(ctx, c.ID, "", 1, a)
	raw, _ := json.Marshal(page)
	if err != nil || len(page.Items) != 1 || strings.Contains(string(raw), "private-doc") || strings.Contains(string(raw), "private-file-ref") || strings.Contains(string(raw), "user:workspace:user") {
		t.Fatal("public projection leaked host references", err)
	}
	transition(persistence.ConversationAttachmentTransition{State: "deleting"})
	page, err = repo.Attachments(ctx, c.ID, "", 1, a)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("deleting attachment visible", err)
	}
	transition(persistence.ConversationAttachmentTransition{State: "deleted"})
	replay, err = repo.ReserveAttachment(ctx, in, a)
	if err != nil || replay.Attachment.State != "deleted" {
		t.Fatal("replay resurrected attachment", err)
	}
	_, err = repo.TransitionAttachment(ctx, current.Attachment.ID, current.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "uploading"}, a)
	requireConversationCode(t, err, "attachment_transition_invalid")
}

func TestAttachmentParentDeletionUsesOwnerReceiptsAndKeepsCleanupReferences(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "parent"}, a)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.ReserveAttachment(ctx, attachmentReservation(c.ID, "first"), a)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.TransitionAttachment(ctx, first.Attachment.ID, 1, persistence.ConversationAttachmentTransition{State: "stored", BodyRef: "retain-for-cleanup"}, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ReserveAttachment(ctx, attachmentReservation(c.ID, "second"), a)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.Attachments(ctx, c.ID, "", 1, a)
	if err != nil || page.Complete || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	next, err := repo.Attachments(ctx, c.ID, page.NextAfter, 1, a)
	if err != nil || !next.Complete || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, err)
	}
	// SQLite test-only fault injection: Agent must roll back its own deletion
	// receipt. Knowledge remains untouched until its public owner request runs.
	_, err = store.Database().ExecContext(ctx, `CREATE TRIGGER fail_attachment_parent_delete BEFORE DELETE ON _agent_conversations BEGIN SELECT RAISE(ABORT, 'injected parent deletion failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, c.ID, c.Revision, a); err == nil {
		t.Fatal("fault injection did not fire")
	}
	still, err := repo.AttachmentRecord(ctx, first.Attachment.ID, a)
	if err != nil || still.Attachment.State != "stored" {
		t.Fatal("partial deletion committed", still, err)
	}
	if _, err = store.Database().ExecContext(ctx, `DROP TRIGGER fail_attachment_parent_delete`); err != nil {
		t.Fatal(err)
	}
	deleteConversationAcrossOwners(t, repo, c, a)
	for _, id := range []string{first.Attachment.ID, second.Attachment.ID} {
		row, err := repo.AttachmentRecord(ctx, id, a)
		if err != nil || row.Attachment.State != "deleting" {
			t.Fatal("cleanup reference lost", err)
		}
		if id == first.Attachment.ID && row.BodyRef != "retain-for-cleanup" {
			t.Fatal("file reference lost")
		}
		if _, err := repo.TransitionAttachment(ctx, id, row.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, a); err != nil {
			t.Fatal("cleanup must finish after parent deletion", err)
		}
	}
	_, err = repo.TransitionAttachment(ctx, first.Attachment.ID, stored.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "indexing"}, a)
	requireConversationCode(t, err, "revision_conflict")
	if _, err := repo.Attachments(ctx, c.ID, "", 10, a); err == nil {
		t.Fatal("deleted conversation remained readable")
	}
}

func TestAttachmentValidationAndQuotaIncludeUnfinishedUploads(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "quota"}, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"../file.pdf", `C:\private.pdf`, "bad\nname.pdf", ".", ""} {
		in := attachmentReservation(c.ID, "bad")
		in.Filename = filename
		_, err := repo.ReserveAttachment(ctx, in, a)
		requireConversationCode(t, err, "attachment_invalid")
	}
	for i := 0; i < 16; i++ {
		in := attachmentReservation(c.ID, fmt.Sprintf("large-%d", i))
		in.Bytes = agentsdk.ConversationAttachmentMaxBytes
		if _, err := repo.ReserveAttachment(ctx, in, a); err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.ReserveAttachment(ctx, attachmentReservation(c.ID, "over-limit"), a)
	requireConversationCode(t, err, "attachment_limit")
}

func TestAttachmentCleanupSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attachments.db")
	open := func(migrate bool) (*ConversationStore, *sql.DB) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		if migrate {
			migrations, err := SchemaMigrations("sqlite", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, migration := range migrations {
				for _, statement := range migration.Statements {
					if _, err = db.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		dialect, _ := ormdialect.New(ormdialect.SQLite)
		store, err := NewStore(db, dialect.WithSchema(""), sqlite.NewEngine())
		if err != nil {
			t.Fatal(err)
		}
		repo := NewConversationStore(store)
		if err := repo.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		return repo, db
	}
	repo, db := open(true)
	ctx, a := t.Context(), conversationTestAuthority()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "persistent-parent"}, a)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, "persistent-upload"), a)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err = repo.TransitionAttachment(ctx, attachment.Attachment.ID, attachment.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "stored", BodyRef: "persistent-body-ref"}, a)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err = repo.TransitionAttachment(ctx, attachment.Attachment.ID, attachment.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "indexing", Source: &persistence.ConversationAttachmentSource{Identity: "knowledge", DocID: "pending-index-doc", PermissionID: "private-permission"}}, a)
	if err != nil {
		t.Fatal(err)
	}
	deleteConversationAcrossOwners(t, repo, conversation, a)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, _ = open(false)
	tombstone, err := repo.AttachmentRecord(ctx, attachment.Attachment.ID, a)
	if err != nil || tombstone.Attachment.State != "deleting" || tombstone.BodyRef != "persistent-body-ref" || tombstone.Source == nil || tombstone.Source.DocID != "pending-index-doc" || tombstone.Source.PermissionID != "private-permission" {
		t.Fatal("restart lost cleanup references", tombstone, err)
	}
	_, err = repo.TransitionAttachment(ctx, attachment.Attachment.ID, attachment.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "ready"}, a)
	requireConversationCode(t, err, "revision_conflict")
	if _, err := repo.TransitionAttachment(ctx, tombstone.Attachment.ID, tombstone.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, a); err != nil {
		t.Fatal("cleanup cannot finish after restart", err)
	}
}
