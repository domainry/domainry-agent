package integration_test

import (
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

func TestKnowledgeToolsSearchReadFreezeAndRevalidateAcrossRestart(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	var reads, searches atomic.Int32
	var revoked atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args map[string]any
		_ = json.NewDecoder(r.Body).Decode(&args)
		if args["team_id"] != "team" || args["kb_id"] != "kb" || args["permission_ids"] != nil {
			t.Error("scope was not server controlled")
		}
		if revoked.Load() {
			http.Error(w, "PRIVATE-DENIED", 403)
			return
		}
		if r.URL.Path == "/v1/kb/search" {
			searches.Add(1)
			if args["query"] != "付款条件" {
				t.Error("query included unrelated conversation data")
			}
			fmt.Fprint(w, `{"hits":[{"doc_id":"contract","title":"测试合同","excerpt":"付款条件见正文"}]}`)
		} else {
			reads.Add(1)
			if args["doc_id"] != "contract" {
				t.Error("invented document ID")
			}
			fmt.Fprint(w, `{"doc_id":"contract","content":"收到发票后30日付款","url":"https://example.com/contract"}`)
		}
	}))
	defer upstream.Close()
	knowledge, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-only-key", TeamID: "team", KBID: "kb", WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	policy := personalReadAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var frozen agentsdk.ConversationStepRequest
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		last := in.Messages[len(in.Messages)-1]
		switch n {
		case 1:
			if searches.Load() != 0 || reads.Load() != 0 {
				t.Error("fixed retrieval ran before the model requested it")
			}
			found := map[string]bool{}
			for _, d := range in.Tools {
				found[d.Key] = true
			}
			if !found["knowledge_search"] || !found["knowledge_read"] || !found["calculate"] {
				t.Error("knowledge composition lost existing tools")
			}
			// Unknown authority/scope input must be rejected by the tool schema.
			return resultToolCall("knowledge_search", "invalid-scope", map[string]any{"query": "付款条件", "permission_ids": []string{"admin"}}), nil
		case 2:
			if !strings.Contains(last.Content, "arguments_invalid") || searches.Load() != 0 {
				t.Error("model-supplied permission IDs reached knowledge service")
			}
			return resultToolCall("knowledge_search", "search", map[string]string{"query": "付款条件"}), nil
		case 3:
			if !strings.Contains(last.Content, "测试合同") || !strings.Contains(last.Content, "contract") {
				t.Error("actual search evidence missing")
			}
			return resultToolCall("knowledge_read", "read", map[string]string{"doc_id": "contract"}), nil
		case 4:
			if !strings.Contains(last.Content, "收到发票后30日付款") || !strings.Contains(last.Content, "https://example.com/contract") {
				t.Error("fetch content or source missing")
			}
			frozen = in
			return agentsdk.ConversationStepResult{}, fmt.Errorf("model disconnected")
		case 5:
			if string(mustJSON(in)) != string(mustJSON(frozen)) {
				t.Error("resume replaced frozen evidence")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "按合同，收到发票后30日付款（https://example.com/contract）。"}, FinishReason: "stop"}, nil
		default:
			t.Error("revoked source reached model")
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected model request")
		}
	}}
	options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Knowledge: knowledge}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "knowledge-tool"}, a)
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "question", Message: "查合同付款条件，必要时读正文"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "failed" || done.ErrorCode != "provider_failed" {
		t.Fatalf("expected frozen step to be recoverable: %+v", done)
	}
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.ErrorCode != "knowledge_access_denied" {
		t.Fatalf("revoked source replayed: %+v", done)
	}
	revoked.Store(false)
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" || len(done.Steps) != 4 || len(done.Steps[2].Calls) != 1 || done.Steps[2].Calls[0].ResultReference == nil {
		t.Fatalf("restored source did not resume the actual ledger: %+v", done)
	}
}

func mustJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }

func TestKnowledgeBusinessFailureIsRecordedWithoutSources(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/kb/fetch" {
			fmt.Fprint(w, `{"err_code":1004,"err_msg":"PRIVATE-UPSTREAM-ERROR","data":{"content":"PRIVATE-FAILED-EVIDENCE"}}`)
			return
		}
		fmt.Fprint(w, `{"err_code":0,"data":{"hits":[]}}`)
	}))
	defer upstream.Close()
	knowledge, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture-only-key", TeamID: "team", KBID: "kb", WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	policy := personalReadAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if strings.Contains(string(mustJSON(in)), "PRIVATE-") {
			t.Error("upstream failure content reached model input")
		}
		switch n {
		case 1:
			return resultToolCall("knowledge_read", "missing", map[string]string{"doc_id": "missing-document"}), nil
		case 2:
			last := in.Messages[len(in.Messages)-1]
			if !last.IsError || !strings.Contains(last.Content, "knowledge_not_found") {
				t.Error("model did not receive a failed read result")
			}
			return resultToolCall("knowledge_search", "lookup", map[string]string{"query": "missing-document"}), nil
		case 3:
			last := in.Messages[len(in.Messages)-1]
			if last.IsError || !strings.Contains(last.Content, `"hits":[]`) {
				t.Error("valid empty search was confused with business failure")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "当前没有找到可读取的文档。"}, FinishReason: "stop"}, nil
		default:
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected model request")
		}
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Knowledge: knowledge})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "knowledge-business-error"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "读取这份文档，读不到就重新检索"}, a)
	if err != nil {
		t.Fatal(err)
	}
	done := waitConversation(t, service, c.ID, run.ID)
	if done.Status != "completed" || len(done.Steps) != 3 || len(done.Steps[0].Calls) != 1 {
		t.Fatalf("failed lookup did not allow an honest completion: %+v", done)
	}
	call := done.Steps[0].Calls[0]
	if call.Status != "failed" || call.ErrorCode != "knowledge_not_found" || len(call.Citations) != 0 {
		t.Fatalf("business failure was persisted as successful evidence: %+v", call)
	}
	events, err := service.Events(t.Context(), c.ID, run.ID, 0, 100, a)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON([]any{done, events, messages})), "PRIVATE-") {
		t.Fatal("upstream failure leaked through public history or events")
	}
}
