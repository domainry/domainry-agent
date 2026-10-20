package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
)

func waitConversationForAuthority(t *testing.T, service agentsdk.ConversationService, conversationID, runID string, authority agentsdk.ConversationAuthority) agentsdk.ConversationRun {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := service.Run(t.Context(), conversationID, runID, authority)
		if err != nil {
			t.Fatal(err)
		}
		if run.Terminal() {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("conversation did not reach a terminal state")
	return agentsdk.ConversationRun{}
}

func TestConversationExternalModelTimeoutReleasesWorkerForAnotherWorkspace(t *testing.T) {
	repo := conversationRepository(t)
	entered := make(chan struct{}, 1)
	model := conversationModelFunc(func(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		last := in.Messages[len(in.Messages)-1].Content
		if strings.Contains(last, "slow") {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return agentsdk.ConversationModelResult{}, ctx.Err()
		}
		return agentsdk.ConversationModelResult{Content: "fast completed", Model: "capacity-fixture"}, nil
	})
	options := conversationOptions()
	options.ExternalCallTimeout = 40 * time.Millisecond
	options.RunTimeout = 2 * time.Second
	options.MaxQueuedPerUser, options.MaxQueuedPerWorkspace = 2, 2
	options.MaxRunningPerUser, options.MaxRunningPerWorkspace = 1, 1
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	b := a
	b.WorkspaceID, b.UserID = "workspace-b", "user-b"
	firstConversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "slow-workspace"}, a)
	if err != nil {
		t.Fatal(err)
	}
	secondConversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "fast-workspace"}, b)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	first, err := service.Send(t.Context(), firstConversation.ID, agentsdk.ConversationSend{ClientMessageID: "slow", Message: "slow external model"}, a)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow model was not called")
	}
	second, err := service.Send(t.Context(), secondConversation.ID, agentsdk.ConversationSend{ClientMessageID: "fast", Message: "fast work"}, b)
	if err != nil {
		t.Fatal(err)
	}
	first = waitConversationForAuthority(t, service, firstConversation.ID, first.ID, a)
	second = waitConversationForAuthority(t, service, secondConversation.ID, second.ID, b)
	if first.Status != "failed" || first.ErrorCode != "provider_timeout" {
		t.Fatalf("slow run=%+v", first)
	}
	if second.Status != "completed" || second.Model != "capacity-fixture" {
		t.Fatalf("following run=%+v", second)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("external timeout did not release the worker promptly: %s", elapsed)
	}
	capacity, err := service.ConversationExecutionCapacity(t.Context(), a)
	if err != nil || capacity != (agentsdk.ConversationExecutionCapacity{}) {
		t.Fatalf("terminal capacity=%+v err=%v", capacity, err)
	}
}

type slowToolModel struct {
	mu    sync.Mutex
	calls int
}

func (*slowToolModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, errors.New("unexpected text generation")
}
func (*slowToolModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "responses", Model: "slow-tool-model", Fingerprint: "slow-tool-v1"}
}
func (m *slowToolModel) StreamConversationStep(_ context.Context, _ agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	if call == 1 {
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "slow-call", Name: "slow_read", Arguments: `{}`}}}, FinishReason: "tool_calls", Model: "slow-tool-model"}, nil
	}
	content := "tool timeout handled"
	if err := emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: content}); err != nil {
		return agentsdk.ConversationStepResult{}, err
	}
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: content}, FinishReason: "stop", Model: "slow-tool-model"}, nil
}

type slowToolHost struct {
	mu       sync.Mutex
	duration time.Duration
}

func (*slowToolHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{{Key: "slow_read", Version: "1", Description: "Bounded slow read", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object"}`), ActionKey: "slow.read", Effect: "read", Idempotency: "natural", TimeoutMillis: 5000, MaxOutputBytes: 1024}}, nil
}
func (*slowToolHost) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: "allowed-v1"}, nil
}
func (h *slowToolHost) InvokeConversationTool(ctx context.Context, _ agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	started := time.Now()
	<-ctx.Done()
	h.mu.Lock()
	h.duration = time.Since(started)
	h.mu.Unlock()
	return agentsdk.ConversationToolResult{}, ctx.Err()
}
func (h *slowToolHost) ReconcileConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return h.InvokeConversationTool(ctx, in)
}

func TestConversationExternalToolTimeoutOverridesLongToolDefinition(t *testing.T) {
	repo := conversationRepository(t)
	host := &slowToolHost{}
	options := conversationOptions()
	options.ToolHost = host
	options.ExternalCallTimeout = 250 * time.Millisecond
	options.RunTimeout = 2 * time.Second
	service, err := conversationassembly.NewService(repo, &slowToolModel{}, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "slow-tool"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "slow-tool", Message: "call it"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run = waitConversationForAuthority(t, service, conversation.ID, run.ID, a)
	if run.Status != "completed" || len(run.Steps) < 1 || len(run.Steps[0].Calls) != 1 || run.Steps[0].Calls[0].ErrorCode != "tool_timeout" {
		t.Fatalf("run=%+v", run)
	}
	host.mu.Lock()
	duration := host.duration
	host.mu.Unlock()
	if duration < 100*time.Millisecond || duration > time.Second {
		t.Fatalf("tool duration=%s", duration)
	}
}
