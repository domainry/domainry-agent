package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
)

type cancelledEffectHost struct {
	*executionHost
	started, stopped, release chan struct{}
	unknown                   bool
}

type delayedCancellationStore struct {
	*agentstore.ConversationStore
	committed, release chan struct{}
}

func (s *delayedCancellationStore) Cancel(ctx context.Context, id, run string, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	out, err := s.ConversationStore.Cancel(ctx, id, run, a)
	close(s.committed)
	select {
	case <-s.release:
	case <-ctx.Done():
		return out, ctx.Err()
	}
	return out, err
}

func TestConversationCancellationDoesNotStopAConcurrentlyResumedAttempt(t *testing.T) {
	repo := &delayedCancellationStore{ConversationStore: conversationRepository(t), committed: make(chan struct{}), release: make(chan struct{})}
	first, resumed, allow := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	model := conversationModelFunc(func(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		if calls.Add(1) == 1 {
			close(first)
			<-ctx.Done()
			return agentsdk.ConversationModelResult{}, ctx.Err()
		}
		close(resumed)
		select {
		case <-allow:
			return agentsdk.ConversationModelResult{Content: "new attempt completed"}, nil
		case <-ctx.Done():
			return agentsdk.ConversationModelResult{}, ctx.Err()
		}
	})
	options := conversationOptions()
	options.Workers = 2
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer func() { release.Do(func() { close(repo.release) }); service.Close() }()
	a := conversationAuthority()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "cancel-resume-race"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "resume me"}, a)
	if err != nil {
		t.Fatal(err)
	}
	awaitCancellationSignal(t, first, "first model attempt")
	response := make(chan error, 1)
	go func() { _, err := service.Cancel(t.Context(), c.ID, run.ID, a); response <- err }()
	awaitCancellationSignal(t, repo.committed, "cancellation committed before API returns")
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	awaitCancellationSignal(t, resumed, "resumed model attempt")
	release.Do(func() { close(repo.release) })
	select {
	case err = <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel API did not return")
	}
	close(allow)
	final := waitConversation(t, service, c.ID, run.ID)
	if final.Status != "completed" || final.Attempt != 2 {
		t.Fatal("old cancellation stopped new attempt", final)
	}
}

func (h *cancelledEffectHost) InvokeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	result, err := h.executionHost.InvokeConversationTool(ctx, in)
	if err != nil || in.Call.ID != "second" {
		return result, err
	}
	close(h.started) // External commit happened; its receipt has not arrived yet.
	<-ctx.Done()
	close(h.stopped)
	<-h.release // Simulate a definitive response racing transport cancellation.
	if h.unknown {
		return agentsdk.ConversationToolResult{}, ctx.Err()
	}
	return result, nil
}

func awaitCancellationSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s", name)
	}
}

