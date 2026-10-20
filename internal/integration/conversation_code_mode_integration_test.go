package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
)

type codeModeHost struct {
	mu      sync.Mutex
	invokes int
}

func (*codeModeHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{{Key: "lookup", Version: "1", Description: "Lookup one bounded value", InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}},"required":["count"],"additionalProperties":false}`), ActionKey: "lookup.read", Effect: "read", Idempotency: "natural", TimeoutMillis: 1000, MaxOutputBytes: 1024}}, nil
}
func (*codeModeHost) AuthorizeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: request.Authority.Known, Revision: "code-test-policy"}, nil
}
func (h *codeModeHost) InvokeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	h.invokes++
	h.mu.Unlock()
	if request.Call.Name != "lookup" || request.IdempotencyKey == "" {
		return agentsdk.ConversationToolResult{}, fmt.Errorf("unexpected nested request")
	}
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"count":2}`)}, nil
}
func (h *codeModeHost) ReconcileConversationTool(ctx context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return h.InvokeConversationTool(ctx, request)
}

type codeModeRuntime struct {
	calls int
	err   error
}

func (r *codeModeRuntime) ExecuteConversationCode(ctx context.Context, request agentsdk.ConversationCodeExecution, dispatch agentsdk.ConversationCodeDispatcher) (agentsdk.ConversationCodeResult, error) {
	r.calls++
	lookup := false
	for _, definition := range request.Tools {
		lookup = lookup || definition.Key == "lookup"
	}
	if request.Language != "lua" || !lookup {
		r.err = fmt.Errorf("unexpected frozen code catalog: %+v", request)
		return agentsdk.ConversationCodeResult{}, r.err
	}
	result, err := dispatch(ctx, agentsdk.ConversationCodeDispatch{Index: 0, Name: "lookup", Arguments: json.RawMessage(`{"query":"alpha"}`)})
	if err != nil {
		r.err = err
		return agentsdk.ConversationCodeResult{}, err
	}
	if result.Status != "completed" || string(result.Content) != `{"count":2}` {
		return agentsdk.ConversationCodeResult{}, fmt.Errorf("unexpected nested receipt")
	}
	return agentsdk.ConversationCodeResult{Language: "lua", Value: json.RawMessage(`{"count":3}`), Dispatches: 1}, nil
}

func TestConversationCodeModeRecordsNestedToolReceipt(t *testing.T) {
	repo := conversationRepository(t)
	host := &codeModeHost{}
	runtime := &codeModeRuntime{}
	model := &executionModel{}
	model.step = func(number int, input agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			visible := map[string]bool{}
			for _, definition := range input.Tools {
				visible[definition.Key] = true
			}
			if !visible[agentsdk.ConversationCodeToolKey] || !visible["lookup"] {
				t.Fatal("run_code or nested binding missing from frozen catalog")
			}
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "code-parent", Name: agentsdk.ConversationCodeToolKey, Arguments: `{"language":"lua","source":"return tools.lookup({query='alpha'})"}`}}}, FinishReason: "tool_calls", Model: "tool-model"}, nil
		}
		if len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != "tool" {
			t.Fatal("code result did not reach the next model step")
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "组合完成"}, FinishReason: "stop", Model: "tool-model"}, nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: host, CodeRuntime: runtime, MaxToolCalls: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authority := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "code-mode"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "code-mode-message", Message: "组合查询结果"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "completed" || len(final.Steps) != 2 || len(final.Steps[0].Calls) != 1 || len(final.Steps[0].Calls[0].Subcalls) != 1 {
		t.Fatalf("nested execution was not projected: runtime_calls=%d runtime_error=%v run=%+v", runtime.calls, runtime.err, final)
	}
	child := final.Steps[0].Calls[0].Subcalls[0]
	if child.Name != "lookup" || child.ParentCallID != "code-parent" || child.DispatchIndex != 0 || child.Status != "completed" || final.Metrics.ToolCalls != 2 {
		t.Fatalf("nested receipt or audit metrics are incomplete: child=%+v metrics=%+v", child, final.Metrics)
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 || runtime.calls != 1 {
		t.Fatalf("unexpected executions: nested=%d code=%d", invokes, runtime.calls)
	}
}

type confirmedCodeHost struct {
	mu      sync.Mutex
	invokes int
}

