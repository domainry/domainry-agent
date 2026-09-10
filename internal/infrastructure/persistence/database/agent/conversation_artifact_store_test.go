package agent

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/artifact"
)

func artifactWrite(t *testing.T, client, id string, version int64, title, markdown string) persistence.ConversationArtifactWrite {
	t.Helper()
	raw, hash, err := artifact.Encode(agentsdk.ConversationArtifactContent{Kind: "markdown", Markdown: markdown})
	if err != nil {
		t.Fatal(err)
	}
	return persistence.ConversationArtifactWrite{ClientID: client, ExpectedVersion: version, Record: persistence.ConversationArtifactRecord{Artifact: agentsdk.ConversationArtifact{ID: id, Title: title, Kind: "markdown", SHA256: hash, Bytes: len(raw)}, Body: raw, Sources: &agentsdk.ConversationSources{Version: 1}}}
}

func TestArtifactVersionsReceiptsConcurrentEditsAndOwnerIsolation(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	input := artifactWrite(t, "create", "", 0, "周报", "## 第一节\n进展\n## 第二节\n风险")
	first, err := repo.SaveArtifact(ctx, input, a)
	if err != nil || first.Artifact.Version != 1 {
		t.Fatal(err)
	}
	duplicate, err := repo.SaveArtifact(ctx, input, a)
	if err != nil || conversationHash(duplicate) != conversationHash(first) {
		t.Fatal("create replay changed version", err)
	}
	changed := input
	changed.Record.Artifact.Title = "不同的请求"
	_, err = repo.SaveArtifact(ctx, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")
	second, err := repo.SaveArtifact(ctx, artifactWrite(t, "edit", first.Artifact.ID, 1, "周报", "## 第一节\n进展\n## 第二节\n等待审批"), a)
	if err != nil || second.Artifact.Version != 2 || !second.Artifact.CreatedAt.Equal(first.Artifact.CreatedAt) {
		t.Fatal("edit did not preserve artifact identity", err)
	}
	_, err = repo.SaveArtifact(ctx, artifactWrite(t, "stale", first.Artifact.ID, 1, "过期编辑", "覆盖"), a)
	requireConversationCode(t, err, "revision_conflict")
	var succeeded atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		command := artifactWrite(t, fmt.Sprintf("parallel-%d", i), first.Artifact.ID, 2, "并发编辑", fmt.Sprintf("revision from editor %d", i))
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := repo.SaveArtifact(ctx, command, a); err == nil {
				succeeded.Add(1)
			}
		}()
	}
	group.Wait()
	if succeeded.Load() != 1 {
		t.Fatal("concurrent editors did not compare the head version")
	}
	// Reopening the repository and replaying the initial request cannot roll
	// the current head back to the creation receipt's immutable version.
	repo = NewConversationStore(store)
	duplicate, err = repo.SaveArtifact(ctx, input, a)
	if err != nil || duplicate.Artifact.Version != 1 {
		t.Fatal(err)
	}
	current, err := repo.ArtifactRecord(ctx, first.Artifact.ID, 0, a)
	if err != nil || current.Artifact.Version != 3 {
		t.Fatal("creation replay changed current head", err)
	}
	original, err := repo.ArtifactRecord(ctx, first.Artifact.ID, 1, a)
	if err != nil || conversationHash(original) != conversationHash(first) {
		t.Fatal("old revision mutated", err)
	}
	var before int64
	for want := int64(3); want >= 1; want-- {
		page, err := repo.ArtifactVersions(ctx, first.Artifact.ID, before, 1, a)
		if err != nil || len(page.Items) != 1 || page.Items[0].Version != want || page.Complete != (want == 1) {
			t.Fatal("version pagination skipped or repeated a revision", err)
		}
		before = page.NextBefore
	}
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}} {
		if _, err := repo.ArtifactRecord(ctx, first.Artifact.ID, 1, other); err == nil {
			t.Fatal("cross-owner revision read")
		}
		if _, err := repo.SaveArtifact(ctx, artifactWrite(t, "cross", first.Artifact.ID, 3, "bad", "bad"), other); err == nil {
			t.Fatal("cross-owner edit")
		}
		page, err := repo.Artifacts(ctx, agentsdk.ConversationArtifactQuery{}, other)
		if err != nil || len(page.Items) != 0 {
			t.Fatal("cross-owner list", err)
		}
	}
	if _, err := repo.SaveArtifact(ctx, artifactWrite(t, "another", "", 0, "另一个成果", "内容"), a); err != nil {
		t.Fatal(err)
	}
	page, err := repo.Artifacts(ctx, agentsdk.ConversationArtifactQuery{Limit: 1}, a)
	if err != nil || page.Complete || len(page.Items) != 1 {
		t.Fatal("first artifact page", err)
	}
	next, err := repo.Artifacts(ctx, agentsdk.ConversationArtifactQuery{Limit: 1, Cursor: page.NextCursor}, a)
	if err != nil || !next.Complete || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("artifact pagination", err)
	}
	_, err = repo.Artifacts(ctx, agentsdk.ConversationArtifactQuery{Limit: 1, Cursor: page.NextCursor, Query: "changed"}, a)
	requireConversationCode(t, err, "artifact_cursor_invalid")
}

