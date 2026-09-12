package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type businessActionPolicy struct{ personalReadAuthorizer }

func (businessActionPolicy) AuthorizeConversationInteraction(_ context.Context, a agentsdk.ConversationAuthority, _ agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: a == conversationAuthority()}, nil
}

type businessActionFixture struct {
	businessSourceFixture
	mu                           sync.Mutex
	changed                      atomic.Bool
	invokes, effects, reconciles int
	unknown                      bool
	saved                        agentsdk.ConversationBusinessActionResult
	request                      agentsdk.ConversationBusinessActionRequest
}

func (f *businessActionFixture) AuthorizeBusinessAction(_ context.Context, q agentsdk.ConversationBusinessAction, a agentsdk.ConversationAuthority) (agentsdk.ConversationToolAuthorization, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationToolAuthorization{}, err
	}
	if q.ActionKey == "" {
		return agentsdk.ConversationToolAuthorization{Granted: true}, nil
	}
	if f.changed.Load() || q.Version != "action-v1" {
		return agentsdk.ConversationToolAuthorization{}, &agentsdk.Error{Class: "conflict", Code: "business_action_changed"}
	}
	if q.ActionKey != "customer.rename" || q.ObjectKey != "customer" || q.RecordID != "customer-2" {
		return agentsdk.ConversationToolAuthorization{}, &agentsdk.Error{Class: "forbidden"}
	}
	var payload map[string]string
	if json.Unmarshal(q.Data, &payload) != nil || len(payload) != 1 || payload["name"] == "" {
		return agentsdk.ConversationToolAuthorization{}, &agentsdk.Error{Class: "bad_request", Code: "business_action_invalid"}
	}
	return agentsdk.ConversationToolAuthorization{Granted: true}, nil
}
func (f *businessActionFixture) InvokeBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invokes++
	if _, err := f.AuthorizeBusinessAction(ctx, q.Action, q.Authority); err != nil {
		return agentsdk.ConversationBusinessActionResult{}, err
	}
	if q.Confirmation == nil || q.Confirmation.UserID != q.Authority.UserID || q.Confirmation.ID == "" || q.IdempotencyKey == "" || q.Arguments == "" {
		return agentsdk.ConversationBusinessActionResult{}, fmt.Errorf("missing trusted execution metadata")
	}
	if f.saved.InvocationID == "" {
		f.effects++
		f.request = q
		f.saved = agentsdk.ConversationBusinessActionResult{Status: "completed", InvocationID: q.IdempotencyKey, ActionKey: q.Action.ActionKey, ObjectKey: q.Action.ObjectKey, RecordID: q.Action.RecordID}
	}
	if f.unknown {
		return agentsdk.ConversationBusinessActionResult{Status: "uncertain"}, nil
	}
	return f.saved, nil
}
func (f *businessActionFixture) ReconcileBusinessAction(ctx context.Context, q agentsdk.ConversationBusinessActionRequest) (agentsdk.ConversationBusinessActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciles++
	if _, err := f.AuthorizeBusinessAction(ctx, q.Action, q.Authority); err != nil {
		return agentsdk.ConversationBusinessActionResult{}, err
	}
	if q.IdempotencyKey != f.request.IdempotencyKey || q.Confirmation == nil || q.Confirmation.ID != f.request.Confirmation.ID {
		return agentsdk.ConversationBusinessActionResult{}, fmt.Errorf("changed reconciliation identity")
	}
	return f.saved, nil
}
func (f *businessActionFixture) RevalidateBusinessAction(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.AuthorizeBusinessAction(ctx, f.request.Action, a); err != nil {
		return err
	}
	if e.Operation != "invoke_action" || string(e.Input) != f.request.Arguments || string(e.Data) != string(mustJSON(f.saved)) {
		return fmt.Errorf("acknowledgement does not match receipt")
	}
	return nil
}

func businessActionCall() agentsdk.ConversationBusinessAction {
	return agentsdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: "customer.rename", Version: "action-v1", RecordID: "customer-2", Data: json.RawMessage(`{"name":"已修改客户"}`)}
}