func TestConversationCancellationPreservesLateEffectsAndReconcilesUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown=%v", unknown), func(t *testing.T) {
			repo := conversationRepository(t)
			host := &cancelledEffectHost{executionHost: &executionHost{allowed: true}, started: make(chan struct{}), stopped: make(chan struct{}), release: make(chan struct{}), unknown: unknown}
			var release sync.Once
			var modelCalls atomic.Int32
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				modelCalls.Add(1)
				if n > 1 {
					return (&executionModel{}).answerResult(), nil
				}
				var calls []agentsdk.ConversationToolCall
				for _, id := range []string{"first", "second", "third"} {
					calls = append(calls, agentsdk.ConversationToolCall{ID: id, Name: "create_item", Arguments: `{"title":"` + id + `"}`})
				}
				return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}, FinishReason: "tool_calls"}, nil
			}}
			options := application.ConversationOptions{ToolHost: host, Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}
			service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { release.Do(func() { close(host.release) }); service.Close() }()
			a := conversationAuthority()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "cancel-effects"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "effects", Message: "three writes"}, a)
			if err != nil {
				t.Fatal(err)
			}
			awaitCancellationSignal(t, host.started, "second external commit")
			stopped, err := service.Cancel(t.Context(), c.ID, run.ID, a)
			if err != nil || stopped.Status != "cancelled" {
				t.Fatal(stopped, err)
			}
			awaitCancellationSignal(t, host.stopped, "tool request cancellation")
			if stopped.Steps[0].Calls[0].Status != "completed" || stopped.Steps[0].Calls[1].Status != "uncertain" || stopped.Steps[0].Calls[2].Status != "not_started" {
				t.Fatal("false cancellation outcomes", stopped.Steps)
			}
			release.Do(func() { close(host.release) })
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				run, err = service.Run(t.Context(), c.ID, run.ID, a)
				if err != nil {
					t.Fatal(err)
				}
				if run.LastEventSeq > stopped.LastEventSeq {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if run.LastEventSeq <= stopped.LastEventSeq || run.Status != "cancelled" {
				t.Fatal("late receipt lost", run)
			}
			want := "completed"
			if unknown {
				want = "uncertain"
			}
			if run.Steps[0].Calls[1].Status != want || run.Steps[0].Calls[2].Status != "not_started" || modelCalls.Load() != 1 {
				t.Fatalf("late status=%+v models=%d", run.Steps, modelCalls.Load())
			}
			host.mu.Lock()
			invokes := host.invokes
			host.mu.Unlock()
			if invokes != 2 {
				t.Fatalf("cancel failed to stop third action: %d", invokes)
			}
			messages, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
			if err != nil || len(messages.Items) != 1 {
				t.Fatal("cancelled assistant reply saved", messages, err)
			}
			service.Close()
			service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			if final.Status != "completed" {
				t.Fatal("resume failed", final)
			}
			host.mu.Lock()
			invokes, reconciles := host.invokes, host.reconciles
			host.mu.Unlock()
			wantReconcile := 0
			if unknown {
				wantReconcile = 1
			}
			if invokes != 3 || reconciles != wantReconcile || modelCalls.Load() != 2 {
				t.Fatalf("replayed effects or skipped lookup: invokes=%d reconciles=%d models=%d", invokes, reconciles, modelCalls.Load())
			}
			t.Logf("cancel/reopen/resume: late=%s effects=%d reconciles=%d model=%d", want, invokes, reconciles, modelCalls.Load())
		})
	}
}

func TestConversationCancellationClosesOfficialKnowledgeConnectorRequest(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	a := conversationAuthority()
	knowledge, err := provider.NewKnowledge(provider.KnowledgeConfig{BaseURL: upstream.URL, APIKey: "isolated-test-key", TeamID: "team", KBID: "cancel-test", WorkspaceID: a.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	repo := conversationRepository(t)
	personal, err := application.NewPersonalConversationHost(repo, personalReadAuthorizer{}, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	var models atomic.Int32
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		models.Add(1)
		return resultToolCall("knowledge_search", "read", map[string]any{"query": "blocked upstream"}), nil
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: personal, PersonalAuthorizer: personalReadAuthorizer{}, Knowledge: knowledge, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "cancel-connector"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "read", Message: "query documents"}, a)
	if err != nil {
		t.Fatal(err)
	}
	awaitCancellationSignal(t, started, "official Connector HTTP request")
	stoppedRun, err := service.Cancel(t.Context(), c.ID, run.ID, a)
	if err != nil || stoppedRun.Status != "cancelled" {
		t.Fatal(stoppedRun, err)
	}
	awaitCancellationSignal(t, stopped, "upstream HTTP context closed")
	service.Close() // Wait for the original call's final receipt transaction.
	stored, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || stored.Status != "cancelled" || models.Load() != 1 {
		t.Fatal(stored, err)
	}
	if stored.Steps[0].Calls[0].Status != "failed" {
		t.Fatal("cancelled read receipt missing", stored.Steps)
	}
	if _, err = json.Marshal(stored); err != nil {
		t.Fatal(err)
	}
	t.Log("official knowledge Connector: cancellation reached the real HTTP server; no next model request")
}
