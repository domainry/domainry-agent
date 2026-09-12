package agent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func TestConversationLifecycleArchivesCompleteGraphAndFencesPurge(t *testing.T) {
	store, db := openAgentStore(t)
	repo := NewConversationStore(store)
	a := conversationTestAuthority()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "retained", Title: "Retained"}, a)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := repo.ReserveAttachment(t.Context(), agentpersistence.ConversationAttachmentReserve{ClientID: "retained-attachment", ConversationID: conversation.ID, Filename: "owned-by-knowledge.txt", ContentType: "text/plain", SHA256: strings.Repeat("a", 64), Bytes: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "retained-message", Message: "retained user input"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Cancel(t.Context(), conversation.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	conversation, err = repo.Get(t.Context(), conversation.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	archived := true
	conversation, err = repo.Update(t.Context(), conversation.ID, agentsdk.ConversationUpdate{ExpectedRevision: conversation.Revision, Archived: &archived}, a)
	if err != nil {
		t.Fatal(err)
	}
	conversation.UpdatedAt = old
	if _, err = db.ExecContext(t.Context(), `UPDATE _agent_conversations SET updated_at=?, payload_json=? WHERE owner_key=? AND conversation_id=?`, old.UnixMilli(), conversationJSON(conversation), conversationOwner(a), conversation.ID); err != nil {
		t.Fatal(err)
	}
	external := []byte(`{"call":{"name":"web_fetch"},"result":{"content":"retained external response"}}`)
	if _, err = db.ExecContext(t.Context(), `INSERT INTO _agent_conversation_tool_calls(owner_key,conversation_id,run_id,step_no,payload_json,call_key) VALUES(?,?,?,?,?,?)`, conversationOwner(a), conversation.ID, run.ID, 1, external, conversationHash("web_fetch")); err != nil {
		t.Fatal(err)
	}
	active, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "active", Title: "Active"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), active.ID, agentsdk.ConversationSend{ClientMessageID: "active-message", Message: "must not purge"}, a); err != nil {
		t.Fatal(err)
	}
	active.Archived, active.UpdatedAt = true, old
	if _, err = db.ExecContext(t.Context(), `UPDATE _agent_conversations SET archived=1, updated_at=?, payload_json=? WHERE owner_key=? AND conversation_id=?`, old.UnixMilli(), conversationJSON(active), conversationOwner(a), active.ID); err != nil {
		t.Fatal(err)
	}
	lifecycle := NewLifecycleStore(store)
	candidates, err := lifecycle.ListLifecycleCandidates(t.Context(), a.WorkspaceID, agentpersistence.LifecycleQuery{PolicyKey: "agent.dialog.v1", Now: now, Retention: 24 * time.Hour, StatusRetention: map[string]time.Duration{"archived": 24 * time.Hour}})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	candidate := candidates[0]
	if candidate.ResourceType != "agent.conversation" || candidate.ResourceID != conversation.ID || candidate.Revision != conversation.Revision || !candidate.UpdatedAt.Equal(old) || !bytes.Contains(candidate.Payload, []byte("retained user input")) || !bytes.Contains(candidate.Payload, []byte("retained external response")) {
		t.Fatalf("candidate=%+v payload=%s", candidate, candidate.Payload)
	}
	if deleted, err := lifecycle.DeleteLifecycleCandidate(t.Context(), "another-workspace", candidate); err != nil || deleted {
		t.Fatalf("cross-workspace deleted=%v err=%v", deleted, err)
	}
	stale := candidate
	stale.Revision++
	if deleted, err := lifecycle.DeleteLifecycleCandidate(t.Context(), a.WorkspaceID, stale); err != nil || deleted {
		t.Fatalf("stale deleted=%v err=%v", deleted, err)
	}
	if deleted, err := lifecycle.DeleteLifecycleCandidate(t.Context(), a.WorkspaceID, candidate); err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	if _, err = repo.Get(t.Context(), conversation.ID, a); err == nil {
		t.Fatal("eligible conversation survived purge")
	}
	var calls int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM _agent_conversation_tool_calls WHERE owner_key=? AND conversation_id=?`, conversationOwner(a), conversation.ID).Scan(&calls); err != nil || calls != 0 {
		t.Fatalf("tool calls=%d err=%v", calls, err)
	}
	if _, err = repo.Get(t.Context(), active.ID, a); err != nil {
		t.Fatal("active run was purged", err)
	}
	if record, err := repo.AttachmentRecord(t.Context(), attachment.Attachment.ID, a); err != nil || record.Attachment.ID != attachment.Attachment.ID {
		t.Fatalf("retention purge crossed into Knowledge=%+v err=%v", record, err)
	}
}
