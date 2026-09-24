package agent

import (
	"bytes"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
)

func TestAgentSubjectLifecycleErasesOwnedExecutionGraphOnly(t *testing.T) {
	store, db := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	a.UserID = "alice"
	b := a
	b.UserID = "bob"
	alice, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "alice-conversation", Title: "Alice"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), alice.ID, agentsdk.ConversationSend{ClientMessageID: "alice-message", Message: "Alice execution secret"}, a); err != nil {
		t.Fatal(err)
	}
	bob, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bob-conversation", Title: "Bob"}, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Enqueue(t.Context(), bob.ID, agentsdk.ConversationSend{ClientMessageID: "bob-message", Message: "Bob execution secret"}, b); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{alice.ID, bob.ID} {
		if _, err = db.ExecContext(t.Context(), `INSERT INTO _agent_conversation_work_budgets(runtime_id,workspace_id,root_conversation_id,updated_at,payload_json) VALUES(?,?,?,?,?)`, a.RuntimeID, a.WorkspaceID, root, int64(1), `{}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = repo.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "prefs:alice.v1", Title: "Alice preference", Content: "private", Enabled: true}, a); err != nil {
		t.Fatal(err)
	}
	lifecycle := NewSubjectLifecycle(store, a.RuntimeID)
	preview, err := lifecycle.PreviewSubject(t.Context(), a.WorkspaceID, a.UserID)
	if err != nil || !bytes.Contains(preview, []byte(`"_agent_conversations":1`)) || !bytes.Contains(preview, []byte(`"_agent_runs":1`)) || !bytes.Contains(preview, []byte(`"_agent_conversation_items":`)) || !bytes.Contains(preview, []byte(`"_agent_user_memories":1`)) {
		t.Fatalf("preview=%s err=%v", preview, err)
	}
	exported, err := lifecycle.ExportSubjectForRequest(t.Context(), "export", a.WorkspaceID, a.UserID)
	if err != nil || !bytes.Contains(exported, []byte("Alice execution secret")) || bytes.Contains(exported, []byte("Bob execution secret")) {
		t.Fatalf("export=%s err=%v", exported, err)
	}
	if _, err = lifecycle.EraseSubjectForRequest(t.Context(), "held", a.WorkspaceID, a.UserID, []lifecyclemodel.LegalHold{{ID: "hold"}}); err == nil {
		t.Fatal("legal hold did not block Agent erasure")
	}
	receipt, err := lifecycle.EraseSubjectForRequest(t.Context(), "erase-alice", a.WorkspaceID, a.UserID, nil)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := lifecycle.EraseSubjectForRequest(t.Context(), "erase-alice", a.WorkspaceID, a.UserID, nil)
	if err != nil || !bytes.Equal(receipt, replayed) {
		t.Fatalf("receipt replay=%s err=%v", replayed, err)
	}
	var steps int
	if err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _subject_steps WHERE workspace_id=? AND request_id=? AND owner='agent' AND operation='erase'`, a.WorkspaceID, "erase-alice").Scan(&steps); err != nil || steps != 1 {
		t.Fatalf("shared Agent subject steps=%d err=%v", steps, err)
	}
	if _, err = repo.Get(t.Context(), alice.ID, a); err == nil {
		t.Fatal("Alice conversation survived")
	}
	if memories, err := repo.Memories(t.Context(), a); err != nil || len(memories) != 0 {
		t.Fatalf("Alice memories=%+v err=%v", memories, err)
	}
	if _, err = repo.Get(t.Context(), bob.ID, b); err != nil {
		t.Fatal("Bob conversation was removed", err)
	}
	var aliceBudgets, bobBudgets int
	if err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_work_budgets WHERE root_conversation_id=?`, alice.ID).Scan(&aliceBudgets); err != nil || aliceBudgets != 0 {
		t.Fatalf("Alice work budgets=%d err=%v", aliceBudgets, err)
	}
	if err = db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_conversation_work_budgets WHERE root_conversation_id=?`, bob.ID).Scan(&bobBudgets); err != nil || bobBudgets != 1 {
		t.Fatalf("Bob work budgets=%d err=%v", bobBudgets, err)
	}
}
