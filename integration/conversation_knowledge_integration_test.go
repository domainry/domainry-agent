package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentmodule "github.com/domainry/domainry-agent/module"
)

func TestConversationKnowledgeModuleRetrievesAndFreezesInputForResume(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var searches, replies atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				searches.Add(1)
				var request map[string]any
				if json.NewDecoder(r.Body).Decode(&request) != nil || request["query"] != "文档中的编号是什么？" || request["team_id"] != "team" || request["kb_id"] != "bcri" {
					t.Errorf("query must contain only current user text: %+v", request)
				}
				io.WriteString(w, `{"matches":[{"doc_id":"guide","snippet":"文档编号为 BCRI-42","title":"操作指南"}]}`)
			}))
			defer upstream.Close()
			inputs := make(chan agentsdk.ConversationModelRequest, 2)
			reply := func(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				inputs <- in
				if replies.Add(1) == 1 {
					return agentsdk.ConversationModelResult{}, errors.New("temporary model failure")
				}
				return agentsdk.ConversationModelResult{Content: "文档编号是 BCRI-42（操作指南，guide）。"}, nil
			}
			var model agentsdk.ConversationModel = conversationModelFunc(reply)
			if stream {
				model = conversationStreamingModelFunc(func(ctx context.Context, in agentsdk.ConversationModelRequest, emit func(string) error) (agentsdk.ConversationModelResult, error) {
					result, err := reply(ctx, in)
					if err == nil {
						err = emit(result.Content)
					}
					return result, err
				})
			}
			a := conversationAuthority()
			options := conversationOptions()
			options.KnowledgeBytes = 512
			binding, err := agentmodule.NewFactory(agentmodule.Options{
				ConversationProvider: model, ConversationOptions: options,
				Knowledge: agentmodule.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "key", TeamID: "team", KBID: "bcri", WorkspaceID: a.WorkspaceID},
			}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, newSQLiteModuleHost(t, a.RuntimeID))
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			s := binding.(agentsdk.ConversationBinding).Conversations()
			if _, err = s.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "private", Title: "偏好", Content: "PRIVATE_MEMORY_NOT_A_QUERY", Enabled: true}, a); err != nil {
				t.Fatal(err)
			}
			c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "knowledge", MemoryEnabled: true}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "query", Message: "文档中的编号是什么？"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if done := waitConversation(t, s, c.ID, run.ID); done.ErrorCode != "provider_failed" {
				t.Fatalf("expected recoverable model failure: %+v", done)
			}
			if _, err = s.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			if done := waitConversation(t, s, c.ID, run.ID); done.Status != "completed" {
				t.Fatalf("resume: %+v", done)
			}
			first, second := <-inputs, <-inputs
			if searches.Load() < 2 || !reflect.DeepEqual(first, second) {
				t.Fatal("resume skipped current-source verification or changed frozen input")
			}
			if len(first.Messages) != 4 || !strings.Contains(first.Messages[1].Content, "BCRI-42") || !strings.Contains(first.Messages[1].Content, "guide") || !strings.Contains(first.Messages[2].Content, "PRIVATE_MEMORY_NOT_A_QUERY") {
				t.Fatalf("knowledge, source or memory missing: %+v", first.Messages)
			}
			if strings.Contains(first.Messages[0].Content, "You have no tools, external access") || first.Messages[len(first.Messages)-1].Content != "文档中的编号是什么？" {
				t.Fatal("incorrect system capability or lost user input")
			}
			page, err := s.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(page.Items) != 2 || !strings.Contains(page.Items[1].Content, "BCRI-42") {
				t.Fatalf("reply persistence: %+v %v", page, err)
			}
		})
	}
}

func TestConversationKnowledgeEmptyAndFailuresRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, body, workspace, code string
		status                      int
	}{
		{"empty", `{"results":[]}`, "workspace", "", 200},
		{"access", `private upstream error`, "workspace", "knowledge_access_denied", 403},
		{"scope", `[]`, "other", "knowledge_access_denied", 200},
		{"malformed", `not JSON`, "workspace", "knowledge_response_invalid", 200},
		{"budget", `{"text":"` + strings.Repeat("x", 600) + `"}`, "workspace", "knowledge_context_exceeded", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var searches, generations atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				searches.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			knowledge, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "key", TeamID: "team", KBID: "kb", WorkspaceID: tc.workspace})
			if err != nil {
				t.Fatal(err)
			}
			options := conversationOptions()
			options.Knowledge, options.KnowledgeBytes = knowledge, 512
			model := conversationModelFunc(func(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				generations.Add(1)
				if !strings.Contains(in.Messages[1].Content, `"results":[]`) {
					t.Error("empty result not represented")
				}
				return agentsdk.ConversationModelResult{Content: "没有检索到相关文档。"}, nil
			})
			s, err := agentapplication.NewConversationService(conversationRepository(t), model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "knowledge"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			run, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "query", Message: "query"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			done := waitConversation(t, s, c.ID, run.ID)
			if done.ErrorCode != tc.code || (tc.code == "" && done.Status != "completed") || (tc.code != "" && (done.Status != "failed" || generations.Load() != 0)) {
				t.Fatalf("retrieval failure must not become an ungrounded reply: %+v", done)
			}
			if tc.name == "scope" && searches.Load() != 0 {
				t.Fatal("wrong workspace reached upstream")
			}
		})
	}
}
