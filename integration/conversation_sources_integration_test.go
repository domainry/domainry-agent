package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/application"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

type sourceModel struct {
	step    func(agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error)
	summary func(agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error)
}

func (*sourceModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return (&executionModel{}).ConversationModelIdentity()
}
func (m *sourceModel) GenerateConversation(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	if m.summary == nil {
		return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected summary")
	}
	return m.summary(in)
}
func (m *sourceModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	out, err := m.step(in)
	if err == nil && out.Message.Content != "" {
		err = emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: out.Message.Content})
	}
	return out, err
}
func sourceAnswer(text string) agentsdk.ConversationStepResult {
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: text}, FinishReason: "stop"}
}

func sourceKnowledgeFixture(t *testing.T, visible *atomic.Bool) *provider.Knowledge {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !visible.Load() {
			http.Error(w, "not permitted", 403)
			return
		}
		fmt.Fprint(w, `{"hits":[{"doc_id":"policy","content":"PRIVATE-VALUE-97","title":"费用规则"}]}`)
	}))
	t.Cleanup(upstream.Close)
	k, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "source-fixture", TeamID: "team", KBID: "kb", WorkspaceID: conversationAuthority().WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Reproduce records made before source metadata and history result run IDs
// existed, while keeping the real owner-scoped SQLite messages and ledger.
type legacySourceRepository struct {
	*agentstore.ConversationStore
	legacy atomic.Bool
}

