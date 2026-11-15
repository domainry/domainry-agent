package agent

import (
	"context"
	"errors"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"strings"
	"testing"
	"time"
)

func TestAgentToolLedgerRetriesTransientStoreFailures(t *testing.T) {
	transient := errors.New("transient")
	attempts := 0
	err := retryAgentTaskStoreTransient(t.Context(), func(err error) bool {
		return errors.Is(err, transient)
	}, func() error {
		attempts++
		if attempts < 3 {
			return transient
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestAgentToolLedgerDoesNotRetryDeterministicFailures(t *testing.T) {
	deterministic := errors.New("deterministic")
	attempts := 0
	err := retryAgentTaskStoreTransient(t.Context(), func(error) bool { return false }, func() error {
		attempts++
		return deterministic
	})
	if !errors.Is(err, deterministic) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestAgentToolLedgerStopsTransientRetryWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	err := retryAgentTaskStoreTransient(ctx, func(error) bool { return true }, func() error {
		attempts++
		cancel()
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestTaskKnowledgeReceiptSurvivesFencedCommitReplayAndRepositoryReopen(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := NewAgentTaskRunStore(store)
	now := time.Now().UTC()
	run := agentmodel.AgentTaskRun{ID: "knowledge-task", WorkspaceID: "workspace", TaskKey: "knowledge", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "knowledge", MaxAttempts: 1, MaxToolCalls: 2, CreatedAt: now, UpdatedAt: now}
	if _, _, err := repo.Create(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.ClaimNextAgentTaskRunForWorker(t.Context(), agentpersistence.SystemScope{Kind: agentpersistence.AgentSystemScopeKindGlobal, Purpose: "Knowledge receipt test"}, "worker", now, time.Minute)
	if err != nil || !found {
		t.Fatal(err)
	}
	ref, _, err := repo.BeginAgentToolCall(t.Context(), agentpersistence.AgentToolCallStart{WorkspaceID: run.WorkspaceID, TaskRunID: run.ID, Tool: agentsdk.AgentToolKnowledgeSearch, InputHash: "query-hash", Owner: claim.Lease.Owner, FencingToken: claim.Lease.FencingToken, MaxToolCalls: 2, CostUnits: 2, MaxCostUnits: 5})
	if err != nil {
		t.Fatal(err)
	}
	receipt := &agentmodel.AgentTaskKnowledgeEvidence{LibraryID: "library-a", Operation: "search", ScopeSHA256: strings.Repeat("a", 64), Citations: []agentmodel.AgentTaskKnowledgeCitationEvidence{{ID: "citation-a", Provider: "knowledge", DocumentID: "doc-a", DocumentVersion: 4}}}
	finish := agentpersistence.AgentToolCallFinish{WorkspaceID: run.WorkspaceID, TaskRunID: run.ID, CallRef: ref, Status: "executed", Owner: claim.Lease.Owner, FencingToken: claim.Lease.FencingToken, Knowledge: receipt, Evidence: map[string]any{"output_hash": "result-hash"}}
	wrong := finish
	wrong.FencingToken++
	if repo.FinishAgentToolCall(t.Context(), wrong) == nil {
		t.Fatal("stale worker wrote citation evidence")
	}
	if err = repo.FinishAgentToolCall(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	finish.Knowledge.Citations[0].DocumentID = "replay-cannot-replace"
	if err = repo.FinishAgentToolCall(t.Context(), finish); err != nil {
		t.Fatal(err)
	}
	repo = NewAgentTaskRunStore(store)
	saved, found, err := repo.Get(t.Context(), run.WorkspaceID, run.ID)
	if err != nil || !found || len(saved.Evidence.ToolInvocations) != 1 || saved.Evidence.ToolInvocations[0].Knowledge == nil || saved.Evidence.ToolInvocations[0].Knowledge.Citations[0].DocumentID != "doc-a" || saved.Evidence.ToolInvocations[0].OutputHash != "result-hash" {
		t.Fatal("committed reference receipt changed or disappeared", saved.Evidence, err)
	}
}
