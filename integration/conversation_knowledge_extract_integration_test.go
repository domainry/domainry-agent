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

func TestKnowledgeExtractModelLoopRestartCitationsAndRevocation(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	var reads atomic.Int32
	var revoked atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if revoked.Load() {
			http.Error(w, "denied", 403)
			return
		}
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil || r.URL.Path != "/v1/kb/fetch" || in["doc_id"] != "contract.pdf" || in["team_id"] != "team" || in["kb_id"] != "kb" {
			t.Error("extraction bypassed trusted source request")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"doc_id": "contract.pdf", "title": "合成合同", "body": "付款金额：9007199254740993.25 元\n客户：青禾公司\n"}})
	}))
	defer upstream.Close()
	source, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "fixture", TeamID: "team", KBID: "kb", WorkspaceID: a.WorkspaceID, ResponseMapping: &provider.KnowledgeResponseMapping{Fetch: &provider.KnowledgeCitationMapping{Items: "/data", DocumentID: "/doc_id", Title: "/title", Excerpt: "/body"}}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"doc_id": "contract.pdf", "fields": []any{map[string]any{"key": "amount", "type": "decimal", "required": true, "pattern": `付款金额：([0-9.]+)`}, map[string]any{"key": "email", "type": "text", "required": true, "pattern": `邮箱：([^\n]+)`}}}
	var frozen agentsdk.ConversationStepRequest
	var citation string
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch n {
		case 1:
			found := false
			for _, tool := range in.Tools {
				found = found || tool.Key == "knowledge_extract"
			}
			if !found {
				t.Error("extraction absent from actual model catalog")
			}
			return resultToolCall("knowledge_extract", "invalid", map[string]any{"doc_id": "contract.pdf", "fields": []any{map[string]any{"key": "amount", "type": "decimal", "pattern": `(a)\1`}}}), nil
		case 2:
			if reads.Load() != 0 || !strings.Contains(in.Messages[len(in.Messages)-1].Content, "extraction_plan_invalid") {
				t.Error("invalid extraction contacted provider")
			}
			return resultToolCall("knowledge_extract", "extract", args), nil
		case 3, 4:
			var result agentsdk.ConversationToolResult
			_ = json.Unmarshal([]byte(in.Messages[len(in.Messages)-1].Content), &result)
			var extracted agentsdk.KnowledgeExtractionResult
			if e := json.Unmarshal(result.Content, &extracted); e != nil || len(extracted.Data.Fields) != 2 || len(extracted.Citations) != 1 {
				t.Error("extraction not supplied to model")
				return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid extraction result")
			}
			if extracted.Data.Fields[0].Value == nil || *extracted.Data.Fields[0].Value != "9007199254740993.25" || extracted.Data.Fields[1].Status != "missing" || extracted.Data.Coverage.OriginalComplete {
				t.Error("incorrect validated extraction")
				return agentsdk.ConversationStepResult{}, fmt.Errorf("invalid extracted fields")
			}
			citation = extracted.Citations[0].ID
			if n == 3 {
				frozen = in
				return agentsdk.ConversationStepResult{}, fmt.Errorf("disconnect after extraction")
			}
			if string(mustJSON(frozen)) != string(mustJSON(in)) {
				t.Error("restart rewrote extraction model input")
			}
			return agentsdk.ConversationStepResult{FinishReason: "stop", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "付款金额为 9007199254740993.25，邮箱未在可用内容中找到。[[cite:" + citation + "]]"}}, nil
		default:
			t.Error("revoked result reached model")
			return agentsdk.ConversationStepResult{}, fmt.Errorf("unexpected step")
		}
	}}
	options := application.ConversationOptions{Knowledge: source, ToolHost: tools, PersonalAuthorizer: personalReadAuthorizer{}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "extract"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "fields", Message: "按合同提取付款金额和邮箱，保留缺失字段与来源。"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, conversation.ID, run.ID); done.Status != "failed" || done.ErrorCode != "provider_failed" {
		t.Fatalf("expected frozen extraction: %+v", done)
	}
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Resume(t.Context(), conversation.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, conversation.ID, run.ID); done.Status != "completed" {
		t.Fatalf("resume extraction: %+v", done)
	}
	check := func(hidden bool) {
		t.Helper()
		page, err := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Items {
			if message.Role == "assistant" {
				if hidden {
					if strings.Contains(message.Content, "9007199254740993.25") || len(message.Citations) != 0 {
						t.Fatal("revoked extraction remained visible")
					}
				} else if !strings.Contains(message.Content, "9007199254740993.25") || len(message.Citations) != 1 || message.Citations[0].ID != citation {
					t.Fatal("typed value/citation not persisted")
				}
				return
			}
		}
		t.Fatal("assistant message absent")
	}
	check(false)
	revoked.Store(true)
	check(true)
	revoked.Store(false)
	check(false)
}
