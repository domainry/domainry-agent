package agent

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/webhost"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func openAttachmentIndexStore(t *testing.T, path string) (*ConversationStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	d, _ := ormdialect.New(ormdialect.SQLite)
	renderer := d.WithSchema("")
	r := &webhost.Registrar{DB: db, Renderer: renderer}
	if err = r.Prepare(t.Context()); err != nil {
		t.Fatal(err)
	}
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ApplyOwnedMigrations(t.Context(), "agent", migrations); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(db, renderer, sqlite.NewEngine())
	if err != nil {
		t.Fatal(err)
	}
	return NewConversationStore(s), db
}

func attachmentIndexFixture(t *testing.T, repo *ConversationStore, client string) (agentsdk.Conversation, persistence.ConversationAttachmentRecord, persistence.ConversationAttachmentSource) {
	t.Helper()
	a, ctx := conversationTestAuthority(), t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: client}, a)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.ReserveAttachment(ctx, attachmentReservation(c.ID, client), a)
	if err != nil {
		t.Fatal(err)
	}
	r, err = repo.TransitionAttachment(ctx, r.Attachment.ID, r.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "stored", BodyRef: "private-original"}, a)
	if err != nil {
		t.Fatal(err)
	}
	source := persistence.ConversationAttachmentSource{Identity: strings.Repeat("a", 64), PermissionID: "private:" + c.ID, AccessPolicySHA256: strings.Repeat("b", 64)}
	if err = repo.ActivateAttachmentKnowledgeSource(ctx, a.RuntimeID, a.WorkspaceID, source.Identity); err != nil {
		t.Fatal(err)
	}
	return c, r, source
}

func claimAttachmentIndex(t *testing.T, repo *ConversationStore) persistence.ConversationAttachmentIndexLease {
	t.Helper()
	l, ok, err := repo.ClaimAttachmentIndexWork(t.Context(), conversationTestAuthority().RuntimeID, "worker", time.Now().UTC(), time.Minute)
	if err != nil || !ok {
		t.Fatal("expected due attachment index work", ok, err)
	}
	return l
}

func applyAttachmentIndex(t *testing.T, repo *ConversationStore, l persistence.ConversationAttachmentIndexLease, event, status string) {
	t.Helper()
	if err := repo.ApplyAttachmentIndexProgress(t.Context(), l, persistence.ConversationAttachmentIndexProgress{Event: event, IndexStatus: status, RetryAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentUnknownUploadSurvivesRestartWithoutResettingWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown-upload.db")
	repo, db := openAttachmentIndexStore(t, path)
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "unknown-upload")
	r, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a)
	if err != nil {
		t.Fatal(err)
	}
	frozen := *r.Source
	lease := claimAttachmentIndex(t, repo)
	if _, started, err := repo.StartAttachmentIndexPut(ctx, lease); err != nil || !started {
		t.Fatal("initial upload not started", started, err)
	}
	for _, code := range []string{"attachment_put_uncertain", "attachment_inspect_failed", "attachment_put_unconfirmed", ""} {
		if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "retry", ErrorCode: code, RetryAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		r, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
		if err != nil || r.Attachment.State != "needs_reconcile" || !r.Index.PutStarted || r.Index.PutAcknowledged || r.Index.IndexObserved || r.BodyRef != original.BodyRef || *r.Source != frozen {
			t.Fatal("unknown result lost immutable original or write identity", code, r, err)
		}
		lease = claimAttachmentIndex(t, repo)
		if _, started, err := repo.StartAttachmentIndexPut(ctx, lease); err != nil || started {
			t.Fatal("unknown upload was repeated", started, err)
		}
	}
	applyAttachmentIndex(t, repo, lease, "retry", "")
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, _ = openAttachmentIndexStore(t, path)
	r, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil || r.Attachment.State != "needs_reconcile" || *r.Source != frozen {
		t.Fatal("restart lost pending reconciliation", r, err)
	}
	checked, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, r.Attachment.ID, r.Attachment.Revision, a)
	if err != nil || checked.Index.LastCheckRevision != r.Attachment.Revision || *checked.Source != frozen || !checked.Index.PutStarted {
		t.Fatal("check reset original command", checked, err)
	}
	lease = claimAttachmentIndex(t, repo)
	if _, started, err := repo.StartAttachmentIndexPut(ctx, lease); err != nil || started {
		t.Fatal("restart made unknown upload retryable", started, err)
	}
	applyAttachmentIndex(t, repo, lease, "indexed", "INDEXED")
	r, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil || r.Attachment.State != "ready" || !r.Index.IndexObserved || r.Index.PutAcknowledged || *r.Source != frozen {
		t.Fatal("positive observation did not resolve the same upload", r, err)
	}
	if _, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, r.Attachment.ID, r.Attachment.Revision, a); err != nil {
		t.Fatal(err)
	}
	lease = claimAttachmentIndex(t, repo)
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "retry", ErrorCode: "attachment_inspect_failed", RetryAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil || r.Attachment.State != "failed" || !r.Index.IndexObserved {
		t.Fatal("temporary inspection failure erased known upload completion", r, err)
	}
	if _, err = repo.TransitionAttachment(ctx, r.Attachment.ID, r.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a); err != nil {
		t.Fatal(err)
	}
	lease = claimAttachmentIndex(t, repo)
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "retry", ErrorCode: "attachment_put_unconfirmed", RetryAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil || r.Attachment.State != "deleting" || r.BodyRef == "" {
		t.Fatal("late uncertainty undid deletion or discarded the original", r, err)
	}
}

