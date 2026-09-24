package agent

import (
	"database/sql"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func peerFixtureWithWorkBudget(t *testing.T, budget sdk.ConversationWorkBudget) (*ConversationStore, sdk.ConversationAuthority, sdk.ConversationDelegation) {
	t.Helper()
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	peer, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "budget-receiver", Name: "Budget reviewer", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	c, err := repo.Create(t.Context(), sdk.ConversationCreate{ClientID: "budget-source"}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Bound the whole review", Deliverable: "Findings", CompletionConditions: []string{"Check totals"}}
	taskBudget := sdk.ConversationTaskBudget{MaxSteps: 6, MaxToolCalls: 6, MaxOutputBytes: 2048, TimeoutSeconds: 60}
	in := persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "budget-delegation", ConversationID: c.ID, AgentID: peer.ID, Purpose: "Bounded review", Brief: brief, Budget: taskBudget, WorkBudget: &budget}, FromAgentID: "default", SourceAgent: sdk.ConversationAgentSnapshot{ID: "default"}, Agent: sdk.ConversationAgentSnapshot{ID: peer.ID, Revision: peer.Revision}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: c.ID, Budget: taskBudget}}
	d, err := repo.CreateConversationDelegation(t.Context(), in, a)
	if err != nil {
		t.Fatal(err)
	}
	return repo, a, d
}

func TestConversationWorkBudgetIsInheritedReservedAndSettledAcrossAgents(t *testing.T) {
	limit := 0.01
	budget := sdk.ConversationWorkBudget{MaxInputTokens: 150, MaxOutputTokens: 80, MaxDurationSeconds: 20, MaxModelCost: &limit, Currency: "CNY"}
	repo, a, root := peerFixtureWithWorkBudget(t, budget)
	other := dependentPeer(t, repo, a, root, "budget-sibling")
	if other.WorkBudget == nil || conversationHash(*other.WorkBudget) != conversationHash(budget) {
		t.Fatalf("whole-work budget not inherited: %+v", other.WorkBudget)
	}

	changed := budget
	changed.MaxInputTokens++
	peer, err := repo.WriteConversationAgent(t.Context(), "", sdk.ConversationAgentWrite{ClientID: "changed-budget-peer", Name: "Changed budget", Instructions: "Review", ModelKey: "default", Enabled: true, MaxConcurrent: 1}, a)
	if err != nil {
		t.Fatal(err)
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Try another allowance", Deliverable: "Findings", CompletionConditions: []string{"Check"}}
	_, err = repo.CreateConversationDelegation(t.Context(), persistence.ConversationDelegationAdmission{Request: sdk.ConversationDelegationCreate{ClientID: "changed-budget", ConversationID: root.SourceConversationID, AgentID: peer.ID, Purpose: "Changed budget", Brief: brief, WorkBudget: &changed}, FromAgentID: root.FromAgentID, SourceAgent: *root.SourceAgent, Agent: sdk.ConversationAgentSnapshot{ID: peer.ID, Revision: peer.Revision}, Task: sdk.ConversationTask{Goal: brief.Goal, SourceConversationID: root.SourceConversationID, Budget: root.Budget}}, a)
	requireConversationCode(t, err, "work_budget_changed")

	claims := make([]persistence.ConversationClaim, 0, 2)
	for range 2 {
		if _, ok, launchErr := repo.LaunchConversationTask(t.Context(), a.RuntimeID); launchErr != nil || !ok {
			t.Fatalf("launch: %v %v", ok, launchErr)
		}
		claim, ok, claimErr := repo.Claim(t.Context(), a.RuntimeID, "budget-worker", time.Minute)
		if claimErr != nil || !ok {
			t.Fatalf("claim: %v %v", ok, claimErr)
		}
		input := executionStoreInput()
		if _, _, stepErr := repo.ExecutionStep(t.Context(), claim, 0, &input); stepErr != nil {
			t.Fatal(stepErr)
		}
		claims = append(claims, claim)
	}
	price := &sdk.ConversationModelPrice{Currency: "CNY", InputPerMillion: 10, OutputPerMillion: 20}
	first := persistence.ConversationWorkStepReservation{InputTokenUpperBound: 100, MaxOutputTokens: 50, MaxDurationMilliseconds: 5000, Price: price}
	if _, err = repo.ReserveConversationWorkStep(t.Context(), claims[0], 0, first); err != nil {
		t.Fatal(err)
	}
	if err = repo.StartConversationWorkStep(t.Context(), claims[0], 0); err != nil {
		t.Fatal(err)
	}
	second := persistence.ConversationWorkStepReservation{InputTokenUpperBound: 60, MaxOutputTokens: 40, MaxDurationMilliseconds: 5000, Price: price}
	_, err = repo.ReserveConversationWorkStep(t.Context(), claims[1], 0, second)
	requireConversationCode(t, err, "work_budget_exhausted")
	result := sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "bounded"}, Usage: map[string]any{"input_tokens": 40, "output_tokens": 10}}
	if err = repo.CompleteExecutionStep(t.Context(), claims[0], 0, result); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ReserveConversationWorkStep(t.Context(), claims[1], 0, second); err != nil {
		t.Fatal(err)
	}
	_, usage, err := repo.ConversationWorkBudget(t.Context(), other.ID, a)
	if err != nil || usage.InputTokens != 40 || usage.OutputTokens != 10 || usage.ModelCalls != 1 || !usage.CostKnown || math.Abs(usage.ModelCost-0.0006) > 1e-12 {
		t.Fatalf("actual shared usage not settled: %+v %v", usage, err)
	}
	if err = repo.ReleaseConversationWorkStep(t.Context(), claims[1], 0); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ReserveConversationWorkStep(t.Context(), claims[1], 0, persistence.ConversationWorkStepReservation{InputTokenUpperBound: 111, MaxOutputTokens: 1, MaxDurationMilliseconds: 1, Price: price})
	requireConversationCode(t, err, "work_budget_exhausted")
}

