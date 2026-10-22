package agent

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationArtifactsUsePhysicalIDCursor(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	write := func(client, title string) agentsdk.ConversationArtifact {
		t.Helper()
		var artifact agentsdk.ConversationArtifact
		err := repository.transaction(t.Context(), func(tx *sql.Tx) error {
			record, saveErr := repository.saveArtifact(t.Context(), tx, artifactWrite(t, client, "", 0, title, title), authority)
			artifact = record.Artifact
			return saveErr
		})
		if err != nil {
			t.Fatal(err)
		}
		return artifact
	}
	first := write("artifact-cursor-1", "产物一")
	second := write("artifact-cursor-2", "产物二")

	firstPage, err := repository.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{Limit: 1}, authority)
	if err != nil || len(firstPage.Items) != 1 || firstPage.Items[0].ID != second.ID || firstPage.Complete || firstPage.NextCursor == "" {
		t.Fatalf("first artifact page mismatch: page=%+v err=%v first=%s", firstPage, err, first.ID)
	}
	raw, decodeErr := base64.RawURLEncoding.DecodeString(firstPage.NextCursor)
	var cursor artifactCursor
	if decodeErr != nil || json.Unmarshal(raw, &cursor) != nil || cursor.ID < 1 {
		t.Fatalf("artifact cursor does not carry a physical id: cursor=%q decoded=%+v err=%v", firstPage.NextCursor, cursor, decodeErr)
	}
	secondPage, err := repository.Artifacts(t.Context(), agentsdk.ConversationArtifactQuery{Limit: 1, Cursor: firstPage.NextCursor}, authority)
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].ID != first.ID || !secondPage.Complete || secondPage.NextCursor != "" {
		t.Fatalf("second artifact page mismatch: page=%+v err=%v", secondPage, err)
	}
}

func TestConversationHistoryUsesPhysicalIDCursor(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-physical-cursor"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(id string, seq int64) {
		t.Helper()
		message := agentsdk.ConversationMessage{ID: id, ConversationID: conversation.ID, Seq: seq, Role: "user", Content: "physical cursor", CreatedAt: now.Add(time.Duration(seq) * time.Millisecond)}
		_, insertErr := store.Database().ExecContext(t.Context(), `INSERT INTO _agent_conversation_items(owner_key, conversation_id, item_kind, item_key, reference_id, run_id, seq, payload_json) VALUES (?, ?, 'message', ?, ?, '', ?, ?)`, conversationOwner(authority), conversation.ID, id, id, seq, conversationJSON(message))
		if insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	insert("message_z", 1)
	insert("message_a", 2)

	firstPage, err := repository.SearchHistory(t.Context(), agentsdk.ConversationHistorySearch{Query: "physical cursor", Limit: 1}, authority)
	if err != nil || len(firstPage.Items) != 1 || firstPage.Items[0].MessageID != "message_a" || firstPage.Complete || firstPage.NextCursor == "" {
		t.Fatalf("first history page mismatch: page=%+v err=%v", firstPage, err)
	}
	raw, decodeErr := base64.RawURLEncoding.DecodeString(firstPage.NextCursor)
	var cursor historyCursor
	if decodeErr != nil || unmarshalDurableJSON(raw, &cursor) != nil || cursor.Before < 1 {
		t.Fatalf("history cursor does not carry a physical id: cursor=%q decoded=%+v err=%v", firstPage.NextCursor, cursor, decodeErr)
	}
	secondPage, err := repository.SearchHistory(t.Context(), agentsdk.ConversationHistorySearch{Query: "physical cursor", Limit: 1, Cursor: firstPage.NextCursor}, authority)
	if err != nil || len(secondPage.Items) != 1 || secondPage.Items[0].MessageID != "message_z" || !secondPage.Complete || secondPage.NextCursor != "" {
		t.Fatalf("second history page mismatch: page=%+v err=%v", secondPage, err)
	}
}
