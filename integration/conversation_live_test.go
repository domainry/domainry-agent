package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

type liveConversationRecorder struct {
	t                            *testing.T
	model                        *provider.ConversationModel
	mu                           sync.Mutex
	replies, summaries, maxBytes int
	last                         agentsdk.ConversationModelRequest
}

func (m *liveConversationRecorder) record(in agentsdk.ConversationModelRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := json.Marshal(in.Messages)
	m.maxBytes = max(m.maxBytes, len(raw))
	if in.Purpose == "summary" {
		m.summaries++
	} else {
		m.replies++
		m.last = in
	}
}
func (m *liveConversationRecorder) GenerateConversation(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	m.record(in)
	result, err := m.model.GenerateConversation(ctx, in)
	if in.Purpose == "summary" {
		if err != nil {
			m.t.Logf("summary provider diagnostic: %v", err)
		} else {
			m.t.Logf("summary received: bytes=%d bare_json=%t fenced_json=%t", len(result.Content), json.Valid([]byte(result.Content)), strings.HasPrefix(strings.TrimSpace(result.Content), "```"))
		}
	}
	return result, err
}
func (m *liveConversationRecorder) StreamConversation(ctx context.Context, in agentsdk.ConversationModelRequest, emit func(string) error) (agentsdk.ConversationModelResult, error) {
	m.record(in)
	return m.model.StreamConversation(ctx, in, emit)
}

