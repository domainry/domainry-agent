package attachmentstorage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"golang.org/x/sys/unix"
)

func TestPrivateFilesImmutableOwnerIsolationAndDeletionFence(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "attachments")
	first, err := NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	id, raw := "att_"+strings.Repeat("a", 32), []byte("original file\x00with binary bytes")
	ref, err := first.PutAttachmentContent(t.Context(), id, hash(raw), raw, a)
	if err != nil {
		t.Fatal(err)
	}
	otherContent := []byte("changed")
	if _, err := second.PutAttachmentContent(t.Context(), id, hash(otherContent), otherContent, a); err == nil {
		t.Fatal("overwrote immutable content")
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}} {
		if _, err := second.ReadAttachmentContent(t.Context(), id, ref, other); err == nil {
			t.Fatal("cross-owner bytes")
		}
	}
	if _, err := first.PutAttachmentContent(t.Context(), "../outside", hash(raw), raw, a); err == nil {
		t.Fatal("path accepted")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.ReadAttachmentContent(t.Context(), id, ref, a)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("restart changed bytes", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				_ = reopened.DeleteAttachmentContent(t.Context(), id, a)
			} else {
				_, _ = second.PutAttachmentContent(t.Context(), id, hash(raw), raw, a)
			}
		}(i)
	}
	wg.Wait()
	if err := reopened.DeleteAttachmentContent(t.Context(), id, a); err != nil {
		t.Fatal(err)
	}
	if _, err := second.PutAttachmentContent(t.Context(), id, hash(raw), raw, a); err == nil {
		t.Fatal("late upload resurrected deleted file")
	}
	if _, err := reopened.ReadAttachmentContent(t.Context(), id, ref, a); err == nil {
		t.Fatal("deleted bytes readable")
	}
	root, err := reopened.owner(a)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, suffix := range []string{".bin", ".pending"} {
		if _, err := root.Stat(id + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("physical content retained", suffix, err)
		}
	}
	if _, err := root.Stat(id + ".deleted"); err != nil {
		t.Fatal("persistent deletion fence missing", err)
	}
}

func TestFilesLockCancellationAndUnsafeStorageRejected(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "files")
	files, err := NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	id := "att_" + strings.Repeat("b", 32)
	root, err := files.owner(a)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := root.OpenFile(id+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	raw := []byte("bytes")
	if _, err := files.PutAttachmentContent(ctx, id, hash(raw), raw, a); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lock ignored cancellation", err)
	}
	_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	ref, err := files.PutAttachmentContent(t.Context(), id, hash(raw), raw, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(id+".bin", []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadAttachmentContent(t.Context(), id, ref, a); err == nil {
		t.Fatal("corruption accepted")
	}
	public := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(public, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0755); err != nil {
		t.Fatal(err)
	}
	if unsafe, err := NewFiles(public); err == nil {
		unsafe.Close()
		t.Fatal("public storage accepted")
	}
}
