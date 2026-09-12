package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

type operationScopeBusiness struct {
	businessSourceFixture
	mu       sync.Mutex
	denied   atomic.Int32
	changed  atomic.Bool
	effects  int
	receipts map[string]agentsdk.ConversationBusinessActionResult
	requests map[string]agentsdk.ConversationBusinessActionRequest
}

func (f *operationScopeBusiness) AuthorizeBusinessAction(_ context.Context, q agentsdk.ConversationBusinessAction, a agentsdk.ConversationAuthority) (agentsdk.ConversationToolAuthorization, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationToolAuthorization{}, err
	}
	if q.ActionKey == "" {
		return agentsdk.ConversationToolAuthorization{Granted: true}, nil
	}
	if q.ActionKey != "customer.rename" || q.ObjectKey != "customer" || q.RecordID == fmt.Sprintf("customer-%d", f.denied.Load()) {
		return agentsdk.ConversationToolAuthorization{}, nil
	}
	if q.Version != "action-v1" || f.changed.Load() {
		return agentsdk.ConversationToolAuthorization{}, &agentsdk.Error{Class: "conflict", Code: "business_action_changed"}
	}
	return agentsdk.ConversationToolAuthorization{Granted: true}, nil
}
func (f *operationScopeBusiness) InvokeBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	if auth, err := f.AuthorizeBusinessAction(ctx, q.Action, q.Authority); err != nil || !auth.Granted {
		return agentsdk.ConversationBusinessActionResult{}, fmt.Errorf("current record policy denied")
	}
	if q.Confirmation == nil || q.Confirmation.ID == "" || q.Confirmation.UserID != q.Authority.UserID || q.IdempotencyKey == "" {
		return agentsdk.ConversationBusinessActionResult{}, fmt.Errorf("missing concrete approval")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.receipts[q.IdempotencyKey]; ok {
		return r, nil
	}
	if f.receipts == nil {
		f.receipts = map[string]agentsdk.ConversationBusinessActionResult{}
		f.requests = map[string]agentsdk.ConversationBusinessActionRequest{}
	}
	f.effects++
	r := agentsdk.ConversationBusinessActionResult{Status: "completed", InvocationID: fmt.Sprintf("operation-%d", f.effects), ObjectKey: q.Action.ObjectKey, ActionKey: q.Action.ActionKey, RecordID: q.Action.RecordID, UpdatedRecords: []agentsdk.ConversationBusinessRecordReference{{ObjectKey: q.Action.ObjectKey, RecordID: q.Action.RecordID}}, ReferenceCount: 1}
	f.receipts[q.IdempotencyKey] = r
	f.requests[q.IdempotencyKey] = q
	return r, nil
}
func (f *operationScopeBusiness) ReconcileBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	return f.InvokeBusinessAction(ctx, q)
}
func (f *operationScopeBusiness) RevalidateBusinessAction(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, r := range f.receipts {
		if string(e.Data) == string(mustJSON(r)) && string(e.Input) == f.requests[key].Arguments {
			auth, err := f.AuthorizeBusinessAction(ctx, f.requests[key].Action, a)
			if err != nil {
				return err
			}
			if auth.Granted {
				return nil
			}
		}
	}
	return fmt.Errorf("receipt no longer authorized")
}
func (f *operationScopeBusiness) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.effects }

type operationScopeGate struct{ pause atomic.Bool }

func (g *operationScopeGate) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	if in.Stage == "execute" && g.pause.Load() {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return in.Authority == conversationAuthority(), nil
}

func scopeActionCall(id string, record int) agentsdk.ConversationToolCall {
	q := businessActionCall()
	q.RecordID = fmt.Sprintf("customer-%d", record)
	q.Data = json.RawMessage(fmt.Sprintf(`{"name":"更新客户%d"}`, record))
	return agentsdk.ConversationToolCall{ID: id, Name: "invoke_action", Arguments: string(mustJSON(q))}
}

