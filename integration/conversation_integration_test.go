package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	agentmodule "github.com/domainry/domainry-agent/module"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type conversationModelFunc func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error)

func (f conversationModelFunc) GenerateConversation(ctx context.Context, r agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return f(ctx, r)
}

type conversationStreamingModelFunc func(context.Context, agentsdk.ConversationModelRequest, func(string) error) (agentsdk.ConversationModelResult, error)

func (f conversationStreamingModelFunc) GenerateConversation(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected summary request")
}
func (f conversationStreamingModelFunc) StreamConversation(ctx context.Context, in agentsdk.ConversationModelRequest, emit func(string) error) (agentsdk.ConversationModelResult, error) {
	return f(ctx, in, emit)
}
func conversationAuthority() agentsdk.ConversationAuthority {
	return agentsdk.ConversationAuthority{Known: true, RuntimeID: "conversation-runtime", WorkspaceID: "workspace", UserID: "user"}
}
func conversationOptions() agentapplication.ConversationOptions {
	return agentapplication.ConversationOptions{ContextBytes: 4096, MaxInputBytes: 512, MaxOutputBytes: 512, SummaryBytes: 512, Workers: 1, Lease: 300 * time.Millisecond, Poll: 10 * time.Millisecond, RunTimeout: 5 * time.Second}
}
func conversationRepository(t *testing.T) *agentstore.ConversationStore {
	db := openSQLite(t, "conversation")
	if err := agentinfra.EnsureSchema(t.Context(), db, "sqlite", ""); err != nil {
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
	return agentstore.NewConversationStore(store)
}
func waitConversation(t *testing.T, s agentsdk.ConversationService, id, run string) agentsdk.ConversationRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.Run(t.Context(), id, run, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		if r.Terminal() {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("conversation did not finish")
	return agentsdk.ConversationRun{}
}

func TestConversationCompactionMemoryAndOriginalHistory(t *testing.T) {
	repo := conversationRepository(t)
	var mu sync.Mutex
	requests := []agentsdk.ConversationModelRequest{}
	model := conversationModelFunc(func(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		mu.Lock()
		requests = append(requests, in)
		mu.Unlock()
		raw, _ := json.Marshal(in.Messages)
		if len(raw) > 4096 {
			return agentsdk.ConversationModelResult{}, fmt.Errorf("unbounded context: %d", len(raw))
		}
		if in.Purpose == "summary" {
			return agentsdk.ConversationModelResult{Content: `{"goal":"做项目","constraints":["用中文"],"facts":["项目代号 ALPHA-42"],"decisions":[],"open_items":["继续处理"]}`, Model: "fake"}, nil
		}
		return agentsdk.ConversationModelResult{Content: strings.Repeat("r", 280), Model: "fake"}, nil
	})
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, conversationOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	memory, err := service.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "preference", Title: "语言", Content: "PREFERENCE_ONLY 用中文", Enabled: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "one", MemoryEnabled: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: fmt.Sprint("m", i), Message: fmt.Sprintf("项目代号 ALPHA-42 第%d轮 %s", i, strings.Repeat("u", 330))}, a)
		if err != nil {
			t.Fatal(err)
		}
		if result := waitConversation(t, service, c.ID, run.ID); result.Status != "completed" {
			t.Fatalf("round %d %+v", i, result)
		}
	}
	summary, err := repo.Summary(t.Context(), c.ID, a)
	if err != nil || summary.ID == "" || summary.ThroughSeq%2 != 0 {
		t.Fatalf("summary %+v %v", summary, err)
	}
	messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(messages.Items) != 28 || !strings.Contains(messages.Items[0].Content, "ALPHA-42") {
		t.Fatal("original history was lost", err)
	}
	forward, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{AfterSeq: 1, Limit: 2}, a)
	if err != nil || len(forward.Items) != 2 || forward.Items[0].Seq != 2 || forward.NextAfterSeq != 3 {
		t.Fatalf("forward page skipped messages: %+v %v", forward, err)
	}
	next, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{AfterSeq: forward.NextAfterSeq, Limit: 2}, a)
	if err != nil || len(next.Items) != 2 || next.Items[0].Seq != 4 {
		t.Fatalf("forward continuation %+v %v", next, err)
	}
	backward, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{Limit: 2}, a)
	if err != nil || len(backward.Items) != 2 || backward.Items[0].Seq != 27 || backward.NextBeforeSeq != 27 {
		t.Fatalf("backward page %+v %v", backward, err)
	}
	mu.Lock()
	last := requests[len(requests)-1]
	first := requests[0]
	mu.Unlock()
	if len(first.Messages) != 3 || !strings.Contains(first.Messages[1].Content, "PREFERENCE_ONLY") || !strings.Contains(last.Messages[2].Content, "ALPHA-42") {
		t.Fatalf("context missing memory or summary: first=%+v last=%+v", first, last)
	}
	// User memory is explicit and independent of per-conversation summaries.
	if err = service.DeleteMemory(t.Context(), memory.ID, memory.Revision, a); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "fresh", MemoryEnabled: false}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), fresh.ID, agentsdk.ConversationSend{ClientMessageID: "fresh", Message: "new chat"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waitConversation(t, service, fresh.ID, run.ID)
	mu.Lock()
	last = requests[len(requests)-1]
	mu.Unlock()
	if len(last.Messages) != 2 || strings.Contains(last.Messages[0].Content, "PREFERENCE_ONLY") {
		t.Fatal("history leaked between conversations")
	}
}

