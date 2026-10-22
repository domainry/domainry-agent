package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/persistence/sqlite"
	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func conversationTestAuthority() agentsdk.ConversationAuthority {
	return agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "member"}
}

func TestConversationSearchIndexExcludesAssistantOutput(t *testing.T) {
	if indexed := conversationMessageSearchText(agentsdk.ConversationMessage{Role: "assistant", Content: "protected result"}); indexed != "" {
		t.Fatalf("assistant output entered the conversation list search index: %q", indexed)
	}
	if indexed := conversationMessageSearchText(agentsdk.ConversationMessage{Role: "user", Content: "  Find\n  renewal   risk "}); indexed != "find renewal risk" {
		t.Fatalf("user message search text was not normalized: %q", indexed)
	}
}

func TestConversationFirstMessageGeneratesTitleAndHistoryIsSearchable(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "automatic-title"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "" || conversation.TitleOrigin != agentsdk.ConversationTitleOriginUnset || conversation.TitleStatus != agentsdk.ConversationTitleStatusPending {
		t.Fatalf("untitled conversation must remain locale-neutral and pending: %#v", conversation)
	}
	message := "  Review   the renewal pipeline and next steps  "
	if _, err = repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "automatic-title-message", Message: message}, authority); err != nil {
		t.Fatal(err)
	}
	conversation, err = repo.Get(t.Context(), conversation.ID, authority)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Title != "Review the renewal pipeline and next steps" || conversation.TitleOrigin != agentsdk.ConversationTitleOriginGenerated || conversation.TitleStatus != agentsdk.ConversationTitleStatusReady {
		t.Fatalf("first message did not generate the durable title: %#v", conversation)
	}
	page, err := repo.List(t.Context(), agentsdk.ConversationQuery{Search: "RENEWAL PIPELINE"}, authority)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != conversation.ID {
		t.Fatalf("message history was not searchable: %#v %v", page, err)
	}
}

func TestConversationExplicitTitleIsNotReplacedByFirstMessage(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "explicit-title", Title: "Quarterly planning"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.TitleOrigin != agentsdk.ConversationTitleOriginUser || conversation.TitleStatus != agentsdk.ConversationTitleStatusReady {
		t.Fatalf("explicit title state was not persisted: %#v", conversation)
	}
	if _, err = repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "explicit-title-message", Message: "This message must not become the title"}, authority); err != nil {
		t.Fatal(err)
	}
	conversation, err = repo.Get(t.Context(), conversation.ID, authority)
	if err != nil || conversation.Title != "Quarterly planning" || conversation.TitleOrigin != agentsdk.ConversationTitleOriginUser {
		t.Fatalf("first message replaced an explicit title: %#v %v", conversation, err)
	}
	var indexed string
	if err = store.Database().QueryRowContext(t.Context(), `SELECT search_text FROM _agent_conversation_items WHERE conversation_id = ? AND item_kind = 'message'`, conversation.ID).Scan(&indexed); err != nil || indexed != "this message must not become the title" {
		t.Fatalf("message search text was not indexed: %q %v", indexed, err)
	}
	page, err := repo.List(t.Context(), agentsdk.ConversationQuery{Search: "must   not become"}, authority)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != conversation.ID {
		t.Fatalf("message body search did not find the conversation: %#v %v", page, err)
	}
}

func TestConversationMessageReferencesArePersistedIndependentlyFromVisibleText(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "references"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	reference := agentsdk.ConversationReference{Kind: "product.record", Payload: json.RawMessage(`{"record_id":"record-1"}`)}
	if _, err = repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "reference-message", Message: "visible request", References: []agentsdk.ConversationReference{reference}}, authority); err != nil {
		t.Fatal(err)
	}
	messages, err := repo.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, authority)
	if err != nil || len(messages.Items) != 1 || messages.Items[0].Content != "visible request" || len(messages.Items[0].References) != 1 || string(messages.Items[0].References[0].Payload) != string(reference.Payload) {
		t.Fatalf("message reference was not persisted independently: %#v %v", messages.Items, err)
	}
}