func TestConversationWorkBudgetSerializesConcurrentReservationsAcrossAgents(t *testing.T) {
	budget := sdk.ConversationWorkBudget{MaxInputTokens: 100, MaxOutputTokens: 20, MaxDurationSeconds: 20}
	repo, a, root := peerFixtureWithWorkBudget(t, budget)
	_ = dependentPeer(t, repo, a, root, "concurrent-budget-sibling")

	claims := make([]persistence.ConversationClaim, 0, 2)
	for range 2 {
		if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
			t.Fatalf("launch: %v %v", ok, err)
		}
		claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "concurrent-budget-worker", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		input := executionStoreInput()
		if _, _, err = repo.ExecutionStep(t.Context(), claim, 0, &input); err != nil {
			t.Fatal(err)
		}
		claims = append(claims, claim)
	}

	start := make(chan struct{})
	errs := make([]error, len(claims))
	var wg sync.WaitGroup
	for i, claim := range claims {
		wg.Add(1)
		go func(index int, current persistence.ConversationClaim) {
			defer wg.Done()
			<-start
			_, errs[index] = repo.ReserveConversationWorkStep(t.Context(), current, 0, persistence.ConversationWorkStepReservation{InputTokenUpperBound: 70, MaxOutputTokens: 10, MaxDurationMilliseconds: 5000})
		}(i, claim)
	}
	close(start)
	wg.Wait()

	succeeded, exhausted := 0, 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		var sdkErr *sdk.Error
		if errors.As(err, &sdkErr) && sdkErr.Code == "agent.conversation.work_budget_exhausted" {
			exhausted++
			continue
		}
		t.Fatalf("unexpected concurrent reservation error: %v", err)
	}
	if succeeded != 1 || exhausted != 1 {
		t.Fatalf("concurrent reservation outcomes: success=%d exhausted=%d errors=%v", succeeded, exhausted, errs)
	}
}