func (r *legacySourceRepository) ConversationSourceSnapshot(ctx context.Context, ref agentsdk.ConversationRunReference, a agentsdk.ConversationAuthority) (persistence.ConversationSourceSnapshot, error) {
	snapshot, err := r.ConversationStore.ConversationSourceSnapshot(ctx, ref, a)
	if err != nil || !r.legacy.Load() {
		return snapshot, err
	}
	if snapshot.Input != nil {
		snapshot.Input.Sources = nil
	}
	for _, call := range snapshot.Calls {
		if call.Result == nil || call.Call.Name != "history_search" && call.Call.Name != "history_read" {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal(call.Result.Content, &data); err != nil {
			return snapshot, err
		}
		delete(data, "run_id")
		if items, ok := data["items"].([]any); ok {
			for _, item := range items {
				delete(item.(map[string]any), "run_id")
			}
		}
		call.Result.Content, err = json.Marshal(data)
		if err != nil {
			return snapshot, err
		}
	}
	return snapshot, nil
}

func TestKnowledgeProvenanceProtectsDerivedRepliesSSEAndHistoryAcrossRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%v", legacy), func(t *testing.T) {
			repo := &legacySourceRepository{ConversationStore: conversationRepository(t)}
			a := conversationAuthority()
			var visible atomic.Bool
			visible.Store(true)
			knowledge := sourceKnowledgeFixture(t, &visible)
			policy := personalReadAuthorizer{}
			personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			var sourceMessage agentsdk.ConversationMessage
			model := &sourceModel{step: func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				last := in.Messages[len(in.Messages)-1]
				switch {
				case last.Role == "user" && last.Content == "读取文档":
					return resultToolCall("knowledge_search", "lookup", map[string]string{"query": "费用规则"}), nil
				case last.Role == "user" && last.Content == "引用旧回复":
					return resultToolCall("history_read", "history", map[string]string{"conversation_id": sourceMessage.ConversationID, "message_id": sourceMessage.ID}), nil
				case last.Role == "user" && last.Content == "搜索旧回复":
					return resultToolCall("history_search", "history-search", map[string]string{"query": "PRIVATE-VALUE-97"}), nil
				case last.Role == "tool" && last.ToolCallID == "history-search":
					if !visible.Load() {
						if strings.Contains(last.Content, "PRIVATE-VALUE-97") || !strings.Contains(last.Content, `"omitted":true`) {
							t.Error("history search did not filter revoked source excerpts")
						}
						return sourceAnswer("部分历史资料当前不可访问。"), nil
					}
					if !strings.Contains(last.Content, "PRIVATE-VALUE-97") {
						t.Error("authorized search evidence missing")
					}
					return sourceAnswer("搜索所得的结论为 PRIVATE-VALUE-97。"), nil
				case last.Role == "tool" && last.ToolCallID == "history":
					if !strings.Contains(last.Content, "PRIVATE-VALUE-97") {
						t.Error("authorized history evidence missing")
					}
					return sourceAnswer("旧回复的结论仍为 PRIVATE-VALUE-97。"), nil
				case last.Role == "tool":
					return sourceAnswer("文档结论是 PRIVATE-VALUE-97。"), nil
				default:
					raw := string(mustJSON(in.Messages))
					if !visible.Load() {
						if strings.Contains(raw, "PRIVATE-VALUE-97") {
							t.Error("revoked reply entered a new model input")
						}
						return sourceAnswer("资料当前无法验证，需要重新查询。"), nil
					}
					if !strings.Contains(raw, "PRIVATE-VALUE-97") {
						t.Error("authorized prior reply missing")
					}
					return sourceAnswer("换个说法：PRIVATE-VALUE-97。"), nil
				}
			}}
			options := application.ConversationOptions{Knowledge: knowledge, ToolHost: personal, PersonalAuthorizer: policy}
			service, err := application.NewConversationService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Close() })
			conversation, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "sources"}, a)
			send := func(id, message string) agentsdk.ConversationRun {
				run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: id, Message: message}, a)
				if err != nil {
					t.Fatal(err)
				}
				done := waitConversation(t, service, conversation.ID, run.ID)
				if done.Status != "completed" {
					t.Fatalf("run did not finish: %+v", done)
				}
				return done
			}
			first := send("read", "读取文档")
			second := send("derive", "换个说法")
			if !strings.Contains(string(mustJSON(second.Steps)), "PRIVATE-VALUE-97") {
				t.Fatal("derived reply missing")
			}
			page, _ := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, a)
			sourceMessage = page.Items[1]
			other, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "other-sources"}, a)
			read, err := service.Send(t.Context(), other.ID, agentsdk.ConversationSend{ClientMessageID: "history", Message: "引用旧回复"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if done := waitConversation(t, service, other.ID, read.ID); done.Status != "completed" {
				t.Fatalf("cross-conversation history failed: %+v", done)
			}
			searchConversation, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "search-sources"}, a)
			search, err := service.Send(t.Context(), searchConversation.ID, agentsdk.ConversationSend{ClientMessageID: "search", Message: "搜索旧回复"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if done := waitConversation(t, service, searchConversation.ID, search.ID); done.Status != "completed" {
				t.Fatalf("cross-conversation search failed: %+v", done)
			}
			service.Close()
			repo.legacy.Store(legacy)
			visible.Store(false)
			service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			for _, ref := range []agentsdk.ConversationRunReference{{ConversationID: conversation.ID, RunID: first.ID}, {ConversationID: conversation.ID, RunID: second.ID}, {ConversationID: other.ID, RunID: read.ID}, {ConversationID: searchConversation.ID, RunID: search.ID}} {
				view, err := service.Run(t.Context(), ref.ConversationID, ref.RunID, a)
				if err != nil || view.AccessError == "" || view.DraftText != "" || len(view.Steps) != 0 || view.Interaction != nil {
					t.Fatal("revoked run projection exposed source data", err)
				}
				if events, err := service.Events(t.Context(), ref.ConversationID, ref.RunID, 0, 100, a); err == nil || len(events.Items) != 0 {
					t.Fatal("revoked SSE replay exposed source data")
				}
			}
			duplicate, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "读取文档"}, a)
			if err != nil || duplicate.ID != first.ID || duplicate.AccessError == "" || duplicate.DraftText != "" {
				t.Fatal("idempotent send bypassed source filtering", err)
			}
			page, err = service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || strings.Contains(string(mustJSON(page)), "PRIVATE-VALUE-97") || page.Items[0].Content != "读取文档" || page.Items[1].AccessError == "" || page.Items[3].AccessError == "" {
				t.Fatal("history response leaked derived content or lost user input", err)
			}
			// New writes carry the current schema even when old records did not.
			repo.legacy.Store(false)
			send("after-revoke", "继续整理")
			send("search-after-revoke", "搜索旧回复")
			// Read APIs still work with no model configured; source policy remains live.
			service.Close()
			service, err = application.NewConversationService(repo, nil, a.RuntimeID, application.ConversationOptions{Knowledge: knowledge, PersonalAuthorizer: policy})
			if err != nil {
				t.Fatal(err)
			}
			visible.Store(true)
			view, err := service.Run(t.Context(), conversation.ID, second.ID, a)
			if err != nil || view.AccessError != "" || !strings.Contains(string(mustJSON(view.Steps)), "PRIVATE-VALUE-97") {
				t.Fatal("restored permission did not restore the original view", err)
			}
			// Original evidence is never deleted by a read projection.
			stored, err := repo.Run(t.Context(), conversation.ID, first.ID, a)
			if err != nil || !strings.Contains(string(mustJSON(stored.Steps)), "PRIVATE-VALUE-97") {
				t.Fatal("source filtering mutated the original ledger", err)
			}
		})
	}
}