func TestBusinessActionConfirmationRestartDuplicateAndReconciliation(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy, source := businessActionPolicy{}, &businessActionFixture{unknown: true}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if n == 1 {
			return resultToolCall("invoke_action", "rename", businessActionCall()), nil
		}
		if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "invocation_id") {
			t.Error("missing actual action receipt")
		}
		return (&executionModel{}).answerResult(), nil
	}}
	options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "action-confirmation"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "rename", Message: "修改客户", WriteScope: &agentsdk.ConversationWriteScope{PersonalMemory: true, PersonalTodos: true, PersonalArtifacts: true}}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
	if waiting.Interaction == nil || !strings.Contains(waiting.Interaction.Arguments, "customer-2") {
		t.Fatal("concrete target missing from confirmation")
	}
	source.mu.Lock()
	before := source.effects
	source.mu.Unlock()
	if before != 0 {
		t.Fatal("personal write scope authorized a business mutation")
	}
	service.Close()
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	unknown := waitConversationState(t, service, c.ID, run.ID, "needs_reconciliation")
	if unknown.Interaction == nil || unknown.Interaction.ID == waiting.Interaction.ID {
		t.Fatal("confirmation overwritten by reconciliation")
	}
	if _, err := service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" {
		t.Fatal(done)
	}
	source.mu.Lock()
	effects, invokes, reconciles := source.effects, source.invokes, source.reconciles
	source.mu.Unlock()
	if effects != 1 || invokes != 1 || reconciles != 1 {
		t.Fatalf("effects=%d invokes=%d reconciles=%d", effects, invokes, reconciles)
	}
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 3 || page.Items[1].InteractionID != waiting.Interaction.ID || page.Items[2].Role != "assistant" || page.Items[2].AccessError != "" {
		t.Fatal(page, err)
	}
	source.revoked.Store(true)
	page, err = service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || page.Items[len(page.Items)-1].AccessError == "" {
		t.Fatal("revoked action acknowledgement remained visible", err)
	}
}

func TestBusinessActionRejectsApprovalAfterPermissionOrContractChanges(t *testing.T) {
	for _, change := range []string{"permission", "contract", "reject"} {
		t.Run(change, func(t *testing.T) {
			repo, a := conversationRepository(t), conversationAuthority()
			policy, source := businessActionPolicy{}, &businessActionFixture{}
			host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
			if err != nil {
				t.Fatal(err)
			}
			model := &executionModel{step: func(int, agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				return resultToolCall("invoke_action", "rename", businessActionCall()), nil
			}}
			service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: change}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "rename", Message: "修改客户"}, a)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
			response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "respond", ExpectedRevision: waiting.Interaction.Revision, Decision: "approve"}
			switch change {
			case "permission":
				source.revoked.Store(true)
			case "contract":
				source.changed.Store(true)
			case "reject":
				response.Decision = "reject"
			}
			_, err = service.Respond(t.Context(), c.ID, run.ID, response, a)
			if change != "reject" && err == nil {
				t.Fatal("invalidated confirmation accepted")
			}
			if change == "reject" && err != nil {
				t.Fatal(err)
			}
			source.mu.Lock()
			defer source.mu.Unlock()
			if source.effects != 0 {
				t.Fatal("rejected operation executed")
			}
		})
	}
}

func TestBusinessActionInvalidPayloadFeedsBackWithoutConfirmation(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy, source := businessActionPolicy{}, &businessActionFixture{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if n == 1 {
			action := businessActionCall()
			action.Data = json.RawMessage(`{"unknown":"value"}`)
			return resultToolCall("invoke_action", "bad", action), nil
		}
		if !strings.Contains(in.Messages[len(in.Messages)-1].Content, "business_action_invalid") {
			t.Error("missing business validation feedback")
		}
		return (&executionModel{}).answerResult(), nil
	}}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "invalid-action"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "invalid", Message: "错误参数"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" || done.Interaction != nil {
		t.Fatal(done)
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.invokes != 0 {
		t.Fatal("invalid business payload reached mutation port")
	}
}
