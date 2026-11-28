package agent

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestHistoryMatchExcerptKeepsLateUTF8Evidence(t *testing.T) {
	content := strings.Repeat("前置内容。", 300) + "生产区域最终迁移到东京。" + strings.Repeat("后置内容。", 300)
	excerpt := historyMatchExcerpt(content, "迁移到东京", 256)
	if !utf8.ValidString(excerpt) || !strings.Contains(excerpt, "迁移到东京") || len(excerpt) > 262 || !strings.HasPrefix(excerpt, "…") || !strings.HasSuffix(excerpt, "…") {
		t.Fatalf("late evidence was not preserved in a bounded UTF-8 excerpt: bytes=%d excerpt=%q", len(excerpt), excerpt)
	}
}

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

func TestRecentMemoryHistoryIsOwnerScopedIncludesAssistantAndExcludesCurrentConversation(t *testing.T) {
	store, _ := openAgentStore(t)
	repository := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	current, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-current"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	older, err := repository.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "history-older"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	insert := func(owner string, conversationID, id, role, content string, seq int64) {
		t.Helper()
		message := agentsdk.ConversationMessage{ID: id, ConversationID: conversationID, Seq: seq, Role: role, Content: content, CreatedAt: now.Add(time.Duration(seq) * time.Millisecond)}
		if _, insertErr := store.Database().ExecContext(t.Context(), `INSERT INTO _agent_conversation_items(owner_key, conversation_id, item_kind, item_key, reference_id, run_id, seq, payload_json) VALUES (?, ?, 'message', ?, ?, '', ?, ?)`, owner, conversationID, id, id, seq, conversationJSON(message)); insertErr != nil {
			t.Fatal(insertErr)
		}
	}
	insert(conversationOwner(authority), current.ID, "current-user", "user", "current", 1)
	insert(conversationOwner(authority), older.ID, "older-assistant", "assistant", "assistant claim", 1)
	insert(conversationOwner(authority), older.ID, "older-user", "user", "Tokyo production region", 2)
	other := authority
	other.UserID = "other-user"
	insert(conversationOwner(other), older.ID, "other-user-message", "user", "private", 3)

	hits, err := repository.RecentMemoryHistory(t.Context(), current.ID, 10, authority)
	if err != nil || len(hits) != 2 || hits[0].MessageID != "older-user" || hits[0].Role != "user" || hits[1].MessageID != "older-assistant" || hits[1].Role != "assistant" {
		t.Fatalf("recent history leaked or omitted messages: %+v err=%v", hits, err)
	}
}
