package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/artifactkernel"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	knowledgeartifact "github.com/domainry/domainry-knowledge/artifact"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func attachmentReservation(conversationID, client string) persistence.ConversationAttachmentReserve {
	content := []byte("private attachment content: " + client)
	return persistence.ConversationAttachmentReserve{
		ClientID: client, ConversationID: conversationID, Filename: "合同.pdf",
		ContentType: "application/pdf", SHA256: knowledgeartifact.Hash(content), Bytes: int64(len(content)), Content: content,
	}
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

func TestAttachmentUsesSharedArtifactBindingsAndOwnerIsolation(t *testing.T) {
	store, database := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "attachment-conversation"}, a)
	if err != nil {
		t.Fatal(err)
	}
	in := attachmentReservation(conversation.ID, "upload")
	var wait sync.WaitGroup
	results := make(chan persistence.ConversationAttachmentRecord, 4)
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, reserveErr := repo.ReserveAttachment(ctx, in, a)
			results <- value
			errors <- reserveErr
		}()
	}
	wait.Wait()
	close(results)
	close(errors)
	for reserveErr := range errors {
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
	}
	var current persistence.ConversationAttachmentRecord
	for value := range results {
		if current.Attachment.ID != "" && current.Attachment.ID != value.Attachment.ID {
			t.Fatal("duplicate attachment identity")
		}
		current = value
	}
	if current.Attachment.Visibility != "conversation_private" || current.Attachment.State != "stored" || current.Attachment.Revision != 1 || current.Source != nil || current.Index != nil {
		t.Fatalf("unexpected attachment: %+v", current)
	}
	content, err := repo.AttachmentContent(ctx, current.Attachment.ID, a)
	if err != nil || string(content) != string(in.Content) {
		t.Fatalf("content=%q err=%v", content, err)
	}
	changed := in
	changed.Filename = "different.pdf"
	_, err = repo.ReserveAttachment(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}} {
		if _, err = repo.AttachmentRecord(ctx, current.Attachment.ID, other); err == nil {
			t.Fatal("cross-owner read")
		}
		if _, err = repo.Attachments(ctx, conversation.ID, "", 10, other); err == nil {
			t.Fatal("cross-owner list")
		}
	}
	page, err := repo.Attachments(ctx, conversation.ID, "", 1, a)
	raw, _ := json.Marshal(page)
	if err != nil || len(page.Items) != 1 || strings.Contains(string(raw), "storage_reference") || strings.Contains(string(raw), string(in.Content)) {
		t.Fatalf("public page=%s err=%v", raw, err)
	}
	var artifacts, subjectBindings, conversationBindings int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM _artifacts WHERE id=? AND owner='agent' AND kind='attachment'`, current.Attachment.ID).Scan(&artifacts); err != nil || artifacts != 1 {
		t.Fatalf("artifacts=%d err=%v", artifacts, err)
	}
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM _artifact_bindings WHERE artifact_id=? AND kind='subject' AND resource_type='agent_user' AND resource_id=?`, current.Attachment.ID, a.UserID).Scan(&subjectBindings); err != nil || subjectBindings != 1 {
		t.Fatalf("subject bindings=%d err=%v", subjectBindings, err)
	}
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM _artifact_bindings WHERE artifact_id=? AND kind='conversation' AND resource_type='agent_conversation' AND resource_id=?`, current.Attachment.ID, conversation.ID).Scan(&conversationBindings); err != nil || conversationBindings != 1 {
		t.Fatalf("conversation bindings=%d err=%v", conversationBindings, err)
	}
	for _, retired := range []string{"_agent_conversation_attachments", "_agent_attachment_cleanup"} {
		var count int
		if err = database.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired table %s exists count=%d err=%v", retired, count, err)
		}
	}
}

func TestAttachmentDeletionRevokesReadsAndKeepsDurableCleanupJob(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "parent"}, a)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, "first"), a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, "second"), a)
	if err != nil {
		t.Fatal(err)
	}
	page, err := repo.Attachments(ctx, conversation.ID, "", 1, a)
	if err != nil || page.Complete || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	next, err := repo.Attachments(ctx, conversation.ID, page.NextAfter, 1, a)
	if err != nil || !next.Complete || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal(next, err)
	}
	deleteConversationAcrossOwners(t, repo, conversation, a)
	for _, id := range []string{first.Attachment.ID, second.Attachment.ID} {
		record, readErr := repo.AttachmentRecord(ctx, id, a)
		if readErr != nil || record.Attachment.State != "deleting" {
			t.Fatalf("cleanup state=%+v err=%v", record, readErr)
		}
		if _, readErr = repo.AttachmentContent(ctx, id, a); readErr == nil {
			t.Fatal("deleting attachment remained downloadable")
		}
	}
	lease, found, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, "cleanup-worker", time.Now().UTC(), time.Minute)
	if err != nil || !found {
		t.Fatalf("cleanup lease=%+v found=%v err=%v", lease, found, err)
	}
	if err = repo.DeleteAttachmentContent(ctx, lease.AttachmentID, lease.Authority); err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "deleted"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.AttachmentRecord(ctx, lease.AttachmentID, a)
	if err != nil || deleted.Attachment.State != "deleted" {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
}

func TestAttachmentValidationAndConversationQuota(t *testing.T) {
	store, _ := openAgentStore(t)
	repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "quota"}, a)
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"../file.pdf", `C:\private.pdf`, "bad\nname.pdf", ".", ""} {
		in := attachmentReservation(conversation.ID, "bad")
		in.Filename = filename
		_, err = repo.ReserveAttachment(ctx, in, a)
		requireConversationCode(t, err, "attachment_invalid")
	}
	for index := 0; index < 50; index++ {
		if _, err = repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, fmt.Sprintf("file-%d", index)), a); err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, "over-limit"), a)
	requireConversationCode(t, err, "attachment_limit")
}

func TestAttachmentCleanupSurvivesDatabaseReopen(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "attachments.db")
	contentPath := filepath.Join(root, "shared-content")
	open := func(migrate bool) (*ConversationStore, *sql.DB) {
		t.Helper()
		database, err := sql.Open("sqlite", databasePath)
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(1)
		dialect, _ := ormdialect.New(ormdialect.SQLite)
		if migrate {
			artifactMigration, migrationErr := sharedartifact.SchemaMigrationForDialect(dialect.WithSchema(""))
			if migrationErr != nil {
				t.Fatal(migrationErr)
			}
			for _, statement := range artifactMigration.Statements {
				if _, err = database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			migrations, migrationErr := SchemaMigrations("sqlite", "")
			if migrationErr != nil {
				t.Fatal(migrationErr)
			}
			for _, migration := range migrations {
				for _, statement := range migration.Statements {
					if _, err = database.ExecContext(t.Context(), statement); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		store, err := NewStore(database, dialect.WithSchema(""), sqlite.NewEngine(), "attachment-store-test")
		if err != nil {
			t.Fatal(err)
		}
		content, err := artifactkernel.NewContentFiles(contentPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = content.Close() })
		if err = store.BindArtifactPersistence(sharedartifact.NewSQLStore(database, dialect.WithSchema("")), content, content); err != nil {
			t.Fatal(err)
		}
		repo := NewConversationStore(store)
		if err = repo.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		return repo, database
	}
	repo, database := open(true)
	ctx, authority := t.Context(), conversationTestAuthority()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "persistent-parent"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.ReserveAttachment(ctx, attachmentReservation(conversation.ID, "persistent-upload"), authority)
	if err != nil {
		t.Fatal(err)
	}
	deleteConversationAcrossOwners(t, repo, conversation, authority)
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	repo, database = open(false)
	t.Cleanup(func() { _ = database.Close() })
	tombstone, err := repo.AttachmentRecord(ctx, attachment.Attachment.ID, authority)
	if err != nil || tombstone.Attachment.State != "deleting" {
		t.Fatalf("restart lost cleanup metadata: %+v err=%v", tombstone, err)
	}
	lease, found, err := repo.ClaimAttachmentIndexWork(ctx, authority.RuntimeID, "restart-worker", time.Now().UTC(), time.Minute)
	if err != nil || !found || lease.AttachmentID != attachment.Attachment.ID {
		t.Fatalf("lease=%+v found=%v err=%v", lease, found, err)
	}
	if err = repo.DeleteAttachmentContent(ctx, lease.AttachmentID, lease.Authority); err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "deleted"}); err != nil {
		t.Fatal(err)
	}
}