func TestAttachmentIndexSourceClaimsExcludeLibrariesAndOtherWorkspaces(t *testing.T) {
	for _, order := range []string{"attachment-first", "library-first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			repo, _ := openAttachmentIndexStore(t, filepath.Join(t.TempDir(), "sources.db"))
			a, ctx := conversationTestAuthority(), t.Context()
			lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: "library", Kind: "shared", Name: "Shared"}, a)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Repeat("c", 64)
			attachment := func() error { return repo.ActivateAttachmentKnowledgeSource(ctx, a.RuntimeID, a.WorkspaceID, source) }
			library := func() error {
				return repo.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}, source)
			}
			switch order {
			case "attachment-first":
				if err = attachment(); err != nil {
					t.Fatal(err)
				}
				if err = attachment(); err != nil {
					t.Fatal("source claim not idempotent", err)
				}
				requireConversationCode(t, library(), "document_source_already_bound")
				for _, scope := range [][3]string{{a.RuntimeID, "other", source}, {"other", a.WorkspaceID, source}, {a.RuntimeID, a.WorkspaceID, strings.Repeat("d", 64)}} {
					requireConversationCode(t, repo.ActivateAttachmentKnowledgeSource(ctx, scope[0], scope[1], scope[2]), "attachment_source_already_bound")
				}
			case "library-first":
				if err = library(); err != nil {
					t.Fatal(err)
				}
				requireConversationCode(t, attachment(), "attachment_source_already_bound")
			case "concurrent":
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for _, f := range []func() error{attachment, library} {
					wg.Add(1)
					go func() { defer wg.Done(); results <- f() }()
				}
				wg.Wait()
				close(results)
				succeeded := 0
				for err := range results {
					if err == nil {
						succeeded++
					}
				}
				if succeeded != 1 {
					t.Fatal("a physical KB had multiple owners", succeeded)
				}
			}
			if managed, err := repo.KnowledgeSourceManaged(ctx, source); err != nil || !managed {
				t.Fatal("default-source bypass remains", managed, err)
			}
		})
	}
}