func TestConversationInvalidSummaryIsRecoverableWithoutLosingMessages(t *testing.T) {
	repo := conversationRepository(t)
	var mu sync.Mutex
	bad := true
	model := conversationModelFunc(func(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		if in.Purpose == "summary" {
			mu.Lock()
			fail := bad
			mu.Unlock()
			if fail {
				return agentsdk.ConversationModelResult{Content: "invalid JSON"}, nil
			}
			return agentsdk.ConversationModelResult{Content: `{"goal":"continue","facts":["retained"]}`}, nil
		}
		return agentsdk.ConversationModelResult{Content: strings.Repeat("r", 280)}, nil
	})
	s, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, conversationOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := conversationAuthority()
	c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "bad-summary"}, a)
	if err != nil {
		t.Fatal(err)
	}
	var failed agentsdk.ConversationRun
	for i := 0; i < 12; i++ {
		r, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: fmt.Sprint("m", i), Message: strings.Repeat("u", 380)}, a)
		if err != nil {
			t.Fatal(err)
		}
		r = waitConversation(t, s, c.ID, r.ID)
		if r.Status == "failed" {
			failed = r
			break
		}
	}
	if failed.ID == "" || failed.ErrorCode != "context_failed" {
		t.Fatalf("expected compaction failure %+v", failed)
	}
	before, err := s.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || int64(len(before.Items)) != failed.UserSeq {
		t.Fatal("original messages missing")
	}
	mu.Lock()
	bad = false
	mu.Unlock()
	if _, err = s.Resume(t.Context(), c.ID, failed.ID, a); err != nil {
		t.Fatal(err)
	}
	if r := waitConversation(t, s, c.ID, failed.ID); r.Status != "completed" {
		t.Fatalf("resume %+v", r)
	}
	after, err := s.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(after.Items) != len(before.Items)+1 {
		t.Fatal("resume duplicated user message")
	}
}