func TestArtifactHeadVersionAndReceiptCommitTogether(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	if _, err := store.Database().ExecContext(ctx, `CREATE TRIGGER fail_artifact_receipt BEFORE INSERT ON _agent_artifact_mutations BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	in := artifactWrite(t, "create", "", 0, "周报", "原文")
	if _, err := repo.SaveArtifact(ctx, in, a); err == nil {
		t.Fatal("receipt failure accepted")
	}
	page, err := repo.Artifacts(ctx, agentsdk.ConversationArtifactQuery{}, a)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("head escaped rollback", err)
	}
	var count int
	if err := store.Database().QueryRowContext(ctx, `SELECT COUNT(*) FROM _agent_artifact_versions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revision escaped rollback", err)
	}
	if _, err := store.Database().ExecContext(ctx, `DROP TRIGGER fail_artifact_receipt`); err != nil {
		t.Fatal(err)
	}
	first, err := repo.SaveArtifact(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Database().ExecContext(ctx, `CREATE TRIGGER fail_artifact_version BEFORE INSERT ON _agent_artifact_versions WHEN NEW.version = 2 BEGIN SELECT RAISE(ABORT, 'injected revision failure'); END`); err != nil {
		t.Fatal(err)
	}
	edit := artifactWrite(t, "edit", first.Artifact.ID, 1, "周报", "新内容")
	if _, err := repo.SaveArtifact(ctx, edit, a); err == nil {
		t.Fatal("version failure accepted")
	}
	current, err := repo.ArtifactRecord(ctx, first.Artifact.ID, 0, a)
	if err != nil || current.Artifact.Version != 1 {
		t.Fatal("failed revision advanced the head", err)
	}
	if _, err := store.Database().ExecContext(ctx, `DROP TRIGGER fail_artifact_version`); err != nil {
		t.Fatal(err)
	}
	current, err = repo.SaveArtifact(ctx, edit, a)
	if err != nil || current.Artifact.Version != 2 {
		t.Fatal("failed receipt prevented retry", err)
	}
}

func TestArtifactExportBindsVersionExpiresAndAuditsDownloads(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	first, err := repo.SaveArtifact(ctx, artifactWrite(t, "create", "", 0, "周报", "original report"), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveArtifact(ctx, artifactWrite(t, "edit", first.Artifact.ID, 1, "周报", "replacement report"), a); err != nil {
		t.Fatal(err)
	}
	input := persistence.ConversationArtifactExportWrite{ClientID: "export", TTLSeconds: 3600, Export: agentsdk.ConversationArtifactExport{ArtifactID: first.Artifact.ID, Version: 1, Format: "markdown", Filename: first.Artifact.ID + "-v1.md", ContentType: "text/markdown; charset=utf-8", SHA256: artifact.Hash([]byte("original report")), Bytes: len("original report")}}
	export, err := repo.SaveArtifactExport(ctx, input, a)
	if err != nil || export.Version != 1 || export.Downloads != 0 || export.ExpiresAt.Sub(export.CreatedAt) != time.Hour {
		t.Fatal("export lost version or expiry", err)
	}
	replay, err := repo.SaveArtifactExport(ctx, input, a)
	if err != nil || conversationHash(replay) != conversationHash(export) {
		t.Fatal("export replay renewed the file", err)
	}
	invalid := input
	invalid.ClientID = "invalid-hash"
	invalid.Export.SHA256 = artifact.Hash([]byte("replacement report"))
	_, err = repo.SaveArtifactExport(ctx, invalid, a)
	requireConversationCode(t, err, "artifact_export_mismatch")
	other := a
	other.UserID = "other"
	if _, err := repo.RecordArtifactDownload(ctx, export.ID, other); err == nil {
		t.Fatal("cross-user export consumed")
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := repo.RecordArtifactDownload(ctx, export.ID, a); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	audited, err := repo.ArtifactExport(ctx, export.ID, a)
	if err != nil || audited.Downloads != 8 || audited.LastDownloadedAt == nil {
		t.Fatal("download audit lost updates", err)
	}
	// Move only this test record past expiry, avoiding a wall-clock sleep.
	audited.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := store.Database().ExecContext(ctx, `UPDATE _agent_artifact_exports SET expires_at=?,payload_json=? WHERE owner_key=? AND export_id=?`, audited.ExpiresAt.UnixMilli(), conversationJSON(audited), conversationOwner(a), export.ID); err != nil {
		t.Fatal(err)
	}
	_, err = repo.RecordArtifactDownload(ctx, export.ID, a)
	requireConversationCode(t, err, "artifact_export_expired")
	audited, err = repo.ArtifactExport(ctx, export.ID, a)
	if err != nil || audited.Downloads != 8 {
		t.Fatal("expired download was counted", err)
	}
}

func TestArtifactSourcesCannotCrossOwnersOrDisappearOnEdit(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	ctx := t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "source", Message: "资料"}, a)
	if err != nil {
		t.Fatal(err)
	}
	in := artifactWrite(t, "from-source", "", 0, "资料周报", "受控内容")
	in.Record.Artifact.SourceConversationID = c.ID
	in.Record.Artifact.SourceRunID = run.ID
	in.Record.Sources.Runs = []agentsdk.ConversationRunReference{{ConversationID: c.ID, RunID: run.ID}}
	first, err := repo.SaveArtifact(ctx, in, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveArtifact(ctx, artifactWrite(t, "drop-source", first.Artifact.ID, 1, "新标题", "replacement"), a)
	requireConversationCode(t, err, "artifact_sources_invalid")
	other := a
	other.UserID = "other"
	if _, err := repo.SaveArtifact(ctx, in, other); err == nil {
		t.Fatal("foreign conversation claimed as source")
	}
}
