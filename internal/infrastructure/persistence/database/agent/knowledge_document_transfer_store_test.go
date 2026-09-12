package agent

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func TestKnowledgeDocumentTransferAtomicRetirementAndRecovery(t *testing.T) {
	for _, scenario := range []string{"commit-rollback", "source-deleted", "source-member-revoked", "target-member-revoked", "competing-moves"} {
		t.Run(scenario, func(t *testing.T) {
			store, _ := openAgentStore(t)
			repo, a, ctx := NewConversationStore(store), conversationTestAuthority(), t.Context()
			actor := a
			actor.UserID = "moving-editor"
			makeLibrary := func(client, physical string) agentsdk.KnowledgeLibrary {
				t.Helper()
				lib, err := repo.CreateKnowledgeLibrary(ctx, agentsdk.KnowledgeLibraryCreate{ClientID: client, Kind: "shared", Name: client}, a)
				if err != nil {
					t.Fatal(err)
				}
				lib, err = repo.SetKnowledgeLibraryMember(ctx, lib.ID, actor.UserID, agentsdk.KnowledgeLibraryMemberWrite{Role: "editor", ExpectedRevision: lib.Revision}, a)
				if err != nil {
					t.Fatal(err)
				}
				if err = repo.ActivateKnowledgeDocumentSource(ctx, agentsdk.KnowledgeDocumentStorageScope{RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, LibraryID: lib.ID}, physical); err != nil {
					t.Fatal(err)
				}
				return lib
			}
			sourceLibrary := makeLibrary("source", strings.Repeat("a", 64))
			targetLibrary := makeLibrary("target", strings.Repeat("b", 64))
			input := persistence.KnowledgeDocumentReserve{LibraryID: sourceLibrary.ID, ClientID: "original", Filename: "private.txt", ContentType: "text/plain", Bytes: 4, SHA256: strings.Repeat("c", 64), SourceID: strings.Repeat("a", 64), AccessPolicySHA256: strings.Repeat("d", 64)}
			source, err := repo.ReserveKnowledgeDocument(ctx, input, a)
			if err != nil {
				t.Fatal(err)
			}
			source, err = repo.CommitKnowledgeDocumentContent(ctx, source.Document.ID, source.Document.Revision, "source-original", a)
			if err != nil {
				t.Fatal(err)
			}
			origin := persistence.KnowledgeDocumentOrigin{LibraryID: sourceLibrary.ID, DocumentID: source.Document.ID, Revision: source.Document.Revision, Mode: "move"}
			input.LibraryID, input.ClientID, input.SourceID, input.AccessPolicySHA256, input.DocumentOrigin = targetLibrary.ID, "transfer", strings.Repeat("b", 64), strings.Repeat("e", 64), &origin
			target, err := repo.ReserveKnowledgeDocument(ctx, input, actor)
			if err != nil {
				t.Fatal(err)
			}
			expected := target.Document.Revision
			switch scenario {
			case "commit-rollback":
				expected++
			case "source-deleted":
				_, err = repo.RequestKnowledgeDocumentDeletion(ctx, source.Document.ID, source.Document.Revision, a)
			case "source-member-revoked":
				_, err = repo.RemoveKnowledgeLibraryMember(ctx, sourceLibrary.ID, actor.UserID, sourceLibrary.Revision, a)
			case "target-member-revoked":
				_, err = repo.RemoveKnowledgeLibraryMember(ctx, targetLibrary.ID, actor.UserID, targetLibrary.Revision, a)
			case "competing-moves":
				input.ClientID = "competitor"
				var winner persistence.KnowledgeDocumentRecord
				winner, err = repo.ReserveKnowledgeDocument(ctx, input, actor)
				if err == nil {
					_, err = repo.CommitKnowledgeDocumentContent(ctx, winner.Document.ID, winner.Document.Revision, "winner-original", actor)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.CommitKnowledgeDocumentContent(ctx, target.Document.ID, expected, "target-original", actor); err == nil {
				t.Fatal("invalid or competing move committed")
			}
			currentTarget, err := repo.KnowledgeDocumentRecord(ctx, target.Document.ID, a)
			if err != nil || currentTarget.BodyRef != "" || currentTarget.Document.State != "uploading" {
				t.Fatal("failed transfer published target", err)
			}
			currentSource, err := repo.KnowledgeDocumentRecord(ctx, source.Document.ID, a)
			if err != nil {
				t.Fatal(err)
			}
			want := "queued"
			if scenario == "source-deleted" || scenario == "competing-moves" {
				want = "deleting"
			}
			if currentSource.Document.State != want {
				t.Fatalf("source changed after failed move: %s, want %s", currentSource.Document.State, want)
			}
			if scenario != "commit-rollback" {
				return
			}
			// Retiring the source and publishing the durable target are one commit.
			committed, err := repo.CommitKnowledgeDocumentContent(ctx, target.Document.ID, target.Document.Revision, "target-original", actor)
			if err != nil {
				t.Fatal(err)
			}
			currentSource, err = repo.KnowledgeDocumentRecord(ctx, source.Document.ID, a)
			if err != nil || currentSource.Document.State != "deleting" || committed.Document.State != "queued" {
				t.Fatal("move did not atomically switch access", err)
			}
			if committed.AccessPolicySHA256 != input.AccessPolicySHA256 || committed.SourceID != input.SourceID || committed.RemoteID == source.RemoteID {
				t.Fatal("target reused source ACL or remote identity")
			}
			// The response may be lost after commit. Recovery must not need the
			// deleted/revoked origin, and must not create another target.
			if _, err = repo.RemoveKnowledgeLibraryMember(ctx, sourceLibrary.ID, actor.UserID, sourceLibrary.Revision, a); err != nil {
				t.Fatal(err)
			}
			restarted := NewConversationStore(store)
			replayed, found, err := restarted.FindKnowledgeDocumentTransfer(ctx, targetLibrary.ID, "transfer", origin, actor)
			if err != nil || !found || replayed.Document.ID != target.Document.ID || replayed.BodyRef != "target-original" {
				t.Fatal("committed transfer receipt lost", err)
			}
			changed := origin
			changed.Mode = "copy"
			_, _, err = restarted.FindKnowledgeDocumentTransfer(ctx, targetLibrary.ID, "transfer", changed, actor)
			requireConversationCode(t, err, "idempotency_conflict")
			page, err := restarted.KnowledgeDocuments(ctx, targetLibrary.ID, "", 20, actor)
			if err != nil || len(page.Items) != 1 {
				t.Fatal("duplicate target document", err)
			}
			raw, err := json.Marshal(page)
			if err != nil || strings.Contains(string(raw), source.Document.ID) || strings.Contains(string(raw), sourceLibrary.ID) || strings.Contains(string(raw), "target-original") {
				t.Fatal("target metadata exposed restricted origin", err)
			}
		})
	}
}