func (*confirmedCodeHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{{Key: "write_item", Version: "1", Description: "Write one item", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`), ActionKey: "item.write", Effect: "write", Idempotency: "key", TimeoutMillis: 1000, MaxOutputBytes: 1024}}, nil
}
func (*confirmedCodeHost) AuthorizeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	authorization := agentsdk.ConversationToolAuthorization{Granted: request.Authority.Known, Revision: "confirmation-policy"}
	if request.Call.ID != "" && request.Definition.Key == "write_item" && request.Confirmation == nil {
		authorization.ConfirmationRequired = true
	}
	return authorization, nil
}
func (*confirmedCodeHost) AuthorizeConversationInteraction(context.Context, agentsdk.ConversationAuthority, agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: "confirmation-policy"}, nil
}
func (h *confirmedCodeHost) InvokeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	if request.Confirmation == nil || request.ConfirmationID == "" {
		return agentsdk.ConversationToolResult{}, fmt.Errorf("missing exact confirmation")
	}
	h.mu.Lock()
	h.invokes++
	h.mu.Unlock()
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"written"}`), ResourceID: "written"}, nil
}
func (h *confirmedCodeHost) ReconcileConversationTool(ctx context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	return h.InvokeConversationTool(ctx, request)
}

type confirmedCodeRuntime struct {
	calls int
}

func (r *confirmedCodeRuntime) ExecuteConversationCode(ctx context.Context, _ agentsdk.ConversationCodeExecution, dispatch agentsdk.ConversationCodeDispatcher) (agentsdk.ConversationCodeResult, error) {
	r.calls++
	result, err := dispatch(ctx, agentsdk.ConversationCodeDispatch{Index: 0, Name: "write_item", Arguments: json.RawMessage(`{"title":"only once"}`)})
	if err != nil {
		return agentsdk.ConversationCodeResult{}, err
	}
	return agentsdk.ConversationCodeResult{Language: "lua", Value: result.Content, Dispatches: 1}, nil
}

func TestConversationCodeModePausesBeforeNestedWriteAndResumesSameReceipt(t *testing.T) {
	repo := conversationRepository(t)
	host := &confirmedCodeHost{}
	runtime := &confirmedCodeRuntime{}
	model := &executionModel{}
	model.step = func(number int, input agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "confirmed-parent", Name: agentsdk.ConversationCodeToolKey, Arguments: `{"language":"lua","source":"return tools.write_item({title='only once'})"}`}}}, FinishReason: "tool_calls", Model: "tool-model"}, nil
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "写入完成"}, FinishReason: "stop", Model: "tool-model"}, nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: host, CodeRuntime: runtime, MaxToolCalls: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authority := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "code-confirmation"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "code-confirmation-message", Message: "写入一次"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, conversation.ID, run.ID, "waiting_confirmation")
	if waiting.Interaction == nil || waiting.Interaction.Tool != "write_item" || len(waiting.Interaction.Operations) != 0 || len(waiting.Steps[0].Calls[0].Subcalls) != 1 || waiting.Steps[0].Calls[0].Subcalls[0].Status != "waiting_confirmation" {
		t.Fatalf("nested confirmation was not isolated: %+v", waiting)
	}
	host.mu.Lock()
	before := host.invokes
	host.mu.Unlock()
	if before != 0 {
		t.Fatal("nested write ran before confirmation")
	}
	_, err = service.Respond(t.Context(), conversation.ID, run.ID, agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve-code-write", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if final.Status != "completed" || invokes != 1 || runtime.calls != 2 || len(final.Steps[0].Calls[0].Subcalls) != 1 || final.Steps[0].Calls[0].Subcalls[0].Status != "completed" {
		t.Fatalf("nested confirmation replay was not exact: invokes=%d code_runs=%d run=%+v", invokes, runtime.calls, final)
	}
}

type uncertainCodeHost struct {
	mu         sync.Mutex
	invokes    int
	reconciles int
	keys       []string
}

