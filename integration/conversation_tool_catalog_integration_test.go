package integration_test

import (
	"context"
	"errors"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"strings"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
)

type availabilityExecutionHost struct {
	*executionHost
	ready                  atomic.Bool
	disableAtAuthorization bool
}

type availabilityApplicationHost struct{ conversationApplicationHost }

func (availabilityApplicationHost) ConversationToolAvailable(_ context.Context, a agentsdk.ConversationAuthority, key string) (bool, error) {
	return a == conversationAuthority() && key != "calculate", nil
}

func (h *availabilityExecutionHost) ConversationToolAvailable(_ context.Context, a agentsdk.ConversationAuthority, key string) (bool, error) {
	return a == conversationAuthority() && key == "create_item" && h.ready.Load(), nil
}
func (h *availabilityExecutionHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	auth, err := h.executionHost.AuthorizeConversationTool(ctx, in)
	if h.disableAtAuthorization && in.Call.ID != "" {
		h.ready.Store(false)
	}
	return auth, err
}

func TestConversationCatalogConnectionChangesAndFrozenResume(t *testing.T) {
	repo := conversationRepository(t)
	host := &availabilityExecutionHost{executionHost: &executionHost{allowed: true}}
	host.ready.Store(true)
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if len(in.Tools) != 1 || in.Tools[0].Key != "create_item" {
			t.Fatal("frozen registered tool contract changed")
		}
		if number == 1 {
			return model.callResult(`{"title":"catalog resume"}`), nil
		}
		if number == 2 {
			return agentsdk.ConversationStepResult{}, errors.New("interrupt model after accepted effect")
		}
		if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "created-id") {
			t.Error("lost accepted result across restart")
		}
		return model.answerResult(), nil
	}
	options := application.ConversationOptions{ToolHost: host}
	s, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	a := conversationAuthority()
	c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "catalog-resume"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create item"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitConversation(t, s, c.ID, run.ID); got.Status != "failed" {
		t.Fatalf("first=%+v", got)
	}
	s.Close()
	host.ready.Store(false)
	s, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	// The shared source audit now rejects every disabled tool before replaying
	// its accepted result, including extension/local tools.
	if got := waitConversation(t, s, c.ID, run.ID); got.Status != "failed" || got.ErrorCode != "tool_unavailable" {
		t.Fatalf("disconnected=%+v", got)
	}
	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	if calls != 2 {
		t.Fatal("disconnected frozen input reached model")
	}
	host.ready.Store(true)
	if _, err = s.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if got := waitConversation(t, s, c.ID, run.ID); got.Status != "completed" {
		t.Fatalf("restored=%+v", got)
	}
	host.mu.Lock()
	invokes, reconciles := host.invokes, host.reconciles
	host.mu.Unlock()
	if invokes != 1 || reconciles != 0 {
		t.Fatalf("effect repeated: invokes=%d reconciles=%d", invokes, reconciles)
	}
}

func TestConversationCatalogRechecksConnectionAfterConcreteAuthorization(t *testing.T) {
	host := &availabilityExecutionHost{executionHost: &executionHost{allowed: true}, disableAtAuthorization: true}
	host.ready.Store(true)
	model := &executionModel{}
	model.step = func(_ int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if len(in.Tools) != 1 {
			t.Fatal("initial tool was not advertised")
		}
		return model.callResult(`{"title":"must not write"}`), nil
	}
	s, err := conversationassembly.NewService(conversationRepository(t), model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := conversationAuthority()
	c, err := s.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "catalog-disable"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create item"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitConversation(t, s, c.ID, run.ID); got.Status != "failed" || got.ErrorCode != "tool_unavailable" {
		t.Fatalf("run=%+v", got)
	}
	host.mu.Lock()
	invokes, reconciles := host.invokes, host.reconciles
	host.mu.Unlock()
	if invokes != 0 || reconciles != 0 {
		t.Fatal("connection disabled before invocation still performed effect")
	}
}
