package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"strings"
	"sync"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
)

type businessWorkflowFixture struct {
	businessSourceFixture
	mu                                 sync.Mutex
	request                            agentsdk.ConversationWorkflowStartRequest
	receipt                            agentsdk.ConversationWorkflowReceipt
	starts, effects, reconciles, reads int
}

func (f *businessWorkflowFixture) AuthorizeWorkflowStart(_ context.Context, start agentsdk.ConversationWorkflowStart, a agentsdk.ConversationAuthority) (agentsdk.ConversationToolAuthorization, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationToolAuthorization{}, err
	}
	if start.WorkflowKey != "" && (start.WorkflowKey != "review" || start.Version != "v1") {
		return agentsdk.ConversationToolAuthorization{}, &agentsdk.Error{Class: "conflict"}
	}
	return agentsdk.ConversationToolAuthorization{Granted: true}, nil
}

func (f *businessWorkflowFixture) StartBusinessWorkflow(ctx context.Context, request agentsdk.ConversationWorkflowStartRequest) (agentsdk.ConversationWorkflowReceipt, error) {
	if _, err := f.AuthorizeWorkflowStart(ctx, request.Start, request.Authority); err != nil {
		return agentsdk.ConversationWorkflowReceipt{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.Confirmation == nil || request.Confirmation.UserID != request.Authority.UserID || request.IdempotencyKey == "" || request.Arguments == "" {
		return agentsdk.ConversationWorkflowReceipt{}, fmt.Errorf("missing trusted confirmation")
	}
	f.starts++
	if f.receipt.ProcessID == "" {
		f.effects++
		f.request = request
		f.receipt = agentsdk.ConversationWorkflowReceipt{Status: "accepted", WorkflowKey: "review", InvocationID: request.IdempotencyKey, ExecutionID: "execution-1", ProcessID: "process-1"}
	}
	return agentsdk.ConversationWorkflowReceipt{Status: "uncertain"}, nil
}

func (f *businessWorkflowFixture) ReconcileBusinessWorkflow(ctx context.Context, request agentsdk.ConversationWorkflowStartRequest) (agentsdk.ConversationWorkflowReceipt, error) {
	if _, err := f.AuthorizeWorkflowStart(ctx, request.Start, request.Authority); err != nil {
		return agentsdk.ConversationWorkflowReceipt{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconciles++
	if request.IdempotencyKey != f.request.IdempotencyKey || request.Arguments != f.request.Arguments {
		return agentsdk.ConversationWorkflowReceipt{}, fmt.Errorf("changed recovery")
	}
	return f.receipt, nil
}

func (f *businessWorkflowFixture) GetBusinessWorkflow(ctx context.Context, q agentsdk.ConversationWorkflowGet, a agentsdk.ConversationAuthority) (agentsdk.ConversationWorkflowState, error) {
	if err := f.authorize(a); err != nil {
		return agentsdk.ConversationWorkflowState{}, err
	}
	if q.WorkflowKey != "review" || q.ProcessID != "process-1" {
		return agentsdk.ConversationWorkflowState{}, fmt.Errorf("unknown instance")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return agentsdk.ConversationWorkflowState{WorkflowKey: "review", ProcessID: "process-1", Name: "Review", Status: "waiting", CurrentSteps: []string{"经理审批"}, CurrentStepCount: 1, UpdatedAt: "2026-09-10T08:00:00Z"}, nil
}

func (f *businessWorkflowFixture) RevalidateBusinessWorkflow(ctx context.Context, e agentsdk.ConversationBusinessEvidence, a agentsdk.ConversationAuthority) error {
	if err := f.authorize(a); err != nil {
		return err
	}
	if e.Operation == "workflow_start" {
		f.mu.Lock()
		defer f.mu.Unlock()
		if string(e.Input) != f.request.Arguments || string(e.Data) != string(mustJSON(f.receipt)) {
			return fmt.Errorf("changed receipt")
		}
		return nil
	}
	var q agentsdk.ConversationWorkflowGet
	if json.Unmarshal(e.Input, &q) != nil {
		return fmt.Errorf("invalid query")
	}
	current, err := f.GetBusinessWorkflow(ctx, q, a)
	if err != nil || string(e.Data) != string(mustJSON(current)) {
		return fmt.Errorf("changed state")
	}
	return nil
}

func TestConversationWorkflowConfirmedStartRestartReconcileAndProgress(t *testing.T) {
	repo, a := conversationRepository(t), conversationAuthority()
	policy, source := businessActionPolicy{}, &businessWorkflowFixture{}
	host, err := application.NewPersonalConversationHost(repo, policy, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		switch n {
		case 1:
			return resultToolCall("workflow_start", "start", agentsdk.ConversationWorkflowStart{WorkflowKey: "review", Version: "v1", Data: json.RawMessage(`{"reason":"review"}`)}), nil
		case 2:
			if !strings.Contains(in.Messages[len(in.Messages)-1].Content, `"status":"accepted"`) {
				t.Error("missing acceptance receipt")
			}
			return resultToolCall("workflow_get", "progress", agentsdk.ConversationWorkflowGet{WorkflowKey: "review", ProcessID: "process-1"}), nil
		default:
			if !strings.Contains(in.Messages[len(in.Messages)-1].Content, `"status":"waiting"`) {
				t.Error("missing actual waiting state")
			}
			return (&executionModel{}).answerResult(), nil
		}
	}}
	options := application.ConversationOptions{ToolHost: host, PersonalAuthorizer: policy, Business: source}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "workflow"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "start", Message: "发起审核", WriteScope: &agentsdk.ConversationWriteScope{PersonalTodos: true}}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
	if waiting.Interaction == nil || waiting.Interaction.Tool != "workflow_start" {
		t.Fatal(waiting)
	}
	source.mu.Lock()
	effects := source.effects
	source.mu.Unlock()
	if effects != 0 {
		t.Fatal("unconfirmed start")
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
	waitConversationState(t, service, c.ID, run.ID, "needs_reconciliation")
	if _, err := service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	if done := waitConversation(t, service, c.ID, run.ID); done.Status != "completed" {
		t.Fatal(done)
	}
	source.mu.Lock()
	starts, effects, reconciles, reads := source.starts, source.effects, source.reconciles, source.reads
	source.mu.Unlock()
	if starts != 1 || effects != 1 || reconciles != 1 || reads == 0 {
		t.Fatal(starts, effects, reconciles, reads)
	}
	page, err := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(page.Items) != 3 || page.Items[2].AccessError != "" {
		t.Fatal(page, err)
	}
	source.revoked.Store(true)
	page, err = service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || page.Items[2].AccessError == "" {
		t.Fatal("revoked workflow remained visible", page, err)
	}
}
