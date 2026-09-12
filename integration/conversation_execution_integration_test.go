package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
)

type executionModel struct {
	mu    sync.Mutex
	calls int
	step  func(int, agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error)
}

func (*executionModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only model call")
}
func (*executionModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "test", Protocol: "chat_completions", Model: "tool-model", Fingerprint: "stable-test-model"}
}
func (m *executionModel) StreamConversationStep(ctx context.Context, in agentsdk.ConversationStepRequest, emit func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.calls++
	number := m.calls
	m.mu.Unlock()
	out, err := m.step(number, in)
	if err != nil {
		return out, err
	}
	if out.Message.Content != "" {
		if err = emit(agentsdk.ConversationModelEvent{Type: "text.delta", Delta: out.Message.Content}); err != nil {
			return agentsdk.ConversationStepResult{}, err
		}
	}
	return out, nil
}
func (*executionModel) callResult(arguments string) agentsdk.ConversationStepResult {
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "call-one", Name: "create_item", Arguments: arguments}}}, FinishReason: "tool_calls", Model: "tool-model", Usage: map[string]any{"total_tokens": 10}}
}
func (*executionModel) answerResult() agentsdk.ConversationStepResult {
	return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已创建事项。"}, FinishReason: "stop", Model: "tool-model", Usage: map[string]any{"total_tokens": 5}}
}

type executionHost struct {
	mu                              sync.Mutex
	invokes, reconciles, authorizes int
	keys                            []string
	allowed                         bool
	uncertain                       bool
	idempotency                     string
	revokeAfterInvoke               bool
}

func (h *executionHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	strategy := h.idempotency
	if strategy == "" {
		strategy = "key"
	}
	return []agentsdk.ConversationToolDefinition{{Key: "create_item", Version: "1", Description: "Create a personal item", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":1}},"required":["title"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`), ActionKey: "items.create", Effect: "write", Idempotency: strategy, MaxOutputBytes: 1024, TimeoutMillis: 1000}}, nil
}
func (h *executionHost) AuthorizeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authorizes++
	return agentsdk.ConversationToolAuthorization{Granted: h.allowed && request.Authority.UserID == conversationAuthority().UserID, Revision: "authorization-1"}, nil
}
func (h *executionHost) InvokeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.invokes++
	if h.revokeAfterInvoke {
		h.allowed = false
	}
	h.keys = append(h.keys, request.IdempotencyKey)
	if h.uncertain {
		return agentsdk.ConversationToolResult{}, fmt.Errorf("connection lost after effect")
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created-id"}`), ResourceID: "created-id"}, nil
}

func TestConversationExecutionReauthorizesToolDataBeforeNextModelRequest(t *testing.T) {
	repo := conversationRepository(t)
	host := &executionHost{allowed: true, revokeAfterInvoke: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number > 1 {
			t.Error("revoked tool data reached model")
		}
		return model.callResult(`{"title":"one"}`), nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "revoked-data"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, c.ID, run.ID)
	if final.ErrorCode != "tool_access_denied" {
		t.Fatalf("run=%+v", final)
	}
	if len(final.Steps) != 2 || len(final.Steps[0].Calls) != 1 || final.Steps[0].Calls[0].Status != "completed" {
		t.Fatal("completed effect lost from public execution snapshot")
	}
}
func (h *executionHost) ReconcileConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconciles++
	h.keys = append(h.keys, request.IdempotencyKey)
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created-id"}`), ResourceID: "created-id"}, nil
}

func TestConversationExecutionResumesAfterModelFailureWithoutRepeatingWrite(t *testing.T) {
	repo := conversationRepository(t)
	host := &executionHost{allowed: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return model.callResult(`{"title":"发布验收"}`), nil
		}
		if number == 2 {
			return agentsdk.ConversationStepResult{}, fmt.Errorf("temporary provider failure")
		}
		if len(in.Messages) < 2 || in.Messages[len(in.Messages)-1].Role != "tool" || in.Messages[len(in.Messages)-1].ToolCallID != "call-one" {
			t.Error("missing persisted tool result")
		}
		return model.answerResult(), nil
	}
	options := application.ConversationOptions{ToolHost: host}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	a := conversationAuthority()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "tool-resume"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "创建发布验收事项"}, a)
	if err != nil {
		t.Fatal(err)
	}
	first := waitConversation(t, service, c.ID, run.ID)
	if first.Status != "failed" {
		t.Fatalf("expected interrupted answer %+v", first)
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
		t.Fatalf("resume failed %+v", final)
	}
	host.mu.Lock()
	invokes, authorizes := host.invokes, host.authorizes
	host.mu.Unlock()
	if invokes != 1 || authorizes != 4 {
		t.Fatalf("write repeated or resume authorization skipped: %d %d", invokes, authorizes)
	}
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 2 || page.Items[1].Content != "已创建事项。" {
		t.Fatalf("final history %+v %v", page, err)
	}
	if final.Usage["total_tokens"] != float64(15) {
		t.Fatalf("resumed step usage double-counted: %+v", final.Usage)
	}
}