func TestSummarySourceChangesRebuildFromOriginalMessagesAndRetainUserConstraints(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	var visible atomic.Bool
	visible.Store(true)
	knowledge := sourceKnowledgeFixture(t, &visible)
	policy := personalReadAuthorizer{}
	personal, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var summaries atomic.Int32
	model := &sourceModel{}
	model.summary = func(in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		summaries.Add(1)
		raw := string(mustJSON(in.Messages))
		if !visible.Load() && strings.Contains(raw, "PRIVATE-VALUE-97") {
			t.Error("revoked source was sent to the summary model")
		}
		content := agentsdk.ConversationSummaryContent{Goal: "整理工作资料"}
		if strings.Contains(raw, "PRIVATE-VALUE-97") {
			content.Facts = []string{"PRIVATE-VALUE-97"}
		}
		if strings.Contains(raw, "周报保留风险") {
			content.Constraints = []string{"周报保留风险"}
		}
		return agentsdk.ConversationModelResult{Content: string(mustJSON(content)), Model: "source-summary"}, nil
	}
	model.step = func(in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		if last.Role == "user" && last.Content == "读取文档" {
			return resultToolCall("knowledge_search", "lookup", map[string]string{"query": "费用规则"}), nil
		}
		if last.Role == "tool" {
			return sourceAnswer("文档结论 PRIVATE-VALUE-97。"), nil
		}
		if !visible.Load() && strings.Contains(string(mustJSON(in.Messages)), "PRIVATE-VALUE-97") {
			t.Error("revoked summary entered reply context")
		}
		return sourceAnswer("已记录本轮讨论。"), nil
	}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{Knowledge: knowledge, ToolHost: personal, PersonalAuthorizer: policy})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "source-summary"}, a)
	send := func(id, text string) agentsdk.ConversationRun {
		run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: id, Message: text}, a)
		if err != nil {
			t.Fatal(err)
		}
		done := waitConversation(t, service, c.ID, run.ID)
		if done.Status != "completed" {
			t.Fatalf("summary run failed: %+v", done)
		}
		return done
	}
	first := send("document", "读取文档")
	send("constraint", "周报保留风险")
	for i := 0; i < 10; i++ {
		send(fmt.Sprintf("filler-%d", i), strings.Repeat("公开背景 ", 600))
	}
	before, err := repo.Summary(t.Context(), c.ID, a)
	if err != nil || summaries.Load() == 0 || before.Sources == nil || len(before.Sources.Runs) != 1 || before.Sources.Runs[0].RunID != first.ID || !strings.Contains(string(mustJSON(before.Content)), "PRIVATE-VALUE-97") {
		t.Fatal("summary lost actual source provenance", err)
	}
	visible.Store(false)
	send("restricted", strings.Repeat("继续公开讨论 ", 500))
	after, err := repo.Summary(t.Context(), c.ID, a)
	if err != nil || after.ID == before.ID || strings.Contains(string(mustJSON(after.Content)), "PRIVATE-VALUE-97") || !strings.Contains(string(mustJSON(after.Content)), "周报保留风险") || after.Sources == nil || len(after.Sources.Runs) != 0 || len(after.Sources.Omitted) == 0 {
		t.Fatal("summary was not rebuilt from authorized history and original user corrections", err)
	}
	visible.Store(true)
	last := send("restored", strings.Repeat("恢复后继续讨论 ", 450))
	restored, _ := repo.Summary(t.Context(), c.ID, a)
	if !strings.Contains(string(mustJSON(restored.Content)), "PRIVATE-VALUE-97") || !strings.Contains(string(mustJSON(restored.Content)), "周报保留风险") {
		t.Fatal("restored source was not recovered from original messages")
	}
	snapshot, err := repo.ConversationSourceSnapshot(t.Context(), agentsdk.ConversationRunReference{ConversationID: c.ID, RunID: last.ID}, a)
	if err != nil || snapshot.Input == nil || snapshot.Input.Sources == nil || len(snapshot.Input.Sources.Runs) != 1 || snapshot.Input.Sources.Runs[0].RunID != first.ID {
		t.Fatal("follow-ups grew a chain instead of retaining the original source root", err)
	}
}
