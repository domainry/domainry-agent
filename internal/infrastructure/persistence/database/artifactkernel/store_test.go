package artifactkernel

import (
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	_ "modernc.org/sqlite"
)

func TestStandaloneArtifactKernelPersistsMetadataBindingsAndImmutableContent(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	dialect, _ := ormdialect.New(ormdialect.SQLite)
	renderer := dialect.WithSchema("")
	migration, err := SchemaMigration(renderer)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migration.Statements {
		if _, err = db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	files, err := NewContentFiles(filepath.Join(t.TempDir(), "content"))
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	content := []byte("generated result")
	info, err := files.PutImmutable(t.Context(), "workspace-a", "agent-generated:one", content)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, renderer)
	now := time.Now().UTC().Truncate(time.Millisecond)
	value, created, err := store.Register(t.Context(), sharedartifact.Artifact{
		ID: "exp_one", WorkspaceID: "workspace-a", Owner: sharedartifact.OwnerAgent, Kind: "generated",
		IdempotencyKey: "request-one", CreatedBy: "user-a", Filename: "result.md", MediaType: "text/markdown",
		ContentSHA256: info.SHA256, SizeBytes: info.Size, StorageReference: info.Reference,
		Status: sharedartifact.StatusAvailable, ScanStatus: sharedartifact.ScanNotRequired,
		ExpiresAt: now.Add(time.Hour), AuthorizationScopeSHA256: strings.Repeat("a", 64), Metadata: []byte(`{"version":1}`), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("register created=%v err=%v", created, err)
	}
	if _, created, err = store.Register(t.Context(), value); err != nil || created {
		t.Fatalf("register replay created=%v err=%v", created, err)
	}
	if _, _, err = store.Bind(t.Context(), sharedartifact.Binding{
		ID: "exp_one:conversation", WorkspaceID: value.WorkspaceID, ArtifactID: value.ID,
		Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingConversation,
		ResourceType: "agent_conversation", ResourceID: "conversation-a", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := store.List(t.Context(), value.WorkspaceID, sharedartifact.Query{
		Owner: sharedartifact.OwnerAgent, Kind: "generated",
		Binding: &sharedartifact.BindingQuery{Owner: sharedartifact.OwnerAgent, Kind: sharedartifact.BindingConversation, ResourceType: "agent_conversation", ResourceID: "conversation-a"},
	})
	if err != nil || len(items) != 1 || items[0].ID != value.ID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	elapsed := value
	elapsed.ID, elapsed.IdempotencyKey, elapsed.StorageReference = "exp_elapsed", "request-elapsed", "agent-generated:elapsed"
	elapsed.ExpiresAt = now.Add(-time.Second)
	if _, created, err = store.Register(t.Context(), elapsed); err != nil || !created {
		t.Fatalf("elapsed created=%v err=%v", created, err)
	}
	withoutExpiry := value
	withoutExpiry.ID, withoutExpiry.IdempotencyKey, withoutExpiry.StorageReference = "exp_without_expiry", "request-without-expiry", "agent-generated:without-expiry"
	withoutExpiry.ExpiresAt = time.Time{}
	if _, created, err = store.Register(t.Context(), withoutExpiry); err != nil || !created {
		t.Fatalf("without expiry created=%v err=%v", created, err)
	}
	elapsedItems, err := store.List(t.Context(), "", sharedartifact.Query{
		Owner: sharedartifact.OwnerAgent, Kind: "generated", Statuses: []sharedartifact.Status{sharedartifact.StatusAvailable},
		ExpiresAtOrBefore: now, Limit: 10,
	})
	if err != nil || len(elapsedItems) != 1 || elapsedItems[0].ID != elapsed.ID {
		t.Fatalf("elapsed artifacts=%+v err=%v", elapsedItems, err)
	}
	reader, err := files.Open(t.Context(), value.WorkspaceID, value.StorageReference)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(raw) != string(content) {
		t.Fatalf("content=%q err=%v", raw, err)
	}
	updatedAt := now.Add(time.Second)
	changed, err := store.Update(t.Context(), sharedartifact.Mutation{
		WorkspaceID: value.WorkspaceID, ID: value.ID, Owner: value.Owner, Kind: value.Kind,
		ExpectedStatus: value.Status, ExpectedScanStatus: value.ScanStatus, ExpectedUpdatedAt: value.UpdatedAt,
		Status: value.Status, ScanStatus: value.ScanStatus, Metadata: []byte(`{"version":1,"downloads":1}`), UpdatedAt: updatedAt,
	})
	if err != nil || !changed {
		t.Fatalf("update changed=%v err=%v", changed, err)
	}
	if err = files.Delete(t.Context(), value.WorkspaceID, value.StorageReference); err != nil {
		t.Fatal(err)
	}
	if err = files.Delete(t.Context(), value.WorkspaceID, value.StorageReference); err != nil {
		t.Fatal("idempotent delete", err)
	}
}

func TestArtifactKernelSchemaIsPortableAndOwnsOnlySharedTables(t *testing.T) {
	for _, name := range []ormdialect.Name{ormdialect.SQLite, ormdialect.Postgres, ormdialect.MySQL} {
		t.Run(string(name), func(t *testing.T) {
			dialect, err := ormdialect.New(name)
			if err != nil {
				t.Fatal(err)
			}
			migration, err := SchemaMigration(dialect.WithSchema(""))
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(migration.Statements, "\n")
			if !strings.Contains(joined, artifactTable) || !strings.Contains(joined, bindingTable) || strings.Contains(joined, "_agent_artifact_exports") {
				t.Fatal("invalid shared Artifact schema")
			}
		})
	}
}
