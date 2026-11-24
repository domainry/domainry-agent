package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
)

type observedToolLedgerDB struct {
	*sql.DB
	started chan struct{}
	once    sync.Once
}

func (s *observedToolLedgerDB) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, options)
	s.once.Do(func() { close(s.started) })
	return tx, err
}

func TestSQLiteToolLedgerWaitsForWriterAndChargesOnce(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "tool-ledger.db")
	store, db := openAgentStoreWithDSN(t, "file:"+databasePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)", 4)
	repository := NewAgentTaskRunStore(store)
	now := time.Now().UTC()
	const workspace, runID, worker = "workspace-a", "tool-ledger", "worker"
	for _, id := range []string{runID, "other-writer"} {
		run := agentmodel.AgentTaskRun{ID: id, WorkspaceID: workspace, TaskKey: "review", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: id, MaxAttempts: 2, CreatedAt: now, UpdatedAt: now}
		if _, _, err := repository.Create(t.Context(), run); err != nil {
			t.Fatal(err)
		}
	}
	claim, found, err := repository.ClaimAgentTaskRun(t.Context(), workspace, runID, worker, now, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim found=%v error=%v", found, err)
	}
	start := agentpersistence.AgentToolCallStart{
		WorkspaceID: workspace, TaskRunID: runID, Tool: "get_record", InputHash: "synthetic-input",
		Owner: worker, FencingToken: claim.Lease.FencingToken, MaxToolCalls: 1, CostUnits: 2, MaxCostUnits: 2,
	}
	var callRef string
	var count int
	withCompetingToolLedgerWrite(t, repository, db, func() error {
		var err error
		callRef, count, err = repository.BeginAgentToolCall(t.Context(), start)
		return err
	})
	if callRef == "" || count != 1 {
		t.Fatalf("call=%q count=%d", callRef, count)
	}
	finish := agentpersistence.AgentToolCallFinish{WorkspaceID: workspace, TaskRunID: runID, CallRef: callRef, Status: "executed", Owner: worker, FencingToken: claim.Lease.FencingToken}
	withCompetingToolLedgerWrite(t, repository, db, func() error {
		return repository.FinishAgentToolCall(t.Context(), finish)
	})
	if err := repository.FinishAgentToolCall(t.Context(), finish); err != nil {
		t.Fatalf("replay finish: %v", err)
	}
	if _, _, err := repository.BeginAgentToolCall(t.Context(), start); err == nil || err.Error() != "agent.task.tool_call_limit" {
		t.Fatalf("begin beyond tool budget: %v", err)
	}
	stale := start
	stale.FencingToken++
	if _, _, err := repository.BeginAgentToolCall(t.Context(), stale); err == nil || err.Error() != "agent.task.tool_fence_rejected" {
		t.Fatalf("begin under a different fence: %v", err)
	}
	stored, found, err := repository.Get(t.Context(), workspace, runID)
	if err != nil || !found {
		t.Fatalf("stored task found=%v error=%v", found, err)
	}
	invocations := stored.Evidence.ToolInvocations
	if stored.ToolCallCount != 1 || len(invocations) != 1 || invocations[0].Ref != callRef || invocations[0].Status != "executed" || invocations[0].CostUnits != 2 {
		t.Fatalf("tool ledger count=%d invocations=%+v", stored.ToolCallCount, invocations)
	}
}

func withCompetingToolLedgerWrite(t *testing.T, repository *AgentTaskRunStore, db *sql.DB, operation func() error) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), "UPDATE _agent_runs SET updated_at=updated_at+1 WHERE workspace_id=? AND run_id=?", "workspace-a", "other-writer"); err != nil {
		t.Fatal(err)
	}
	observed := &observedToolLedgerDB{DB: db, started: make(chan struct{})}
	repository.db = observed
	defer func() { repository.db = db }()
	finished := make(chan error, 1)
	go func() { finished <- operation() }()
	<-observed.started
	// Keep the writer longer than the ledger's full snapshot-retry backoff.
	select {
	case err := <-finished:
		t.Fatalf("ledger returned before the writer released its transaction: %v", err)
	case <-time.After(650 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		<-finished
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatalf("ledger after write contention: %v", err)
	}
}