func TestAttachmentIndexDeletionFencesLateWritesAndRequiresAcknowledgement(t *testing.T) {
	repo, _ := openAttachmentIndexStore(t, filepath.Join(t.TempDir(), "inflight.db"))
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "inflight")
	r, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source.DocID == "" || r.Source.RequestID == "" || r.Attachment.Visibility != "conversation_private" {
		t.Fatal("private source identity missing", r)
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}} {
		if _, err = repo.QueueAttachmentIndex(ctx, r.Attachment.ID, r.Attachment.Revision, source, other); err == nil {
			t.Fatal("cross-owner indexing accepted")
		}
	}
	l := claimAttachmentIndex(t, repo)
	if _, ok, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, "competing", time.Now().UTC(), time.Minute); err != nil || ok {
		t.Fatal("concurrent lease accepted", err)
	}
	if _, started, err := repo.StartAttachmentIndexPut(ctx, l); err != nil || !started {
		t.Fatal(started, err)
	}
	if _, started, err := repo.StartAttachmentIndexPut(ctx, l); err != nil || started {
		t.Fatal("PUT repeated", started, err)
	}
	replay, err := repo.QueueAttachmentIndex(ctx, r.Attachment.ID, original.Attachment.Revision, source, a)
	if err != nil || *replay.Source != *r.Source || !replay.Index.PutStarted {
		t.Fatal("queue retry reset in-flight write", err)
	}
	changed := source
	changed.PermissionID = "other-private-scope"
	_, err = repo.QueueAttachmentIndex(ctx, r.Attachment.ID, r.Attachment.Revision, changed, a)
	requireConversationCode(t, err, "attachment_source_changed")
	deleteConversationAcrossOwners(t, repo, c, a)
	applyAttachmentIndex(t, repo, l, "put_acknowledged", "")
	l = claimAttachmentIndex(t, repo)
	applyAttachmentIndex(t, repo, l, "indexed", "INDEXED")
	l = claimAttachmentIndex(t, repo)
	r, err = repo.AttachmentIndexWorkRecord(ctx, l)
	if err != nil || r.Attachment.State != "deleting" || r.BodyRef != original.BodyRef || !r.Index.IndexObserved {
		t.Fatal("late indexed result revived parent or lost original", r, err)
	}
	_, err = repo.TransitionAttachment(ctx, r.Attachment.ID, r.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleted"}, a)
	requireConversationCode(t, err, "attachment_index_managed")
	if err = repo.StartAttachmentIndexDelete(ctx, l); err != nil {
		t.Fatal(err)
	}
	requireConversationCode(t, repo.ApplyAttachmentIndexProgress(ctx, l, persistence.ConversationAttachmentIndexProgress{Event: "deleted"}), "attachment_cleanup_unconfirmed")
	applyAttachmentIndex(t, repo, l, "delete_acknowledged", "")
	l = claimAttachmentIndex(t, repo)
	applyAttachmentIndex(t, repo, l, "deleted", "")
	r, err = repo.AttachmentRecord(ctx, r.Attachment.ID, a)
	if err != nil || r.Attachment.State != "deleted" || r.BodyRef != "" {
		t.Fatal("cleanup not finalized", r, err)
	}
	if jobs, err := repo.AttachmentCleanupCandidates(ctx, a.RuntimeID, time.Now().Add(time.Hour), 20); err != nil || len(jobs) != 0 {
		t.Fatal("cleanup not retired", jobs, err)
	}
	if _, err = repo.QueueAttachmentIndex(ctx, r.Attachment.ID, r.Attachment.Revision, source, a); err == nil {
		t.Fatal("deleted parent resurrected")
	}
}

func TestAttachmentIndexRestartTakeoverAndReadyDeleteNeverReuseLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	repo, db := openAttachmentIndexStore(t, path)
	a, ctx := conversationTestAuthority(), t.Context()
	_, original, source := attachmentIndexFixture(t, repo, "restart")
	r, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a)
	if err != nil {
		t.Fatal(err)
	}
	old := claimAttachmentIndex(t, repo)
	if _, started, err := repo.StartAttachmentIndexPut(ctx, old); err != nil || !started {
		t.Fatal(started, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, _ = openAttachmentIndexStore(t, path)
	l, found, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, old.Owner, old.ExpiresAt.Add(time.Millisecond), time.Minute)
	if err != nil || !found || l.Token <= old.Token {
		t.Fatal("restart takeover lost fence", l, err)
	}
	if _, err = repo.AttachmentIndexWorkRecord(ctx, old); err == nil {
		t.Fatal("old lease remained valid")
	}
	if resumed, started, err := repo.StartAttachmentIndexPut(ctx, l); err != nil || started || *resumed.Source != *r.Source {
		t.Fatal("uncertain PUT repeated after restart", started, err)
	}
	applyAttachmentIndex(t, repo, l, "indexed", "INDEXED")
	if _, ok, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, "idle", time.Now().Add(time.Hour), time.Minute); err != nil || ok {
		t.Fatal("ready task was still runnable", err)
	}
	r, err = repo.AttachmentRecord(ctx, r.Attachment.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.TransitionAttachment(ctx, r.Attachment.ID, r.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a); err != nil {
		t.Fatal(err)
	}
	deletion := claimAttachmentIndex(t, repo)
	if deletion.Token <= l.Token {
		t.Fatal("ready/deleting reset the monotonic fence", deletion, l)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, old, persistence.ConversationAttachmentIndexProgress{Event: "indexed", IndexStatus: "INDEXED"}); err == nil {
		t.Fatal("old lease accepted after task reactivation")
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, l, persistence.ConversationAttachmentIndexProgress{Event: "indexed", IndexStatus: "INDEXED"}); err == nil {
		t.Fatal("completed lease accepted after task reactivation")
	}
}

func TestAttachmentIndexDeleteBeforePutNeedsNoRemoteWrite(t *testing.T) {
	repo, _ := openAttachmentIndexStore(t, filepath.Join(t.TempDir(), "cancel.db"))
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "cancel")
	if _, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a); err != nil {
		t.Fatal(err)
	}
	l := claimAttachmentIndex(t, repo)
	deleteConversationAcrossOwners(t, repo, c, a)
	if _, started, err := repo.StartAttachmentIndexPut(ctx, l); err != nil || started {
		t.Fatal("PUT started after parent deletion", started, err)
	}
	applyAttachmentIndex(t, repo, l, "deleted", "")
}
