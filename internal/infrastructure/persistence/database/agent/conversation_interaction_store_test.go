package agent

import (
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
)

func interactionFixture(t *testing.T, repo *ConversationStore, key string) persistence.ConversationClaim {
	t.Helper()
	a := conversationTestAuthority()
	c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: key}, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "input", Message: "Create one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.Claim(t.Context(), a.RuntimeID, "worker", time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	in := executionStoreInput()
	if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &in); err != nil {
		t.Fatal(err)
	}
	if err = repo.CompleteExecutionStep(t.Context(), claim, 0, agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "call", Name: "create_thing", Arguments: `{"name":"one"}`}}}, FinishReason: "tool_calls"}); err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestConversationInteractionWaitFencesWorkerAndResponseIsDurableAndIdempotent(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewConversationStore(store)
	claim := interactionFixture(t, repo, "confirm")
	ctx, a := t.Context(), claim.Authority
	i, err := repo.WaitExecution(ctx, claim, persistence.ConversationWait{Step: 0, CallID: "call", Kind: "confirmation", Question: "创建此事项？"})
	if err != nil {
		t.Fatal(err)
	}
	if i.Arguments != `{"name":"one"}` || i.DefinitionHash == "" || i.ArgumentsHash == "" || i.ExpiresAt.IsZero() {
		t.Fatal("missing frozen confirmation binding")
	}
	if _, found, err := repo.Claim(ctx, a.RuntimeID, "second", time.Minute); err != nil || found {
		t.Fatal("waiting run consumed worker", err)
	}
	requireConversationCode(t, repo.AppendDelta(ctx, claim, 0, "late"), "lease_lost")
	_, err = repo.Resume(ctx, claim.Run.ConversationID, claim.Run.ID, a)
	requireConversationCode(t, err, "interaction_response_required")
	repo = NewConversationStore(store)
	snapshot, err := repo.Run(ctx, claim.Run.ConversationID, claim.Run.ID, a)
	if err != nil || snapshot.Status != "waiting_confirmation" || snapshot.Interaction.ID != i.ID || snapshot.Steps[0].Calls[0].Status != "waiting_confirmation" {
		t.Fatal("lost waiting snapshot", err)
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "response", ExpectedRevision: 1, Decision: "approve"}
	other := a
	other.UserID = "other"
	if _, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, response, other); err == nil {
		t.Fatal("cross-user approval")
	}
	queued, err := repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, response, a)
	if err != nil || queued.Status != "queued" || queued.LastInputSeq != 2 || queued.Interaction.Status != "approved" {
		t.Fatalf("response %+v %v", queued, err)
	}
	for n := 0; n < 3; n++ {
		if _, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, response, a); err != nil {
			t.Fatal(err)
		}
	}
	changed := response
	changed.Decision = "reject"
	_, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, changed, a)
	requireConversationCode(t, err, "interaction_response_conflict")
	history, err := repo.Messages(ctx, claim.Run.ConversationID, agentsdk.ConversationMessageQuery{}, a)
	if err != nil || len(history.Items) != 2 || history.Items[1].InteractionID != i.ID {
		t.Fatal("duplicate/lost response message", err)
	}
	current, found, err := repo.Claim(ctx, a.RuntimeID, "resumed", time.Minute)
	if err != nil || !found || current.Fence <= claim.Fence {
		t.Fatal("missing resumed claim", err)
	}
	approval, found, err := repo.ExecutionInteraction(ctx, current, 0, "call", "confirmation")
	if err != nil || !found || approval.Interaction.RespondedBy != a.UserID {
		t.Fatal("receipt lost", err)
	}
	if _, _, err = repo.BeginExecutionTool(ctx, current, 0, "call", agentsdk.ConversationToolAuthorization{Granted: true}); err != nil {
		t.Fatal(err)
	}
	if err = repo.FinishExecutionTool(ctx, current, 0, "call", agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}); err != nil {
		t.Fatal(err)
	}
	reconciliation, err := repo.WaitExecution(ctx, current, persistence.ConversationWait{Step: 0, CallID: "call", Kind: "reconciliation", Question: "核查实际结果"})
	if err != nil || reconciliation.ID == i.ID || !reconciliation.ExpiresAt.IsZero() {
		t.Fatal("reconciliation replaced approval", err)
	}
	if _, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, response, a); err != nil {
		t.Fatal("late duplicate approval after next interaction", err)
	}
	if _, err = repo.Resume(ctx, claim.Run.ConversationID, claim.Run.ID, a); err != nil {
		t.Fatal(err)
	}
	current, _, _ = repo.Claim(ctx, a.RuntimeID, "reconciler", time.Minute)
	approval, found, err = repo.ExecutionInteraction(ctx, current, 0, "call", "confirmation")
	if err != nil || !found || approval.Interaction.Status != "approved" {
		t.Fatal("approval lost during reconciliation", err)
	}
	if err = repo.FinishExecutionTool(ctx, current, 0, "call", agentsdk.ConversationToolResult{Status: "completed", Content: []byte(`{"id":"one"}`)}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Run(ctx, claim.Run.ConversationID, claim.Run.ID, a)
	if err != nil || snapshot.Interaction.Status != "resolved" {
		t.Fatal("reconciliation not resolved", err)
	}
	if snapshot.Metrics.ConfirmationDecisions != 1 {
		t.Fatalf("confirmation decisions=%d", snapshot.Metrics.ConfirmationDecisions)
	}
	confirmation := snapshot.Steps[0].Calls[0].Confirmation
	if confirmation == nil || confirmation.ID != i.ID || confirmation.Status != "approved" || confirmation.RespondedBy != a.UserID || confirmation.RespondedAt == nil {
		t.Fatalf("confirmation audit=%+v", confirmation)
	}
}

