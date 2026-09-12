package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	agentremote "github.com/domainry/domainry-agent/remote"
)

type executableRuntime string

func (r executableRuntime) RuntimeID() string { return string(r) }

func executableConversationStore(t *testing.T) *agentstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
	return store
}

func TestSaaSConversationExecutableRejectsMissingIdentityBeforeRecovery(t *testing.T) {
	clearAgentEnvironment(t)
	for name, value := range map[string]string{"AGENT_SAAS_API_KEY": "agent-service-key", "AGENT_SAAS_RUNTIME_ID": "runtime", "AGENT_CONVERSATION_PROVIDER": "gateway", "AGENT_CONVERSATION_MODEL": "fixture", "AGENT_PROVIDER_API_KEY": "model-key", "AGENT_CONVERSATION_BASE_URL": "https://models.example.test"} {
		t.Setenv(name, value)
	}
	store := executableConversationStore(t)
	repo := agentstore.NewConversationStore(store)
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "queued-before-upgrade"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "previously queued"}, a)
	if err != nil {
		t.Fatal(err)
	}
	server, closeService, err := openService(store)
	defer closeService()
	if err == nil || server != nil {
		t.Fatal("missing current Identity policy started a worker")
	}
	stored, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || stored.Status != "queued" || stored.Attempt != 0 {
		t.Fatal("unconfigured service claimed persisted work", err)
	}
}

func TestSaaSConversationExecutableRevalidatesCurrentIdentity(t *testing.T) {
	for _, condition := range []string{"revoked", "credential", "forged"} {
		t.Run(condition, func(t *testing.T) {
			clearAgentEnvironment(t)
			identity := configureIdentityFixture(t)
			identity.pauseAt.Store(2) // send admission succeeds; worker resolution waits.
			var modelCalls atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				if r.Header.Get("x-api-key") != "model-key" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("model transport mixed credentials")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"model\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Current owner verified.\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer model.Close()
			for name, value := range map[string]string{"AGENT_SAAS_API_KEY": "agent-service-key", "AGENT_SAAS_RUNTIME_ID": "runtime", "AGENT_CONVERSATION_PROVIDER": "gateway", "AGENT_CONVERSATION_PROTOCOL": "chat_completions", "AGENT_CONVERSATION_MODEL": "fixture", "AGENT_PROVIDER_API_KEY": "model-key", "AGENT_CONVERSATION_BASE_URL": model.URL} {
				t.Setenv(name, value)
			}
			server, closeService, err := openService(executableConversationStore(t))
			if err != nil {
				t.Fatal(err)
			}
			defer closeService()
			httpServer := httptest.NewServer(server.Handler())
			defer httpServer.Close()
			binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: httpServer.URL, APIKey: "agent-service-key", Client: httpServer.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, executableRuntime("runtime"))
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			service := binding.(agentsdk.ConversationBinding).Conversations()
			a := agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "current-role"}
			for _, kind := range []string{"workspace", "runtime"} {
				forged := a
				if kind == "workspace" {
					forged.WorkspaceID = "another-workspace"
				} else {
					forged.RuntimeID = "another-runtime"
				}
				if _, err = service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: kind}, forged); err == nil {
					t.Fatal("forged deployment scope accepted", kind)
				}
			}
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: condition}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "continue current work"}, a)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-identity.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not resolve principal")
			}
			switch condition {
			case "revoked":
				identity.revoked.Store(true)
			case "credential":
				identity.unavailable.Store(true)
			case "forged":
				identity.forged.Store(true)
			}
			close(identity.release)
			wait := func() agentsdk.ConversationRun {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					current, err := service.Run(t.Context(), c.ID, run.ID, a)
					if err != nil {
						t.Fatal(err)
					}
					if current.Terminal() {
						return current
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("run did not finish")
				return agentsdk.ConversationRun{}
			}
			failed := wait()
			want := "execution_authorization_unavailable"
			if condition == "revoked" {
				want = "execution_access_denied"
			}
			if failed.Status != "failed" || failed.ErrorCode != want || modelCalls.Load() != 0 {
				t.Fatalf("unauthorized worker: %+v model=%d", failed, modelCalls.Load())
			}
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err == nil {
				t.Fatal("denied owner resumed")
			}
			after, err := service.Run(t.Context(), c.ID, run.ID, a)
			if err != nil || after.LastEventSeq != failed.LastEventSeq {
				t.Fatal("denied resume changed run", err)
			}
			identity.revoked.Store(false)
			identity.unavailable.Store(false)
			identity.forged.Store(false)
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			if final := wait(); final.Status != "completed" || modelCalls.Load() != 1 {
				t.Fatalf("authorized resume failed: %+v model=%d", final, modelCalls.Load())
			}
			messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(messages.Items) != 2 || strings.Contains(fmt.Sprint(messages), "service-key") {
				t.Fatal("messages or credential boundary invalid", err)
			}
		})
	}
}