func TestConversationBindingIsAStableOwnerScopedIdentity(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	authority := conversationTestAuthority()
	binding := &agentsdk.ConversationProductBinding{Kind: "product.record", Key: "account:one"}
	created, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bound-one", Title: "Original title", Binding: binding}, authority)
	if err != nil || created.Binding == nil || *created.Binding != *binding {
		t.Fatalf("bound conversation was not created: %#v %v", created, err)
	}
	reused, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bound-two", Title: "A presentation title is not identity", Binding: binding}, authority)
	if err != nil || reused.ID != created.ID {
		t.Fatalf("the same binding created a duplicate conversation: %#v %v", reused, err)
	}
	page, err := repo.List(t.Context(), agentsdk.ConversationQuery{BindingKind: binding.Kind, BindingKey: binding.Key}, authority)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("binding lookup did not return the bound conversation: %#v %v", page, err)
	}
	other, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "ordinary"}, authority)
	if err != nil || other.ID == created.ID || other.Binding != nil {
		t.Fatalf("ordinary conversations were constrained by bindings: %#v %v", other, err)
	}
}
func requireConversationCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *agentsdk.Error
	if !errors.As(err, &e) || e.Code != "agent.conversation."+code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestConversationAtomicIdempotencyAndOwnerIsolation(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	ctx := t.Context()
	a := conversationTestAuthority()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "client", Title: "100% plan"}, a)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "client", Title: "100% plan"}, a)
	if err != nil || replay.ID != c.ID {
		t.Fatalf("create replay %+v %v", replay, err)
	}
	_, err = repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "client", Title: "changed"}, a)
	requireConversationCode(t, err, "idempotency_conflict")
	var wg sync.WaitGroup
	results := make(chan agentsdk.ConversationRun, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "message", Message: "remember this"}, a)
			results <- r
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	runID := ""
	for r := range results {
		if runID != "" && r.ID != runID {
			t.Fatal("duplicate run")
		}
		runID = r.ID
	}
	page, err := repo.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("messages %+v %v", page, err)
	}
	_, err = repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "message", Message: "different"}, a)
	requireConversationCode(t, err, "idempotency_conflict")
	_, err = repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "second", Message: "blocked"}, a)
	requireConversationCode(t, err, "busy")
	for _, other := range []agentsdk.ConversationAuthority{{Known: true, RuntimeID: "other", WorkspaceID: a.WorkspaceID, UserID: a.UserID}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: "other", UserID: a.UserID}, {Known: true, RuntimeID: a.RuntimeID, WorkspaceID: a.WorkspaceID, UserID: "other"}} {
		_, err = repo.Get(ctx, c.ID, other)
		requireConversationCode(t, err, "not_found")
		_, err = repo.Run(ctx, c.ID, runID, other)
		requireConversationCode(t, err, "not_found")
		entries, err := repo.List(ctx, agentsdk.ConversationQuery{}, other)
		if err != nil || len(entries.Items) != 0 {
			t.Fatal("cross-owner list")
		}
	}
	a.RoleKey = "another-role"
	if _, err = repo.Get(ctx, c.ID, a); err != nil {
		t.Fatal("personal history should survive role switch", err)
	}
	_, err = repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "second", Title: "1000 plan"}, a)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repo.List(ctx, agentsdk.ConversationQuery{Search: "%"}, a)
	if err != nil || len(entries.Items) != 1 || entries.Items[0].ID != c.ID {
		t.Fatalf("literal search %+v %v", entries, err)
	}
	first, err := repo.List(ctx, agentsdk.ConversationQuery{Limit: 1}, a)
	if err != nil || first.NextCursor == "" {
		t.Fatal(err, "missing cursor")
	}
	rawCursor, decodeErr := base64.RawURLEncoding.DecodeString(first.NextCursor)
	_, physicalIDText, found := strings.Cut(string(rawCursor), ":")
	physicalID, parseErr := strconv.ParseInt(physicalIDText, 10, 64)
	if decodeErr != nil || !found || parseErr != nil || physicalID < 1 || physicalIDText == first.Items[0].ID {
		t.Fatalf("conversation cursor does not carry a physical id: cursor=%q business_id=%q", first.NextCursor, first.Items[0].ID)
	}
	second, err := repo.List(ctx, agentsdk.ConversationQuery{Limit: 1, BeforeID: first.NextCursor}, a)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("pagination %+v %v", second, err)
	}
}

