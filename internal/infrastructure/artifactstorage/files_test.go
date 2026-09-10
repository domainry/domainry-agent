package artifactstorage

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func TestPrivateContentIsImmutableConcurrentAndOwnerScoped(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "bodies")
	storage, err := NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "alice"}
	body := []byte(strings.Repeat("受限正文", 10000))
	hash := artifact.Hash(body)
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ref, err := storage.PutArtifactContent(t.Context(), hash, body, a)
			if err != nil || ref != "content_"+hash {
				t.Error("concurrent store", err)
			}
		}()
	}
	group.Wait()
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = NewFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	ref := "content_" + hash
	got, err := storage.ReadArtifactContent(t.Context(), ref, a)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatal("content mismatch", err)
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "bob"}, {Known: true, RuntimeID: "runtime", WorkspaceID: "other", UserID: "alice"}, {Known: true, RuntimeID: "other", WorkspaceID: "workspace", UserID: "alice"}} {
		if _, err := storage.ReadArtifactContent(t.Context(), ref, other); err == nil {
			t.Fatal("cross-owner content read")
		}
	}
	if _, err := storage.PutArtifactContent(t.Context(), hash, []byte("replacement"), a); err == nil {
		t.Fatal("hash mismatch overwritten")
	}
	for _, invalid := range []string{"../secret", "content_" + hash + "/../other", "content_" + strings.ToUpper(hash)} {
		if _, err := storage.ReadArtifactContent(t.Context(), invalid, a); err == nil {
			t.Fatal("path used as content reference")
		}
	}
	key, _ := ownerKey(a)
	owner, err := storage.root.OpenRoot(key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.WriteFile(hash+".json", []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ReadArtifactContent(t.Context(), ref, a); err == nil {
		t.Fatal("corrupted content trusted")
	}
}

func TestContentCannotFollowLinksOutsideItsOwnerDirectory(t *testing.T) {
	storage, err := NewFiles(filepath.Join(t.TempDir(), "bodies"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "alice"}
	b := a
	b.UserID = "bob"
	raw := []byte("private body")
	hash := artifact.Hash(raw)
	ref, err := storage.PutArtifactContent(t.Context(), hash, raw, a)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := storage.owner(b, true)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	alice, _ := ownerKey(a)
	if err := owner.Symlink("../"+alice+"/"+hash+".json", hash+".json"); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ReadArtifactContent(t.Context(), ref, b); err == nil {
		t.Fatal("symlink escaped owner directory")
	}
}
