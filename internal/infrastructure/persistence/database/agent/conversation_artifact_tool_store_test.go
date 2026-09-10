package agent

import (
	"fmt"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func TestArtifactToolEffectResultAndEventCommitTogether(t *testing.T) {
	for _, name := range []string{"artifact_create", "artifact_export"} {
		t.Run(name, func(t *testing.T) {
			store, _ := openAgentStore(t)
			repo := NewConversationStore(store)
			arguments := `{"title":"周报","content":{"kind":"markdown","markdown":"正文"}}`
			var original persistence.ConversationArtifactRecord
			if name == "artifact_export" {
				var err error
				original, err = repo.SaveArtifact(t.Context(), artifactWrite(t, "original", "", 0, "周报", "正文"), conversationTestAuthority())
				if err != nil {
					t.Fatal(err)
				}
				arguments = fmt.Sprintf(`{"id":%q,"version":1,"format":"markdown"}`, original.Artifact.ID)
			}
			claim, request := personalMutationFixture(t, repo, name, name, arguments, true)
			client := "tool_" + conversationHash(request.IdempotencyKey)
			digest := conversationHash([]any{request.Definition, request.Call})
			prepared := persistence.ConversationArtifactToolMutation{}
			if name == "artifact_create" {
				write := artifactWrite(t, client, "", 0, "周报", "正文")
				write.RequestSHA256 = digest
				write.Record.Artifact.SourceConversationID = request.ConversationID
				write.Record.Artifact.SourceRunID = request.RunID
				write.Record.Sources.Runs = []agentsdk.ConversationRunReference{{ConversationID: request.ConversationID, RunID: request.RunID, BeforeStep: 1}}
				prepared.Write = &write
			} else {
				prepared.Export = &persistence.ConversationArtifactExportWrite{ClientID: client, RequestSHA256: digest, TTLSeconds: 3600, Export: agentsdk.ConversationArtifactExport{ArtifactID: original.Artifact.ID, Version: 1, Format: "markdown", Filename: original.Artifact.ID + "-v1.md", ContentType: "text/markdown; charset=utf-8", SHA256: artifact.Hash([]byte("正文")), Bytes: len("正文")}}
			}
			_, err := store.Database().ExecContext(t.Context(), `CREATE TRIGGER fail_artifact_receipt BEFORE INSERT ON _agent_conversation_events WHEN CAST(NEW.payload_json AS TEXT) LIKE '%tool.completed%' BEGIN SELECT RAISE(ABORT, 'injected artifact receipt failure'); END`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.ApplyArtifactTool(t.Context(), request, prepared); err == nil {
				t.Fatal("receipt failure ignored")
			}
			var count int
			if name == "artifact_create" {
				err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_artifact_versions`).Scan(&count)
			} else {
				err = store.Database().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_artifact_exports`).Scan(&count)
			}
			if err != nil || count != 0 {
				t.Fatal("artifact effect survived missing receipt", err)
			}
			ledger, err := repo.ExecutionTools(t.Context(), claim, 0)
			if err != nil || len(ledger) != 1 || ledger[0].State != "started" {
				t.Fatal("ledger did not roll back with effect", err)
			}
			if _, err = store.Database().ExecContext(t.Context(), `DROP TRIGGER fail_artifact_receipt`); err != nil {
				t.Fatal(err)
			}
			result, err := repo.ApplyArtifactTool(t.Context(), request, prepared)
			if err != nil || result.Status != "completed" {
				t.Fatal("artifact retry failed", err, result.ErrorCode)
			}
			repo = NewConversationStore(store)
			// The ledger alone recovers a committed effect after a lost response.
			replayed, err := repo.ApplyArtifactTool(t.Context(), request, persistence.ConversationArtifactToolMutation{})
			if err != nil || conversationHash(replayed) != conversationHash(result) {
				t.Fatal("artifact receipt lost on restart", err)
			}
			if err = repo.FinishExecutionTool(t.Context(), claim, 0, request.Call.ID, result); err != nil {
				t.Fatal(err)
			}
			events, err := repo.Events(t.Context(), request.ConversationID, request.RunID, 0, 100, request.Authority)
			if err != nil {
				t.Fatal(err)
			}
			completed := 0
			for _, event := range events.Items {
				if event.Type == "tool.completed" {
					completed++
				}
			}
			if completed != 1 {
				t.Fatal("replay duplicated completion event")
			}
			changed := request
			changed.Call.Arguments = `{}`
			if _, err = repo.ApplyArtifactTool(t.Context(), changed, prepared); err == nil {
				t.Fatal("changed frozen input reused a receipt")
			}
		})
	}
}