func TestConversationEnqueuePublishesReceiptBeforeWorkerPlans(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	ctx := t.Context()
	a := conversationTestAuthority()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "planned-run"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "planned-message", Message: "处理这个请求"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if run.Plan != nil {
		t.Fatalf("queued run must be planned by the claimed worker: %+v", run.Plan)
	}
	events, err := repo.Events(ctx, c.ID, run.ID, 0, 10, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(events.Items) != 1 || events.Items[0].Type != "run.queued" {
		t.Fatalf("run receipt must be durable before worker planning: %+v", events.Items)
	}
}

func TestConversationRestartLeaseFenceAndFrozenInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "conversation.db")
	open := func(migrate bool) (*ConversationStore, *sql.DB) {
		db, err := sql.Open("sqlite", file)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if migrate {
			d, _ := ormdialect.New(ormdialect.SQLite)
			applyOperationMigrations(t, db, d.WithSchema(""))
			migrations, err := SchemaMigrations("sqlite", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range migrations {
				for _, q := range m.Statements {
					if _, err = db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		d, _ := ormdialect.New(ormdialect.SQLite)
		store, err := NewStore(db, d.WithSchema(""), sqlite.NewEngine(), "conversation-store-test")
		if err != nil {
			t.Fatal(err)
		}
		return newTestConversationStore(t, store), db
	}
	repo, db := open(true)
	a := conversationTestAuthority()
	ctx := t.Context()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "restart"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "m", Message: "original"}, a)
	if err != nil {
		t.Fatal(err)
	}
	old, ok, err := repo.Claim(ctx, a.RuntimeID, "old", time.Minute)
	if err != nil || !ok {
		t.Fatal(err, "claim")
	}
	input := agentsdk.ConversationModelRequest{Messages: []agentsdk.ConversationModelMessage{{Role: "user", Content: "original"}}, Purpose: "reply", IdempotencyKey: "stable", MaxOutputBytes: 1024}
	if _, _, err = repo.ModelInput(ctx, old, &input); err != nil {
		t.Fatal(err)
	}
	if err = repo.AppendDelta(ctx, old, 0, "durable partial"); err != nil {
		t.Fatal(err)
	}
	// Expire only after the snapshot writes. Race instrumentation may take
	// longer than a tiny lease to persist those writes; that is not the
	// abandoned-worker recovery condition this test intends to exercise.
	if renewed, err := repo.Heartbeat(ctx, old, time.Millisecond); err != nil || !renewed {
		t.Fatal("prepare expired lease", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	repo, db = open(false)
	defer db.Close()
	snapshot, err := repo.Run(ctx, c.ID, run.ID, a)
	if err != nil || snapshot.DraftText != "durable partial" {
		t.Fatal("draft lost on database reopen", err)
	}
	recovered, ok, err := repo.Claim(ctx, a.RuntimeID, "new", time.Minute)
	if err != nil || !ok || recovered.Fence <= old.Fence || recovered.Run.Attempt != 2 || recovered.Run.DraftText != "" {
		t.Fatalf("recovery %+v %v", recovered, err)
	}
	loaded, found, err := repo.ModelInput(ctx, recovered, nil)
	if err != nil || !found || loaded.IdempotencyKey != input.IdempotencyKey {
		t.Fatal("lost frozen input", err)
	}
	err = repo.Finish(ctx, old, agentsdk.ConversationModelResult{Content: "late"}, "")
	requireConversationCode(t, err, "lease_lost")
	if _, err = repo.Cancel(ctx, c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	err = repo.Finish(ctx, recovered, agentsdk.ConversationModelResult{Content: "late"}, "")
	requireConversationCode(t, err, "lease_lost")
	if _, err = repo.Resume(ctx, c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	final, ok, err := repo.Claim(ctx, a.RuntimeID, "final", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = repo.Finish(ctx, final, agentsdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	messages, err := repo.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 3 || messages.Items[1].Content != "durable partial" || messages.Items[1].Generation.Status != "interrupted" || messages.Items[2].Content != "done" {
		t.Fatalf("partial response was lost or a fenced late reply committed %+v %v", messages, err)
	}
	cursor := int64(0)
	terminal := false
	for !terminal {
		events, err := repo.Events(ctx, c.ID, run.ID, cursor, 1, a)
		if err != nil || len(events.Items) != 1 {
			t.Fatal("event pagination", err)
		}
		cursor = events.NextSeq
		terminal = events.Terminal
	}
	if cursor != 8 {
		t.Fatalf("lost event sequence: %d", cursor)
	}
	_, err = repo.Events(ctx, c.ID, run.ID, cursor+1, 1, a)
	requireConversationCode(t, err, "cursor_invalid")
	latest, err := repo.Get(ctx, c.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO _agent_conversation_work_budgets(runtime_id,workspace_id,root_conversation_id,updated_at,payload_json) VALUES(?,?,?,?,?)`, a.RuntimeID, a.WorkspaceID, c.ID, time.Now().UnixMilli(), `{}`); err != nil {
		t.Fatal(err)
	}
	if err = repo.Delete(ctx, c.ID, latest.Revision, a); err != nil {
		t.Fatal(err)
	}
	_, err = repo.Get(ctx, c.ID, a)
	requireConversationCode(t, err, "not_found")
	for _, table := range []string{conversationItemTable, agentRunTable, conversationWorkBudgetTable} {
		var count int
		if err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("delete %s count=%d err=%v", table, count, err)
		}
	}
}

func TestConversationDraftCommitReplayAndAttemptFence(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	ctx := t.Context()
	a := conversationTestAuthority()
	c, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "draft"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "m", Message: "question"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(ctx, a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = repo.AppendDelta(ctx, claim, 0, "你好"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Run(ctx, c.ID, run.ID, a)
	if err != nil || snapshot.DraftText != "你好" || snapshot.DraftBytes != 6 {
		t.Fatalf("missing atomic draft %+v %v", snapshot, err)
	}
	if err = repo.AppendDelta(ctx, claim, 0, "你好"); err != nil {
		t.Fatal("retry duplicated chunk", err)
	}
	replay, err := repo.Events(ctx, c.ID, run.ID, snapshot.LastEventSeq, 100, a)
	if err != nil || len(replay.Items) != 0 {
		t.Fatal("idempotent append added an event", err)
	}
	requireConversationCode(t, repo.AppendDelta(ctx, claim, 0, "您好"), "delta_conflict")
	requireConversationCode(t, repo.AppendDelta(ctx, claim, 8, "gap"), "delta_offset_invalid")
	messages, err := repo.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 2 || messages.Items[1].Content != "你好" || messages.Items[1].Generation == nil || messages.Items[1].Generation.Status != "generating" {
		t.Fatal("public in-flight response was not retained", err)
	}
	cancelled, err := repo.Cancel(ctx, c.ID, run.ID, a)
	if err != nil || cancelled.DraftText != "你好" {
		t.Fatal("cancel lost draft", err)
	}
	requireConversationCode(t, repo.AppendDelta(ctx, claim, 6, "late"), "lease_lost")
	resumed, err := repo.Resume(ctx, c.ID, run.ID, a)
	if err != nil || resumed.DraftText != "" {
		t.Fatal("resume retained old draft", err)
	}
	next, ok, err := repo.Claim(ctx, a.RuntimeID, "worker", time.Minute)
	if err != nil || !ok || next.Run.Attempt != 2 {
		t.Fatal(err)
	}
	if err = repo.AppendDelta(ctx, next, 0, "done"); err != nil {
		t.Fatal(err)
	}
	requireConversationCode(t, repo.Finish(ctx, next, agentsdk.ConversationModelResult{Content: "different"}, ""), "draft_mismatch")
	if err = repo.Finish(ctx, next, agentsdk.ConversationModelResult{Content: "done"}, ""); err != nil {
		t.Fatal(err)
	}
	done, err := repo.Run(ctx, c.ID, run.ID, a)
	if err != nil || done.DraftText != "" || done.DraftBytes != 0 || done.AssistantMessageID == "" {
		t.Fatal("completed draft not promoted", err)
	}
	messages, err = repo.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 3 || messages.Items[1].Content != "你好" || messages.Items[1].Generation.Status != "interrupted" || messages.Items[2].Content != "done" || messages.Items[2].Generation.Status != "completed" || messages.Items[1].ID == messages.Items[2].ID {
		t.Fatal("incorrect history after resume", err)
	}
	events, err := repo.Events(ctx, c.ID, run.ID, snapshot.LastEventSeq, 100, a)
	if err != nil || !events.Terminal || events.NextSeq != done.LastEventSeq {
		t.Fatal("snapshot event cursor mismatch", err)
	}
}
