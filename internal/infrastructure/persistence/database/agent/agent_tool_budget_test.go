package agent

import (
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

func TestTaskBudgetReservationsAreAtomicAndFenced(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewAgentTaskRunStore(store)
	now := time.Now().UTC()
	run := agentmodel.AgentTaskRun{ID: "budget", WorkspaceID: "workspace", TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "budget", MaxAttempts: 1, MaxToolCalls: 4, MaxCostUnits: 5, CreatedAt: now, UpdatedAt: now}
	if _, _, err := repo.Create(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.ClaimNextAgentTaskRunForWorker(t.Context(), persistence.SystemScope{Kind: persistence.AgentSystemScopeKindGlobal, Purpose: "budget test"}, "worker", now, time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	start := persistence.AgentToolCallStart{WorkspaceID: run.WorkspaceID, TaskRunID: run.ID, Tool: "query_records", Owner: claim.Lease.Owner, FencingToken: claim.Lease.FencingToken, MaxToolCalls: 4, MaxCostUnits: 5, CostUnits: 2}
	wrong := start
	wrong.FencingToken++
	if _, _, err := repo.BeginAgentToolCall(t.Context(), wrong); err == nil {
		t.Fatal("foreign worker reserved budget")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := repo.BeginAgentToolCall(t.Context(), start); err == nil {
				successes.Add(1)
			} else {
				var coded *agentsdk.Error
				if !errors.As(err, &coded) || coded.Code != "agent.task.cost_budget_exceeded" {
					t.Errorf("unexpected rejection: %v", err)
				}
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 2 {
		t.Fatalf("cost budget admitted %d concurrent calls", successes.Load())
	}
	// Reopen the repository over the same committed state: the budget is not
	// an in-memory counter that a service restart resets.
	repo = NewAgentTaskRunStore(store)
	if _, count, err := repo.BeginAgentToolCall(t.Context(), start); err == nil || count != 2 {
		t.Fatalf("restart lost durable usage: count=%d err=%v", count, err)
	}
}