func TestConversationListedOperationsAreExactAndDurable(t *testing.T) {
	for _, scenario := range []string{"restart_and_new_call", "single_call_only", "denied_before_approval", "denied_after_approval", "contract_changed", "reject", "cross_user", "incomplete_parameters"} {
		t.Run(scenario, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			policy, source, gate := businessActionPolicy{}, &operationScopeBusiness{}, &operationScopeGate{}
			host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if n == 1 {
					calls := []agentsdk.ConversationToolCall{scopeActionCall("first", 1), scopeActionCall("second", 2)}
					if scenario == "incomplete_parameters" {
						calls[1].Arguments = `{"object_key":"customer"}`
					}
					return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "fixture", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: calls}}, nil
				}
				if n == 2 && scenario == "restart_and_new_call" {
					return resultToolCall("invoke_action", "new-call-outside-scope", businessActionCall()), nil
				}
				return (&executionModel{}).answerResult(), nil
			}}
			options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source, ExecutionAuthorizer: gate, Lease: 300 * time.Millisecond, Poll: 10 * time.Millisecond}
			service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { service.Close() })
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: scenario}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "修改这两位客户"}, a)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
			i := waiting.Interaction
			if scenario == "incomplete_parameters" {
				if i == nil || len(i.Operations) != 0 || source.count() != 0 {
					t.Fatal("incomplete call was offered as an approvable scope")
				}
				response := agentsdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-incomplete", ExpectedRevision: i.Revision, Decision: "approve", Scope: "listed_operations"}
				if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err == nil {
					t.Fatal("unlisted operations were approved")
				}
				response.Scope = ""
				if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
					t.Fatal(err)
				}
				done := waitConversation(t, service, c.ID, run.ID)
				if done.Status != "completed" || source.count() != 1 || len(done.Steps[0].Calls) != 2 || done.Steps[0].Calls[1].Status != "failed" {
					t.Fatalf("incomplete call produced an effect: %+v effects=%d", done, source.count())
				}
				return
			}
			if i == nil || len(i.Operations) != 2 || i.Operations[0].CallID != "first" || i.Operations[1].CallID != "second" || source.count() != 0 {
				t.Fatalf("missing concrete list: %+v", i)
			}
			response := agentsdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approval", ExpectedRevision: i.Revision, Decision: "approve", Scope: "listed_operations"}
			if scenario == "single_call_only" {
				response.Scope = ""
			}
			if scenario == "reject" {
				response.Decision = "reject"
				response.Scope = ""
			}
			if scenario == "denied_before_approval" {
				source.denied.Store(2)
			}
			if scenario == "contract_changed" {
				source.changed.Store(true)
			}
			if scenario == "cross_user" {
				a.UserID = "somebody-else"
			}
			gate.pause.Store(true)
			approved, err := service.Respond(t.Context(), c.ID, run.ID, response, a)
			if scenario == "denied_before_approval" || scenario == "contract_changed" || scenario == "cross_user" {
				if err == nil || source.count() != 0 {
					t.Fatal("invalid approval was accepted", err)
				}
				unchanged, err := repo.Run(t.Context(), c.ID, run.ID, conversationAuthority())
				if err != nil || unchanged.LastEventSeq != waiting.LastEventSeq || unchanged.Interaction.Status != "pending" {
					t.Fatal("denied approval changed persistence", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "reject" {
				if approved.Status != "cancelled" || source.count() != 0 {
					t.Fatal("rejection had an effect")
				}
				return
			}
			if response.Scope != "" && (approved.Interaction.ApprovedScope != "listed_operations" || approved.Interaction.AuthorizationID != i.ID) {
				t.Fatal("scope receipt absent")
			}
			// An exact duplicate keeps the same scope. Changing its scope is a
			// conflicting response, even if no external call has run yet.
			if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
				t.Fatal(err)
			}
			changed := response
			changed.Scope = "listed_operations"
			if response.Scope != "" {
				changed.Scope = ""
			}
			if _, err = service.Respond(t.Context(), c.ID, run.ID, changed, a); err == nil {
				t.Fatal("duplicate enlarged or changed approval scope")
			}
			service.Close()
			if source.count() != 0 {
				t.Fatal("paused approval executed")
			}
			gate.pause.Store(false)
			if scenario == "denied_after_approval" {
				source.denied.Store(2)
			}
			service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "denied_after_approval" {
				failed := waitConversation(t, service, c.ID, run.ID)
				if failed.Status != "failed" || source.count() != 1 {
					t.Fatalf("approval bypassed current second-record permission: %+v effects=%d", failed, source.count())
				}
				source.denied.Store(0)
				if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "single_call_only" || scenario == "restart_and_new_call" {
				next := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
				expectedCall, expectedEffects := "second", 1
				if scenario == "restart_and_new_call" {
					expectedCall = "new-call-outside-scope"
					expectedEffects = 2
				}
				if next.Interaction.CallID != expectedCall || source.count() != expectedEffects {
					t.Fatalf("scope expanded: %+v effects=%d", next.Interaction, source.count())
				}
				if _, err = service.Respond(t.Context(), c.ID, run.ID, agentsdk.ConversationInteractionResponse{InteractionID: next.Interaction.ID, ClientID: "approve-next", ExpectedRevision: next.Interaction.Revision, Decision: "approve"}, a); err != nil {
					t.Fatal(err)
				}
			}
			done := waitConversation(t, service, c.ID, run.ID)
			expected := 2
			if scenario == "restart_and_new_call" {
				expected = 3
			}
			if done.Status != "completed" || source.count() != expected {
				t.Fatalf("scope failed: %+v effects=%d", done, source.count())
			}
		})
	}
}

func TestConversationListedOperationScopeOverSaaS(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy, source := businessActionPolicy{}, &operationScopeBusiness{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, _ agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if n == 1 {
			return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{scopeActionCall("saas-first", 1), scopeActionCall("saas-second", 2)}}}, nil
		}
		return (&executionModel{}).answerResult(), nil
	}}
	local, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source, ExecutionAuthorizer: &operationScopeGate{}})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "scope-saas-fixture", Conversations: local, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "scope-saas-fixture", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	service := binding.(agentsdk.ConversationBinding).Conversations()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "scope-saas"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "修改两项"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
	if waiting.Interaction == nil || len(waiting.Interaction.Operations) != 2 {
		t.Fatal("SaaS dropped concrete scope")
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve", Scope: "all_tools"}
	interactions := service.(agentsdk.ConversationInteractionService)
	if _, err = interactions.Respond(t.Context(), c.ID, run.ID, response, a); err == nil {
		t.Fatal("unknown scope accepted over SaaS")
	}
	response.Scope = "listed_operations"
	if _, err = interactions.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal(err)
	}
	done := waitConversation(t, service, c.ID, run.ID)
	if done.Status != "completed" || source.count() != 2 || done.Interaction.ApprovedScope != "listed_operations" {
		t.Fatalf("SaaS scope lost: %+v effects=%d", done, source.count())
	}
}
