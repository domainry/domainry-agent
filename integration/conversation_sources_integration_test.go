package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

type snapshotReadRepository struct {
	*agentstore.ConversationStore
	sourceReads atomic.Int64
}

func (r *snapshotReadRepository) ConversationSourceSnapshot(ctx context.Context, ref sdk.ConversationRunReference, a sdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	r.sourceReads.Add(1)
	return r.ConversationStore.ConversationSourceSnapshot(ctx, ref, a)
}

func TestHistorySnapshotsSurviveSourceRevocationButFreshReadsDoNot(t *testing.T) {
	for _, turns := range []int{3, 48} {
		t.Run(fmt.Sprintf("follow_up_turns_%d", turns), func(t *testing.T) {
			verifyHistorySnapshotsAfterSourceRevocation(t, turns)
		})
	}
}

func verifyHistorySnapshotsAfterSourceRevocation(t *testing.T, followUpTurns int) {
	t.Helper()
	repo := &snapshotReadRepository{ConversationStore: conversationRepository(t)}
	a := conversationAuthority()
	var allowed atomic.Bool
	allowed.Store(true)
	var reads atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if !allowed.Load() {
			http.Error(w, "current resource permission denied", http.StatusForbidden)
			return
		}
		fmt.Fprint(w, `{"hits":[{"doc_id":"policy","content":"OBSERVED-VALUE-97","title":"费用规则"}]}`)
	}))
	defer upstream.Close()
	knowledge, err := newKnowledgeWithOfficialAdapter(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "snapshot-fixture", TeamID: "team", KBID: "kb", WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	policy := personalReadAuthorizer{}
	personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var original sdk.ConversationMessage
	model := &executionModel{}
	model.step = func(_ int, in sdk.ConversationStepRequest) (sdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" {
			switch last.Content {
			case "读取文档", "重新读取文档":
				return resultToolCall("knowledge_search", "lookup", map[string]string{"query": "费用规则"}), nil
			case "搜索旧回复":
				return resultToolCall("history_search", "search", map[string]string{"query": "OBSERVED-VALUE-97"}), nil
			case "引用旧回复":
				return resultToolCall("history_read", "read", map[string]string{"conversation_id": original.ConversationID, "message_id": original.ID}), nil
			}
		}
		if last.Role == "tool" && !strings.Contains(last.Content, "OBSERVED-VALUE-97") {
			if allowed.Load() || !strings.Contains(last.Content, `"status":"failed"`) {
				t.Errorf("fresh resource read did not fail closed: %s", last.Content)
			}
			return sourceSnapshotAnswer("当前资料读取被拒绝；旧消息仅代表历史观察。"), nil
		}
		if !strings.Contains(string(mustJSON(in.Messages)), "OBSERVED-VALUE-97") {
			t.Error("authorized historical observation was lost")
		}
		return sourceSnapshotAnswer(citedToolAnswer(in, "历史观察值为 OBSERVED-VALUE-97，不代表当前实时状态。")), nil
	}
	options := application.ConversationOptions{Knowledge: knowledge, ToolHost: personal, PersonalAuthorizer: policy}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	c, err := service.Create(t.Context(), sdk.ConversationCreate{ClientID: "snapshot-policy"}, a)
	if err != nil {
		t.Fatal(err)
	}
	send := func(id, text string) sdk.ConversationRun {
		t.Helper()
		run, err := service.Send(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: id, Message: text}, a)
		if err != nil {
			t.Fatal(err)
		}
		done := waitConversation(t, service, c.ID, run.ID)
		if done.Status != "completed" {
			t.Fatalf("run did not complete: %+v", done)
		}
		return done
	}
	first := send("initial", "读取文档")
	page, err := service.Messages(t.Context(), c.ID, sdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	original = page.Items[len(page.Items)-1]
	initialReads := reads.Load()
	if initialReads < 1 {
		t.Fatal("seed did not actually query the resource")
	}
	service.Close()
	allowed.Store(false)
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for index := 0; index < followUpTurns; index++ {
		text := "解释之前的结论"
		if index == 1 {
			text = "搜索旧回复"
		} else if index == 2 {
			text = "引用旧回复"
		}
		send(fmt.Sprintf("follow-%d", index), text)
	}
	t.Logf("owner snapshot replay: follow_up_turns=%d elapsed=%s historical_source_snapshot_reads=%d upstream_reads_before=%d upstream_reads_after=%d (in-process deterministic model; not production model latency)", followUpTurns, time.Since(start), repo.sourceReads.Load(), initialReads, reads.Load())
	view, err := service.Run(t.Context(), c.ID, first.ID, a)
	if err != nil || view.AccessError != "" || !strings.Contains(string(mustJSON(view.Steps)), "OBSERVED-VALUE-97") {
		t.Fatal("historical run was redacted", err)
	}
	events, err := service.Events(t.Context(), c.ID, first.ID, 0, 100, a)
	if err != nil || len(events.Items) == 0 {
		t.Fatal("owner event replay failed", err)
	}
	page, err = service.Messages(t.Context(), c.ID, sdk.ConversationMessageQuery{}, a)
	if err != nil || !strings.Contains(string(mustJSON(page)), "OBSERVED-VALUE-97") {
		t.Fatal("old messages changed", err)
	}
	duplicate, err := service.Send(t.Context(), c.ID, sdk.ConversationSend{ClientMessageID: "initial", Message: "读取文档"}, a)
	if err != nil || duplicate.ID != first.ID || duplicate.AccessError != "" {
		t.Fatal("idempotent response lost the saved snapshot", err)
	}
	if repo.sourceReads.Load() != 0 || reads.Load() != initialReads {
		t.Fatal("history APIs reopened business sources", repo.sourceReads.Load(), reads.Load(), initialReads)
	}
	fresh := send("fresh", "重新读取文档")
	if !strings.Contains(string(mustJSON(fresh.Steps)), `"status":"failed"`) || reads.Load() <= initialReads {
		t.Fatal("new operation bypassed live source authorization")
	}
	other := a
	other.UserID = "another-user"
	if _, err := service.Messages(t.Context(), c.ID, sdk.ConversationMessageQuery{}, other); err == nil {
		t.Fatal("conversation ownership bypassed")
	}
	if _, err := service.Events(t.Context(), c.ID, first.ID, 0, 100, other); err == nil {
		t.Fatal("event ownership bypassed")
	}
	if _, err := service.Run(t.Context(), c.ID, first.ID, other); err == nil {
		t.Fatal("run ownership bypassed")
	}
}

func sourceSnapshotAnswer(text string) sdk.ConversationStepResult {
	return sdk.ConversationStepResult{Message: sdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}
}
