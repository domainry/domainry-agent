package documentstorage

import (
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func TestLibraryOriginalIsolationAndDurableDeletionFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	f, err := NewFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { f.Close() }()
	scope := agentsdk.KnowledgeDocumentStorageScope{RuntimeID: "runtime", WorkspaceID: "workspace", LibraryID: "lib_" + strings.Repeat("a", 32)}
	id, raw := "kdoc_"+strings.Repeat("b", 32), []byte("shared immutable original")
	ref, err := f.PutKnowledgeDocumentContent(t.Context(), scope, id, artifact.Hash(raw), raw)
	if err != nil {
		t.Fatal(err)
	}
	other := scope
	other.LibraryID = "lib_" + strings.Repeat("c", 32)
	if _, err = f.ReadKnowledgeDocumentContent(t.Context(), other, id, ref); err == nil {
		t.Fatal("cross-library original read")
	}
	other = scope
	other.WorkspaceID = "other"
	if _, err = f.ReadKnowledgeDocumentContent(t.Context(), other, id, ref); err == nil {
		t.Fatal("cross-workspace original read")
	}
	if err = f.DeleteKnowledgeDocumentContent(t.Context(), scope, id); err != nil {
		t.Fatal(err)
	}
	f.Close()
	f, err = NewFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.PutKnowledgeDocumentContent(t.Context(), scope, id, artifact.Hash(raw), raw); err == nil {
		t.Fatal("late upload escaped persistent tombstone")
	}
	if _, err = f.ReadKnowledgeDocumentContent(t.Context(), scope, id, ref); err == nil {
		t.Fatal("deleted original returned")
	}
}
