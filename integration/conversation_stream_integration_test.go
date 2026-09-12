package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	agentmodule "github.com/domainry/domainry-agent/module"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

func TestConversationModelToPersistedBrowserStreamAndDisconnectReplay(t *testing.T) {
	for _, mode := range []string{"module", "saas"} {
		t.Run(mode, func(t *testing.T) {
			finish := make(chan struct{})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			providerCancelled := make(chan struct{}, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() { providerCancelled <- struct{}{} }()
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"model\":\"fixture\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"先到文字\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"，再完成\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			model, err := provider.NewConversationModel(provider.ConversationModelConfig{Provider: "gateway", URL: upstream.URL, APIKey: "test-key", Model: "fixture", Client: upstream.Client()})
			if err != nil {
				t.Fatal(err)
			}
			a := conversationAuthority()
			var binding agentsdk.Binding
			if mode == "module" {
				binding, err = agentmodule.NewFactory(agentmodule.Options{ConversationProvider: model, ConversationOptions: conversationOptions()}).OpenModule(ctx, agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, newSQLiteModuleHost(t, a.RuntimeID))
			} else {
				service, e := conversationassembly.NewService(conversationRepository(t), model, a.RuntimeID, conversationOptions())
				if e != nil {
					t.Fatal(e)
				}
				defer service.Close()
				server, e := agentserver.New(agentserver.Config{APIKey: "service-key", Conversations: service, ConversationRuntimeID: a.RuntimeID})
				if e != nil {
					t.Fatal(e)
				}
				saas := httptest.NewServer(server.Handler())
				defer saas.Close()
				binding, err = agentremote.NewFactory(agentremote.Options{BaseURL: saas.URL, APIKey: "service-key", Client: saas.Client()}).OpenSaaS(ctx, agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer binding.Close(context.Background())
			if !binding.Descriptor().HasCapability(agentsdk.CapabilityConversationStreamV1) {
				t.Fatal("streaming capability missing")
			}
			if mode == "saas" && (binding.TaskRunner() != nil || binding.InteractiveRunner() != nil) {
				t.Fatal("conversation-only endpoint exposes legacy runners")
			}
			var adapter modulehttp.Adapter
			for _, candidate := range binding.(modulehttp.Provider).HTTPAdapters() {
				if candidate.Name() == "conversations" {
					adapter = candidate
				}
			}
			if adapter == nil {
				t.Fatal("missing conversation browser adapter")
			}
			browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				identity := identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: a.WorkspaceID, UserID: a.UserID}}
				adapter.Handler().ServeHTTP(w, r.WithContext(identitysdk.WithRequestIdentity(r.Context(), identity)))
			}))
			defer browser.Close()
			svc := binding.(agentsdk.ConversationBinding).Conversations()
			c, err := svc.Create(ctx, agentsdk.ConversationCreate{ClientID: "stream"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := svc.Send(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "m", Message: "hello"}, a)
			if err != nil {
				t.Fatal(err)
			}
			path := browser.URL + "/agent/conversations/" + c.ID + "/runs/" + run.ID + "/events/stream"
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			response, err := browser.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			cursor := int64(0)
			observed := false
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "id: ") {
					cursor, _ = strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
				}
				if strings.HasPrefix(line, "data: ") {
					var event agentsdk.ConversationEvent
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event.Type == "message.delta" {
						observed = true
						break
					}
				}
			}
			if !observed {
				t.Fatal("no text arrived before upstream finished", scanner.Err())
			}
			snapshot, err := svc.Run(ctx, c.ID, run.ID, a)
			if err != nil || snapshot.Status != "running" || snapshot.DraftText != "先到文字" || snapshot.LastEventSeq != cursor {
				t.Fatalf("delta was not persisted before SSE: %+v %v", snapshot, err)
			}
			history, err := svc.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(history.Items) != 1 {
				t.Fatal("unfinished assistant message in history", err)
			}
			response.Body.Close() // Browser disconnect must not cancel the durable run.
			finish <- struct{}{}
			done := waitConversation(t, svc, c.ID, run.ID)
			if done.Status != "completed" || done.DraftText != "" {
				t.Fatalf("run did not complete after browser disconnect %+v", done)
			}
			request, _ = http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			request.Header.Set("Last-Event-ID", strconv.FormatInt(cursor, 10))
			response, err = browser.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !strings.Contains(string(replay), "event: run.completed") || strings.Contains(string(replay), "先到文字") {
				t.Fatalf("incorrect resumed SSE %s %v", replay, err)
			}
			history, err = svc.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(history.Items) != 2 || history.Items[1].Content != "先到文字，再完成" {
				t.Fatal("incorrect final reply", err)
			}
			<-providerCancelled
			// A later cancellation closes the model HTTP request and never promotes its draft.
			next, err := svc.Send(ctx, c.ID, agentsdk.ConversationSend{ClientMessageID: "cancel", Message: "second"}, a)
			if err != nil {
				t.Fatal(err)
			}
			for {
				snapshot, err = svc.Run(ctx, c.ID, next.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.DraftBytes > 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("second draft missing")
				case <-time.After(5 * time.Millisecond):
				}
			}
			stopped, err := svc.Cancel(ctx, c.ID, next.ID, a)
			if err != nil || stopped.Status != "cancelled" {
				t.Fatal("cancel failed", err)
			}
			select {
			case <-providerCancelled:
			case <-ctx.Done():
				t.Fatal("cancel did not close provider request")
			}
			history, err = svc.Messages(ctx, c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(history.Items) != 3 || history.Items[2].Role != "user" {
				t.Fatal("cancelled draft entered history", err)
			}
		})
	}
}