func TestConversationWorkerRestartReusesFrozenInput(t *testing.T) {
	repo := conversationRepository(t)
	entered := make(chan agentsdk.ConversationModelRequest, 1)
	blocking := conversationStreamingModelFunc(func(ctx context.Context, r agentsdk.ConversationModelRequest, emit func(string) error) (agentsdk.ConversationModelResult, error) {
		if err := emit("unfinished draft"); err != nil {
			return agentsdk.ConversationModelResult{}, err
		}
		entered <- r
		<-ctx.Done()
		return agentsdk.ConversationModelResult{}, ctx.Err()
	})
	s, err := conversationassembly.NewService(repo, blocking, conversationAuthority().RuntimeID, conversationOptions())
	if err != nil {
		t.Fatal(err)
	}
	a := conversationAuthority()
	c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "restart", MemoryEnabled: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "m", Message: "remember"}, a)
	if err != nil {
		t.Fatal(err)
	}
	var before agentsdk.ConversationModelRequest
	select {
	case before = <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	s.Close()
	snapshot, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || snapshot.DraftText != "unfinished draft" {
		t.Fatal("shutdown lost committed draft", err)
	}
	if _, err = repo.WriteMemory(t.Context(), agentsdk.ConversationMemoryWrite{ID: "changed", Title: "changed", Content: "new preference", Enabled: true}, a); err != nil {
		t.Fatal(err)
	}
	recovered := make(chan agentsdk.ConversationModelRequest, 1)
	model := conversationModelFunc(func(_ context.Context, r agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		recovered <- r
		return agentsdk.ConversationModelResult{Content: "done"}, nil
	})
	second, err := conversationassembly.NewService(repo, model, a.RuntimeID, conversationOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	done := waitConversation(t, second, c.ID, run.ID)
	after := <-recovered
	if done.Status != "completed" || done.Attempt != 2 || done.DraftText != "" || !reflect.DeepEqual(before, after) {
		t.Fatalf("restart input changed: before=%+v after=%+v run=%+v", before, after, done)
	}
}

func TestConversationModuleSaaSAndBrowserSurface(t *testing.T) {
	model := conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		return agentsdk.ConversationModelResult{Content: "reply"}, nil
	})
	for _, mode := range []string{"module", "saas"} {
		t.Run(mode, func(t *testing.T) {
			a := conversationAuthority()
			var binding agentsdk.Binding
			var err error
			if mode == "module" {
				binding, err = agentmodule.NewFactory(agentmodule.Options{ConversationProvider: model}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, newSQLiteModuleHost(t, a.RuntimeID))
			} else {
				svc, e := conversationassembly.NewService(conversationRepository(t), model, a.RuntimeID, conversationOptions())
				if e != nil {
					t.Fatal(e)
				}
				defer svc.Close()
				server, e := agentserver.New(agentserver.Config{APIKey: "secret", Conversations: svc, ConversationRuntimeID: a.RuntimeID})
				if e != nil {
					t.Fatal(e)
				}
				httpServer := httptest.NewServer(server.Handler())
				defer httpServer.Close()
				binding, err = agentremote.NewFactory(agentremote.Options{BaseURL: httpServer.URL, APIKey: "secret", Client: httpServer.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
				forged := httptest.NewRequest("POST", "/agent/v1/conversations/list", strings.NewReader(`{"authority":{"known":true,"runtime_id":"another","workspace_id":"workspace","user_id":"user"}}`))
				forged.Header.Set("Authorization", "Bearer secret")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, forged)
				if response.Code != 403 {
					t.Fatalf("forged runtime status %d", response.Code)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			svc := binding.(agentsdk.ConversationBinding).Conversations()
			// Host composition must retain the new adapter alongside business adapters.
			bindApplicationHost(t, binding, newTaskHost(binding.(agentpersistence.ExecutionStateBinding).AgentTaskState()))
			var adapter modulehttp.Adapter
			for _, candidate := range binding.(modulehttp.Provider).HTTPAdapters() {
				if candidate.Name() == "conversations" {
					adapter = candidate
				}
			}
			if adapter == nil {
				t.Fatal("conversation adapter disappeared after host binding")
			}
			call := func(method, path, body, user string) *httptest.ResponseRecorder {
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				if user != "" {
					request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: a.WorkspaceID, UserID: user}}))
				}
				response := httptest.NewRecorder()
				adapter.Handler().ServeHTTP(response, request)
				return response
			}
			if r := call("GET", "/agent/conversations", "", ""); r.Code != 403 {
				t.Fatal("anonymous access allowed")
			}
			if r := call("POST", "/agent/conversations", `{"client_id":"forged","user_id":"another"}`, a.UserID); r.Code != 400 {
				t.Fatal("accepted caller identity")
			}
			created := call("POST", "/agent/conversations", `{"client_id":"browser"}`, a.UserID)
			var c agentsdk.Conversation
			if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &c) != nil {
				t.Fatalf("create %d %s", created.Code, created.Body)
			}
			sent := call("POST", "/agent/conversations/"+c.ID+"/messages", `{"client_message_id":"m","message":"hello"}`, a.UserID)
			var run agentsdk.ConversationRun
			if sent.Code != 202 || json.Unmarshal(sent.Body.Bytes(), &run) != nil {
				t.Fatalf("send %d %s", sent.Code, sent.Body)
			}
			if done := waitConversation(t, svc, c.ID, run.ID); done.Status != "completed" {
				t.Fatalf("run %+v", done)
			}
			replay := call("POST", "/agent/conversations/"+c.ID+"/messages", `{"client_message_id":"m","message":"hello"}`, a.UserID)
			var repeated agentsdk.ConversationRun
			_ = json.Unmarshal(replay.Body.Bytes(), &repeated)
			if repeated.ID != run.ID {
				t.Fatal("HTTP replay duplicated run")
			}
			if r := call("GET", "/agent/conversations/"+c.ID, "", "another"); r.Code != 404 {
				t.Fatalf("foreign read %d %s", r.Code, r.Body)
			}
			events := call("GET", "/agent/conversations/"+c.ID+"/runs/"+run.ID+"/events/stream?after_seq=1", "", a.UserID)
			if events.Code != 200 || !strings.Contains(events.Body.String(), "event: run.completed") || strings.Contains(events.Body.String(), "id: 1\n") {
				t.Fatalf("SSE replay %d %s", events.Code, events.Body)
			}
			if r := call("GET", "/agent/conversations?limit=invalid", "", a.UserID); r.Code != 400 {
				t.Fatal("invalid pagination accepted")
			}
		})
	}
}

func TestConversationPersistsSafeProviderFailureCategories(t *testing.T) {
	for _, tc := range []struct{ code, want string }{{"agent.conversation.provider_quota_exhausted", "provider_quota_exhausted"}, {"agent.conversation.provider_rate_limited", "provider_rate_limited"}, {"agent.conversation.provider_timeout", "provider_timeout"}, {"private-untrusted-error", "provider_failed"}} {
		t.Run(tc.want, func(t *testing.T) {
			model := conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				return agentsdk.ConversationModelResult{}, &agentsdk.Error{Class: "unavailable", Code: tc.code, Message: "private provider detail"}
			})
			service, err := conversationassembly.NewService(conversationRepository(t), model, conversationAuthority().RuntimeID, conversationOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "provider-failure"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "message", Message: "hello"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			result := waitConversation(t, service, c.ID, run.ID)
			if result.Status != "failed" || result.ErrorCode != tc.want {
				t.Fatalf("unexpected persisted failure: %+v", result)
			}
		})
	}
}