func TestConversationWorkAllocationCountsCallsAndRepeatedArgumentsWithoutReset(t *testing.T) {
	budget := sdk.ConversationWorkBudget{MaxInputTokens: 1000, MaxOutputTokens: 1000, MaxDurationSeconds: 60}
	repo, a, d := peerFixtureWithWorkBudget(t, budget)
	if _, ok, err := repo.LaunchConversationTask(t.Context(), a.RuntimeID); err != nil || !ok {
		t.Fatalf("launch: %v %v", ok, err)
	}
	claim, ok, err := repo.Claim(t.Context(), a.RuntimeID, "diagnostic-worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	result := sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "read", Name: "lookup", Arguments: `{"query":"same"}`}}}, Usage: map[string]any{"input_tokens": 10, "output_tokens": 2}}
	for step := 0; step < 3; step++ {
		reservation := persistence.ConversationWorkStepReservation{InputTokenUpperBound: 20, MaxOutputTokens: 5, MaxDurationMilliseconds: 5000}
		if _, err = repo.ReserveConversationWorkStep(t.Context(), claim, step, reservation); err != nil {
			t.Fatal(err)
		}
		if err = repo.StartConversationWorkStep(t.Context(), claim, step); err != nil {
			t.Fatal(err)
		}
		if err = repo.transaction(t.Context(), func(tx *sql.Tx) error { return repo.completeConversationWorkStep(t.Context(), tx, claim, step, result) }); err != nil {
			t.Fatal(err)
		}
	}
	failure := persistence.ConversationWorkStepReservation{InputTokenUpperBound: 20, MaxOutputTokens: 5, MaxDurationMilliseconds: 5000}
	if _, err = repo.ReserveConversationWorkStep(t.Context(), claim, 3, failure); err != nil {
		t.Fatal(err)
	}
	if err = repo.StartConversationWorkStep(t.Context(), claim, 3); err != nil {
		t.Fatal(err)
	}
	if err = repo.ReleaseConversationWorkStep(t.Context(), claim, 3); err != nil {
		t.Fatal(err)
	}
	allocation, err := repo.ConversationWorkAllocation(t.Context(), d.ID, a)
	if err != nil || allocation.Usage.ModelCalls != 4 || allocation.Usage.ToolCalls != 3 || allocation.Usage.InputTokens != 30 || allocation.Usage.OutputTokens != 6 || allocation.Usage.CostKnown || allocation.Usage.DurationMilliseconds < 4 || allocation.RepeatedToolCalls != 2 {
		t.Fatalf("delegation allocation=%+v err=%v", allocation, err)
	}
	if err = repo.Finish(t.Context(), claim, sdk.ConversationModelResult{Content: "partial diagnostic result"}, ""); err != nil {
		t.Fatal(err)
	}
	d = currentPeer(t, repo, a, d.ID)
	transfer := transferAdmission(t, repo, a, d, "diagnostic-replacement")
	next, err := repo.TransferConversationDelegation(t.Context(), d.ID, transfer, a)
	if err != nil {
		t.Fatal(err)
	}
	afterTransfer, err := repo.ConversationWorkAllocation(t.Context(), next.ID, a)
	if err != nil || conversationHash(afterTransfer) != conversationHash(allocation) {
		t.Fatalf("transfer reset work allocation: before=%+v after=%+v err=%v", allocation, afterTransfer, err)
	}
	_, total, err := repo.ConversationWorkBudget(t.Context(), d.ID, a)
	if err != nil || total.ModelCalls != 4 || total.ToolCalls != 3 || total.InputTokens != 30 || total.OutputTokens != 6 || total.CostKnown {
		t.Fatalf("work aggregate=%+v err=%v", total, err)
	}
}

func TestConversationWorkDelegationLimitIsSharedAcrossWorkspaceUsers(t *testing.T) {
	repo, a, d := peerFixture(t)
	other := a
	other.UserID = "another-collaborator"
	for i := 1; i < 32; i++ {
		err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
			if err := repo.lockConversationWorkspaceCapacity(t.Context(), tx, other); err != nil {
				return err
			}
			_, _, err := repo.admitConversationWorkBudget(t.Context(), tx, d.RootConversationID, nil, other)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err := repo.transaction(t.Context(), func(tx *sql.Tx) error {
		if err := repo.lockConversationWorkspaceCapacity(t.Context(), tx, a); err != nil {
			return err
		}
		_, _, err := repo.admitConversationWorkBudget(t.Context(), tx, d.RootConversationID, nil, a)
		return err
	})
	requireConversationCode(t, err, "delegation_limit")
}