func (*uncertainCodeHost) ConversationTools(context.Context, agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	return []agentsdk.ConversationToolDefinition{{Key: "write_item", Version: "1", Description: "Write one item", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`), ActionKey: "item.write", Effect: "write", Idempotency: "reconcile", TimeoutMillis: 1000, MaxOutputBytes: 1024}}, nil
}
func (*uncertainCodeHost) AuthorizeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: request.Authority.Known, Revision: "uncertain-policy"}, nil
}
func (h *uncertainCodeHost) InvokeConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.invokes++
	h.keys = append(h.keys, request.IdempotencyKey)
	return agentsdk.ConversationToolResult{}, errors.New("response lost after external write")
}
func (h *uncertainCodeHost) ReconcileConversationTool(_ context.Context, request agentsdk.ConversationToolRequest) (agentsdk.ConversationToolResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconciles++
	h.keys = append(h.keys, request.IdempotencyKey)
	return agentsdk.ConversationToolResult{Status: "completed", Content: json.RawMessage(`{"id":"written"}`), ResourceID: "written"}, nil
}

func TestConversationCodeModeReconcilesUnknownNestedWriteWithoutRepeatingIt(t *testing.T) {
	repo := conversationRepository(t)
	host := &uncertainCodeHost{}
	runtime := &confirmedCodeRuntime{}
	model := &executionModel{}
	model.step = func(number int, input agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "uncertain-parent", Name: agentsdk.ConversationCodeToolKey, Arguments: `{"language":"lua","source":"return tools.write_item({title='only once'})"}`}}}, FinishReason: "tool_calls", Model: "tool-model"}, nil
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已核查写入结果"}, FinishReason: "stop", Model: "tool-model"}, nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: host, CodeRuntime: runtime, MaxToolCalls: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authority := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "code-uncertain"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "code-uncertain-message", Message: "写入并核查"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, conversation.ID, run.ID, "needs_reconciliation")
	if waiting.Interaction == nil || waiting.Interaction.Kind != "reconciliation" || waiting.Interaction.Tool != "write_item" || len(waiting.Steps[0].Calls[0].Subcalls) != 1 || waiting.Steps[0].Calls[0].Subcalls[0].Status != "needs_reconciliation" {
		t.Fatalf("unknown nested effect was not retained: %+v", waiting)
	}
	if _, err = service.Resume(t.Context(), conversation.ID, run.ID, authority); err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	host.mu.Lock()
	invokes, reconciles, keys := host.invokes, host.reconciles, append([]string(nil), host.keys...)
	host.mu.Unlock()
	if final.Status != "completed" || invokes != 1 || reconciles != 1 || runtime.calls != 2 || len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("unknown nested write was repeated or changed identity: invokes=%d reconciles=%d code_runs=%d keys=%v run=%+v", invokes, reconciles, runtime.calls, keys, final)
	}
	child := final.Steps[0].Calls[0].Subcalls[0]
	if child.Status != "completed" || child.ResourceID != "written" {
		t.Fatalf("reconciled nested receipt was not projected: %+v", child)
	}
}

type blockingCodeRuntime struct{ calls int }

func (r *blockingCodeRuntime) ExecuteConversationCode(ctx context.Context, _ agentsdk.ConversationCodeExecution, _ agentsdk.ConversationCodeDispatcher) (agentsdk.ConversationCodeResult, error) {
	r.calls++
	<-ctx.Done()
	return agentsdk.ConversationCodeResult{}, ctx.Err()
}

func TestConversationCodeModeEnforcesToolDeadlineAndRecordsFailure(t *testing.T) {
	repo := conversationRepository(t)
	host := &codeModeHost{}
	runtime := &blockingCodeRuntime{}
	model := &executionModel{}
	model.step = func(number int, input agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "timeout-parent", Name: agentsdk.ConversationCodeToolKey, Arguments: `{"language":"lua","source":"while true do end"}`}}}, FinishReason: "tool_calls", Model: "tool-model"}, nil
		}
		var result agentsdk.ConversationToolResult
		if len(input.Messages) == 0 || json.Unmarshal([]byte(input.Messages[len(input.Messages)-1].Content), &result) != nil || result.Status != "failed" || result.ErrorCode != "code_timeout" {
			t.Fatalf("code timeout did not reach the model as a failed receipt: %+v", input.Messages)
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "代码执行超时"}, FinishReason: "stop", Model: "tool-model"}, nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: host, CodeRuntime: runtime, MaxToolCalls: 4, ExternalCallTimeout: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authority := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "code-timeout"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "code-timeout-message", Message: "运行受限代码"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "completed" || runtime.calls != 1 || len(final.Steps) != 2 || final.Steps[0].Calls[0].Status != "failed" || final.Steps[0].Calls[0].ErrorCode != "code_timeout" {
		t.Fatalf("code deadline was not enforced: calls=%d run=%+v", runtime.calls, final)
	}
}
