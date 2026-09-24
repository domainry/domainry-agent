package agent

import (
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestConversationModelAttemptsPersistWaitsAndDiscardFailedPreviews(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	ctx, authority := t.Context(), conversationTestAuthority()
	conversation, err := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "model-attempts", Title: "Model attempts"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(ctx, conversation.ID, agentsdk.ConversationSend{ClientMessageID: "one-request", Message: "Do the work"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := repo.Claim(ctx, authority.RuntimeID, "model-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	input := executionStoreInput()
	if _, found, err := repo.ExecutionStep(ctx, claim, 0, &input); err != nil || !found {
		t.Fatal("step", err)
	}
	first, err := repo.BeginConversationModelAttempt(ctx, claim, 0)
	if err != nil || first.Number != 1 {
		t.Fatal("begin", first, err)
	}
	if err = repo.AppendEvent(ctx, claim, "step.text.delta", map[string]any{"step": 0, "attempt": claim.Run.Attempt, "offset": 0, "delta": "partial"}); err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(-time.Millisecond)
	details := agentsdk.ConversationModelFailureDetails{Retryable: true, ErrorCode: "provider_rate_limited", Usage: map[string]any{"input_tokens": 7}}
	if err = repo.FailConversationModelAttempt(ctx, claim, 0, first.Number, details, &retryAt); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.Run(ctx, conversation.ID, run.ID, authority)
	if err != nil || len(saved.ModelAttempts) != 1 || saved.ModelAttempts[0].Status != "retry_scheduled" || saved.ModelAttempts[0].ErrorCode != "provider_rate_limited" || len(saved.Steps) != 1 || saved.Steps[0].Text != "" || saved.Steps[0].Status != "retry_wait" {
		t.Fatalf("failed preview was retained or retry missing: %+v %v", saved, err)
	}
	second, err := repo.BeginConversationModelAttempt(ctx, claim, 0)
	if err != nil || second.Number != 2 {
		t.Fatal("retry begin", second, err)
	}
	result := agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "done"}, FinishReason: "stop", Model: "served", Usage: map[string]any{"input_tokens": 8, "output_tokens": 2}}
	if err = repo.CompleteExecutionStep(ctx, claim, 0, result); err != nil {
		t.Fatal(err)
	}
	saved, err = repo.Run(ctx, conversation.ID, run.ID, authority)
	if err != nil || len(saved.ModelAttempts) != 2 || saved.ModelAttempts[1].Status != "completed" || saved.Steps[0].Text != "done" {
		t.Fatalf("completion not projected: %+v %v", saved, err)
	}
}

func TestConversationTextAttemptFailureClearsDurableDraft(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	ctx, authority := t.Context(), conversationTestAuthority()
	conversation, _ := repo.Create(ctx, agentsdk.ConversationCreate{ClientID: "text-attempt", Title: "Text retry"}, authority)
	run, _ := repo.Enqueue(ctx, conversation.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "reply"}, authority)
	claim, ok, err := repo.Claim(ctx, authority.RuntimeID, "text-worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	attempt, err := repo.BeginConversationModelAttempt(ctx, claim, -1)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.AppendDelta(ctx, claim, 0, "failed fragment"); err != nil {
		t.Fatal(err)
	}
	if err = repo.FailConversationModelAttempt(ctx, claim, -1, attempt.Number, agentsdk.ConversationModelFailureDetails{ErrorCode: "provider_network"}, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.Run(ctx, conversation.ID, run.ID, authority)
	if err != nil || saved.DraftText != "" || saved.DraftBytes != 0 || len(saved.ModelAttempts) != 1 || saved.ModelAttempts[0].Status != "failed" {
		t.Fatalf("draft was not cleared: %+v %v", saved, err)
	}
}

func TestTerminalModelFailureProjectsRecoverableInterruptedStep(t *testing.T) {
	run := agentsdk.ConversationRun{Attempt: 1, Steps: []agentsdk.ConversationStepView{{Number: 0, Attempt: 1, Status: "failed", LastModelError: "provider_failed", Calls: []agentsdk.ConversationToolView{{ID: "saved", Status: "completed"}}}}}
	if err := projectConversationExecutionEvent(&run, "run.failed", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if run.Steps[0].Status != "interrupted" || run.Steps[0].LastModelError != "provider_failed" || run.Steps[0].Calls[0].Status != "completed" {
		t.Fatalf("terminal model failure hid its recovery state or saved effect: %+v", run.Steps[0])
	}
}