func TestConversationInteractionCancellationExpiryAndDeletion(t *testing.T) {
	for _, mode := range []string{"cancel", "expire", "expire_on_response", "reject"} {
		t.Run(mode, func(t *testing.T) {
			store, _ := openAgentStore(t)
			repo := NewConversationStore(store)
			claim := interactionFixture(t, repo, mode)
			ctx, a := t.Context(), claim.Authority
			ttl := time.Minute
			if mode == "expire" || mode == "expire_on_response" {
				ttl = time.Millisecond
			}
			i, err := repo.WaitExecution(ctx, claim, persistence.ConversationWait{Step: 0, CallID: "call", Kind: "confirmation", Question: "确认？", TTL: ttl})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cancel":
				_, err = repo.Cancel(ctx, claim.Run.ConversationID, claim.Run.ID, a)
			case "reject":
				_, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, agentsdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "reject", ExpectedRevision: 1, Decision: "reject"}, a)
			case "expire":
				time.Sleep(5 * time.Millisecond)
				var count int
				count, err = repo.ExpireInteractions(ctx, a.RuntimeID, 10)
				if count != 1 {
					t.Fatalf("expired=%d", count)
				}
			case "expire_on_response":
				time.Sleep(5 * time.Millisecond)
				_, err = repo.RespondExecution(ctx, claim.Run.ConversationID, claim.Run.ID, agentsdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "expired", ExpectedRevision: 1, Decision: "approve"}, a)
				requireConversationCode(t, err, "interaction_expired")
				err = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			run, err := repo.Run(ctx, claim.Run.ConversationID, claim.Run.ID, a)
			if err != nil || !run.Terminal() || run.Interaction.Status == "pending" {
				t.Fatal("interaction remained pending", err)
			}
			_, err = repo.Resume(ctx, claim.Run.ConversationID, claim.Run.ID, a)
			requireConversationCode(t, err, "interaction_closed")
			c, _ := repo.Get(ctx, claim.Run.ConversationID, a)
			if c.ActiveRunID != "" {
				t.Fatal("closed interaction kept conversation busy")
			}
			if err = repo.Delete(ctx, c.ID, c.Revision, a); err != nil {
				t.Fatal(err)
			}
			_, found, err := repo.readInteraction(ctx, store.Database(), claim, 0, "call", "confirmation")
			if err != nil || found {
				t.Fatal("interaction survived conversation deletion", err)
			}
		})
	}
}