// This opt-in acceptance exercises real compaction with the DEFAULT 64 KiB
// budget and reopens a private SQLite database. It never uses the playground DB.
func TestGatewayLiveLongConversation(t *testing.T) {
	if os.Getenv("AGENT_CONVERSATION_LONG_LIVE") != "1" {
		t.Skip("set AGENT_CONVERSATION_LONG_LIVE=1 and configure Gateway to run real long-conversation acceptance")
	}
	config := provider.ConversationModelConfigFromEnvironment()
	if config.Provider != provider.ConversationProviderGateway {
		t.Fatal("Gateway configuration required")
	}
	model, err := provider.NewConversationModel(config)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &liveConversationRecorder{model: model, t: t}
	path := filepath.Join(t.TempDir(), "long-conversation.db")
	var db *sql.DB
	var service *agentapplication.ConversationService
	var repo *agentstore.ConversationStore
	open := func() {
		var err error
		db, err = sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if err = agentinfra.EnsureSchema(t.Context(), db, "sqlite", ""); err != nil {
			t.Fatal(err)
		}
		renderer, err := agentinfra.Renderer("sqlite", "")
		if err != nil {
			t.Fatal(err)
		}
		store, err := agentinfra.NewAgentStore(db, renderer, "sqlite")
		if err != nil {
			t.Fatal(err)
		}
		repo = agentstore.NewConversationStore(store)
		service, err = conversationassembly.NewService(repo, recorder, conversationAuthority().RuntimeID, agentapplication.ConversationOptions{})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { service.Close(); db.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "long-live", Title: "Long conversation acceptance"}, conversationAuthority())
	if err != nil {
		t.Fatal(err)
	}
	send := func(index int, message string) string {
		r, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: fmt.Sprintf("live-%d", index), Message: message}, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(150 * time.Second)
		for time.Now().Before(deadline) {
			run, err := service.Run(t.Context(), c.ID, r.ID, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			if run.Terminal() {
				if run.Status != "completed" {
					t.Fatalf("round %d failed: %s", index, run.ErrorCode)
				}
				page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{AfterSeq: run.UserSeq, Limit: 1}, conversationAuthority())
				if err != nil || len(page.Items) != 1 {
					t.Fatalf("round %d missing reply", index)
				}
				return page.Items[0].Content
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("round %d timed out", index)
		return ""
	}
	for i := 0; i < 16; i++ {
		note := fmt.Sprintf("第%d轮归档校验。", i+1)
		if i == 0 {
			note = "项目代号 CEDAR-928，截止时间周五。已决定先做网页版本。尚未完成的待办：核对发票。"
		}
		if i == 1 {
			note = "纠正截止时间：改为周四，周五是旧信息。核对发票仍然没完成；先做网页版本的决定保持不变。"
		}
		filler := strings.Repeat(fmt.Sprintf("trace_%02d_abcdefghijklmnopqrstuvwxyz_0123456789;", i), 170)
		send(i, note+"以下是无须记忆的归档校验样本，不是项目事实：\n"+filler+"\n只回复收到。")
		snapshot, err := repo.Summary(t.Context(), c.ID, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.ThroughSeq >= 4 {
			raw, _ := json.Marshal(snapshot.Content)
			if !strings.Contains(string(raw), "CEDAR-928") || !strings.Contains(string(raw), "四") || !strings.Contains(string(raw), "发票") || !(strings.Contains(string(raw), "网页") || strings.Contains(strings.ToLower(string(raw)), "web")) {
				t.Fatalf("summary lost synthetic acceptance facts at round %d: %s", i, raw)
			}
		}
	}
	summary, err := repo.Summary(t.Context(), c.ID, conversationAuthority())
	if err != nil || summary.ID == "" || summary.ThroughSeq < 4 || summary.ThroughSeq%2 != 0 {
		t.Fatal("early facts and correction were not compacted on a complete-turn boundary")
	}
	if summary.PreviousID == "" {
		t.Fatal("long history did not exercise iterative summary replacement")
	}
	before, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{Limit: 100}, conversationAuthority())
	if err != nil || len(before.Items) != 32 || !strings.Contains(before.Items[0].Content, "CEDAR-928") || !strings.Contains(before.Items[2].Content, "周四") {
		t.Fatal("compaction lost original history")
	}
	service.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	restored, err := repo.Summary(t.Context(), c.ID, conversationAuthority())
	if err != nil || restored.ID != summary.ID {
		t.Fatal("summary did not survive database reopen")
	}
	answer := send(16, "重启后的验收：只返回一个 JSON 对象，不要 Markdown。project_code 填项目代号；deadline 填最新截止星期；pending 填仍未完成的待办；decision 填已确认的产品形态决定。只能依据前文，缺失就填不知道。")
	var fields map[string]string
	if json.Unmarshal([]byte(answer), &fields) != nil {
		t.Fatal("post-restart model reply is not the requested JSON")
	}
	if fields["project_code"] != "CEDAR-928" || !strings.Contains(fields["deadline"], "四") || strings.Contains(fields["deadline"], "五") || !strings.Contains(fields["pending"], "发票") || !(strings.Contains(fields["decision"], "网页") || strings.Contains(strings.ToLower(fields["decision"]), "web")) {
		t.Fatalf("post-compaction reply lost synthetic acceptance facts: %+v; summary=%+v", fields, restored.Content)
	}
	recorder.mu.Lock()
	summaries, replies, maxBytes, last := recorder.summaries, recorder.replies, recorder.maxBytes, recorder.last
	recorder.mu.Unlock()
	if summaries < 2 || maxBytes > 65536 {
		t.Fatal("compaction did not enforce the default context budget")
	}
	injected := false
	for _, message := range last.Messages {
		if strings.Contains(message.Content, "Earlier conversation summary (data):") {
			injected = true
		}
		if message.Role != "system" && (strings.Contains(message.Content, "trace_00_") || strings.Contains(message.Content, "trace_01_")) {
			t.Fatal("compacted originals duplicated into final context")
		}
	}
	if !injected {
		t.Fatal("final model request did not use persisted summary")
	}
	latest, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{Limit: 30}, conversationAuthority())
	if err != nil || len(latest.Items) != 30 || latest.NextBeforeSeq == 0 {
		t.Fatal("latest page missing cursor")
	}
	early, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{BeforeSeq: latest.NextBeforeSeq, Limit: 30}, conversationAuthority())
	if err != nil || len(early.Items) != 4 || early.Items[0].Seq != 1 || early.Items[3].Seq != 4 {
		t.Fatal("pagination lost compacted originals")
	}
	t.Logf("Gateway long-conversation acceptance passed: replies=%d summaries=%d max_context_bytes=%d default_budget=65536 through_seq=%d original_messages=34; facts, correction, pending work, decision, iterative compaction, pagination and SQLite reopen verified", replies, summaries, maxBytes, restored.ThroughSeq)
}
