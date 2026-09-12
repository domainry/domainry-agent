package agent

import (
	"path/filepath"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestAttachmentIndexCheckPreservesActiveLeaseAndUnknownWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "check.db")
	repo, db := openAttachmentIndexStore(t, path)
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "check")
	if _, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a); err != nil {
		t.Fatal(err)
	}
	lease := claimAttachmentIndex(t, repo)
	before, started, err := repo.StartAttachmentIndexPut(ctx, lease)
	if err != nil || !started {
		t.Fatal(err, started)
	}
	nextActor := a
	nextActor.RoleKey = "updated-role"
	checked, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, original.Attachment.ID, before.Attachment.Revision, nextActor)
	if err != nil || checked.Index.Actor != a || *checked.Source != *before.Source || !checked.Index.PutStarted || checked.Index.PutAcknowledged || checked.Index.LastCheckRevision != before.Attachment.Revision {
		t.Fatal("check changed frozen attempt", checked, err)
	}
	replay, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, original.Attachment.ID, before.Attachment.Revision, nextActor)
	if err != nil || replay.Attachment.Revision != checked.Attachment.Revision {
		t.Fatal("check replay wrote again", err)
	}
	if _, err = repo.AttachmentIndexWorkRecord(ctx, lease); err != nil {
		t.Fatal("check invalidated active lease", err)
	}
	if _, ok, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, "competitor", time.Now().UTC(), time.Minute); err != nil || ok {
		t.Fatal("active lease stolen", err)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "retry", ErrorCode: "attachment_put_uncertain", RetryAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before, err = repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	checked, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, original.Attachment.ID, before.Attachment.Revision, nextActor)
	if err != nil || checked.Index.Actor != nextActor || *checked.Source != *before.Source || !checked.Index.PutStarted || checked.Index.PutAcknowledged {
		t.Fatal("resume changed uncertain write", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	repo, _ = openAttachmentIndexStore(t, path)
	replay, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, original.Attachment.ID, before.Attachment.Revision, nextActor)
	if err != nil || replay.Attachment.Revision != checked.Attachment.Revision {
		t.Fatal("restart lost check receipt", err)
	}
	next := claimAttachmentIndex(t, repo)
	if next.Authority != nextActor || next.Token <= lease.Token {
		t.Fatal("role or fence lost", next)
	}
	if _, started, err = repo.StartAttachmentIndexPut(ctx, next); err != nil || started {
		t.Fatal("uncertain PUT repeated", started, err)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "put_acknowledged"}); err == nil {
		t.Fatal("stale attempt accepted")
	}
}

func TestAttachmentIndexCheckReadyAndDeletionRemainMonotonic(t *testing.T) {
	repo, _ := openAttachmentIndexStore(t, filepath.Join(t.TempDir(), "ready.db"))
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "ready-check")
	if _, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a); err != nil {
		t.Fatal(err)
	}
	lease := claimAttachmentIndex(t, repo)
	if _, _, err := repo.StartAttachmentIndexPut(ctx, lease); err != nil {
		t.Fatal(err)
	}
	applyAttachmentIndex(t, repo, lease, "indexed", "INDEXED")
	ready, err := repo.AttachmentRecord(ctx, original.Attachment.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, ready.Attachment.ID, ready.Attachment.Revision, a)
	if err != nil || checked.Attachment.State != "ready" || !checked.Index.IndexObserved {
		t.Fatal("ready state regressed", err)
	}
	lease = claimAttachmentIndex(t, repo)
	applyAttachmentIndex(t, repo, lease, "indexed", "INDEXED")
	if _, ok, err := repo.ClaimAttachmentIndexWork(ctx, a.RuntimeID, "idle", time.Now().UTC(), time.Minute); err != nil || ok {
		t.Fatal("ready job did not park", err)
	}
	replay, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, ready.Attachment.ID, ready.Attachment.Revision, a)
	if err != nil || replay.Index.LastCheckRevision != ready.Attachment.Revision {
		t.Fatal("ready receipt missing", err)
	}
	deleting, err := repo.TransitionAttachment(ctx, ready.Attachment.ID, replay.Attachment.Revision, persistence.ConversationAttachmentTransition{State: "deleting"}, a)
	if err != nil {
		t.Fatal(err)
	}
	lease = claimAttachmentIndex(t, repo)
	if err = repo.StartAttachmentIndexDelete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "retry", ErrorCode: "attachment_delete_uncertain", RetryAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	deleting, err = repo.AttachmentRecord(ctx, deleting.Attachment.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	checked, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, deleting.Attachment.ID, deleting.Attachment.Revision, a)
	if err != nil || checked.Attachment.State != "deleting" || !checked.Index.DeleteStarted || checked.Index.DeleteAcknowledged || checked.BodyRef != original.BodyRef {
		t.Fatal("check rewrote uncertain deletion", err)
	}
	lease = claimAttachmentIndex(t, repo)
	requireConversationCode(t, repo.ApplyAttachmentIndexProgress(ctx, lease, persistence.ConversationAttachmentIndexProgress{Event: "deleted"}), "attachment_cleanup_unconfirmed")
}

func TestAttachmentIndexCheckOwnerRevisionAndParentBoundaries(t *testing.T) {
	repo, _ := openAttachmentIndexStore(t, filepath.Join(t.TempDir(), "scope.db"))
	a, ctx := conversationTestAuthority(), t.Context()
	c, original, source := attachmentIndexFixture(t, repo, "scope-check")
	_, err := repo.RequestAttachmentIndexCheck(ctx, c.ID, original.Attachment.ID, original.Attachment.Revision, a)
	requireConversationCode(t, err, "attachment_index_not_requested")
	queued, err := repo.QueueAttachmentIndex(ctx, original.Attachment.ID, original.Attachment.Revision, source, a)
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "other"}, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.RequestAttachmentIndexCheck(ctx, other.ID, queued.Attachment.ID, queued.Attachment.Revision, a)
	requireConversationCode(t, err, "attachment_not_found")
	for _, mutate := range []func(*agentsdk.ConversationAuthority){func(a *agentsdk.ConversationAuthority) { a.UserID = "other" }, func(a *agentsdk.ConversationAuthority) { a.WorkspaceID = "other" }, func(a *agentsdk.ConversationAuthority) { a.RuntimeID = "other" }} {
		outsider := a
		mutate(&outsider)
		if _, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, queued.Attachment.ID, queued.Attachment.Revision, outsider); err == nil {
			t.Fatal("foreign owner allowed")
		}
	}
	_, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, queued.Attachment.ID, queued.Attachment.Revision+1, a)
	requireConversationCode(t, err, "revision_conflict")
	archived := true
	if _, err = repo.Update(ctx, c.ID, agentsdk.ConversationUpdate{ExpectedRevision: c.Revision, Archived: &archived}, a); err != nil {
		t.Fatal(err)
	}
	_, err = repo.RequestAttachmentIndexCheck(ctx, c.ID, queued.Attachment.ID, queued.Attachment.Revision, a)
	requireConversationCode(t, err, "attachment_conversation_archived")
}