func TestConversationExecutionReconcilesUnknownWritesAndReauthorizesResume(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint("revoke=", revoke), func(t *testing.T) {
			repo := conversationRepository(t)
			host := &executionHost{allowed: true, uncertain: true}
			model := &executionModel{}
			model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if number == 1 {
					return model.callResult(`{"title":"one"}`), nil
				}
				return model.answerResult(), nil
			}
			service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			a := conversationAuthority()
			c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "uncertain"}, a)
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create one"}, a)
			if err != nil {
				t.Fatal(err)
			}
			first := waitConversationState(t, service, c.ID, run.ID, "needs_reconciliation")
			if first.Interaction == nil || first.Interaction.Kind != "reconciliation" {
				t.Fatalf("unknown effect was not retained: %+v", first)
			}
			host.mu.Lock()
			host.allowed = !revoke
			host.mu.Unlock()
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			host.mu.Lock()
			defer host.mu.Unlock()
			if host.invokes != 1 {
				t.Fatal("unknown write repeated")
			}
			if revoke {
				if host.reconciles != 0 || final.ErrorCode != "tool_access_denied" {
					t.Fatalf("revoked run executed %+v", final)
				}
			} else if host.reconciles != 1 || final.Status != "completed" || len(host.keys) != 2 || host.keys[0] != host.keys[1] {
				t.Fatalf("reconciliation lost key or failed: %+v %+v", host, final)
			}
		})
	}
}

type timedOutWriteHost struct {
	mu         sync.Mutex
	invokes    int
	reconciles int
	committed  bool
	keys       []string
}

func (*timedOutWriteHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{{
		Key: "create_item", Version: "1", Description: "Create an item through a bounded external write",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
		ActionKey:    "items.create", Effect: "write", Idempotency: "reconcile", TimeoutMillis: 5000, MaxOutputBytes: 1024,
	}}, nil
}

func (*timedOutWriteHost) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: "allowed-v1"}, nil
}

func (h *timedOutWriteHost) InvokeConversationTool(ctx context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	h.invokes++
	h.committed = true
	h.keys = append(h.keys, request.IdempotencyKey)
	h.mu.Unlock()
	<-ctx.Done()
	return agentsdk.ConversationToolResult{}, ctx.Err()
}

func (h *timedOutWriteHost) ReconcileConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconciles++
	h.keys = append(h.keys, request.IdempotencyKey)
	if !h.committed {
		return agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}, nil
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"created-after-timeout"}`), ResourceID: "created-after-timeout"}, nil
}

func TestConversationWriteTimeoutReconcilesAfterRestartWithoutRepeatingEffect(t *testing.T) {
	repo := conversationRepository(t)
	host := &timedOutWriteHost{}
	model := &executionModel{}
	model.step = func(number int, _ agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return model.callResult(`{"title":"timeout"}`), nil
		}
		return model.answerResult(), nil
	}
	options := conversationOptions()
	options.ToolHost = host
	options.ExternalCallTimeout = 50 * time.Millisecond
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	a := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "write-timeout"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "write-timeout", Message: "create one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	unknown := waitConversationState(t, service, conversation.ID, run.ID, "needs_reconciliation")
	unknownAudit := false
	for _, event := range unknown.Audit {
		unknownAudit = unknownAudit || event.Type == "tool" && event.Status == "uncertain" && event.ErrorCode == "external_result_unknown"
	}
	if len(unknown.Steps) != 1 || len(unknown.Steps[0].Calls) != 1 || unknown.Steps[0].Calls[0].Status != "needs_reconciliation" || !unknownAudit {
		t.Fatalf("write timeout was not persisted as unknown: %+v", unknown)
	}
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Resume(t.Context(), conversation.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	final := waitConversationState(t, service, conversation.ID, run.ID, "completed")
	if final.Steps[0].Calls[0].ResourceID != "created-after-timeout" {
		t.Fatalf("reconciled receipt missing: %+v", final.Steps)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 1 || host.reconciles != 1 || len(host.keys) != 2 || host.keys[0] == "" || host.keys[0] != host.keys[1] {
		t.Fatalf("timed-out write replayed or changed key: invokes=%d reconciles=%d keys=%v", host.invokes, host.reconciles, host.keys)
	}
}

func TestConversationExecutionValidatesSchemaBeforeHostEffects(t *testing.T) {
	host := &executionHost{allowed: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return model.callResult(`{"title":7,"unexpected":true}`), nil
		}
		var result agentsdk.ConversationToolResult
		if json.Unmarshal([]byte(in.Messages[len(in.Messages)-1].Content), &result) != nil || result.ErrorCode != "arguments_invalid" {
			t.Error("model did not receive validation error")
		}
		var feedback struct {
			Issues []json.RawMessage `json:"issues"`
		}
		if json.Unmarshal(result.Content, &feedback) != nil || len(feedback.Issues) == 0 {
			t.Error("model did not receive correctable schema fields")
		}
		return model.answerResult(), nil
	}
	service, err := conversationassembly.NewService(conversationRepository(t), model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "validation"}, a)
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "Create one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
		t.Fatalf("validation feedback stopped execution %+v", final)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 0 {
		t.Fatal("invalid input reached business effect")
	}
}
