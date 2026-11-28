package agent

import (
	"context"
	"database/sql"
	"reflect"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-orm/query"
)

// FreezeAgentTaskMemoryContext persists the exact selected item revisions
// before the first model call. The task lease is the write fence; retries read
// the already frozen value and never resolve current memories again.
func (s *AgentTaskRunStore) FreezeAgentTaskMemoryContext(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64, snapshot agentsdk.MemoryContextSnapshot) (agentmodel.AgentTaskRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, err := query.NewWorkspaceSelectBuilder(s.store.Renderer(), agentRunTable, run.WorkspaceID).
		Columns("payload_json").Where(agentTaskLeasePredicate(run.ID, owner, token)).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, read, readArgs...).Scan(&raw); err == sql.ErrNoRows {
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_fence_rejected")
	} else if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	var current agentmodel.AgentTaskRun
	if err = unmarshalDurableJSON(raw, &current); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if current.MemoryContext != nil {
		if current.MemoryContext.ID == snapshot.ID && current.MemoryContext.ContentHash == snapshot.ContentHash {
			return current, tx.Commit()
		}
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_snapshot_conflict")
	}
	now := time.Now().UTC()
	current.MemoryContext = &snapshot
	current.Evidence.Memory = &agentsdk.AgentMemoryEvidence{
		SnapshotID: snapshot.ID, ContentHash: snapshot.ContentHash, ItemCount: len(snapshot.Items),
		Receipts: append([]agentsdk.MemoryContextReceipt(nil), snapshot.Receipts...), Ranking: snapshot.Ranking,
	}
	current.UpdatedAt, current.Revision = now, current.Revision+1
	raw, err = marshalDurableJSON(current)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(s.store.Renderer(), agentRunTable, current.WorkspaceID).
		Set("payload_json", raw).Set("updated_at", now.UnixMilli()).
		Where(agentTaskLeasePredicate(current.ID, owner, token)).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	updated, err := agentTaskExactlyOneRow(result)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if !updated {
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_fence_rejected")
	}
	return current, tx.Commit()
}

func (s *AgentTaskRunStore) SaveAgentTaskMemoryCandidateReceipts(ctx context.Context, run agentmodel.AgentTaskRun, receipts []agentsdk.AgentMemoryCandidateReceipt) (agentmodel.AgentTaskRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	read, readArgs, err := query.NewWorkspaceSelectBuilder(s.store.Renderer(), agentRunTable, run.WorkspaceID).
		Columns("payload_json").Where(query.And(agentRunKindPredicate(agentRunKindTask), query.Equal("run_id", run.ID))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, read, readArgs...).Scan(&raw); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	var current agentmodel.AgentTaskRun
	if err = unmarshalDurableJSON(raw, &current); err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if !current.Status.Terminal() || current.Evidence.Memory == nil || len(current.Evidence.Memory.Candidates) == 0 {
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_candidate_state_invalid")
	}
	if reflect.DeepEqual(current.Evidence.Memory.CandidateReceipts, receipts) {
		return current, tx.Commit()
	}
	if len(current.Evidence.Memory.CandidateReceipts) != 0 && !memoryCandidateReceiptsRetryable(current.Evidence.Memory.CandidateReceipts) || current.Revision != run.Revision {
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_candidate_receipt_conflict")
	}
	previousUpdatedAt := current.UpdatedAt
	current.Evidence.Memory.CandidateReceipts = append([]agentsdk.AgentMemoryCandidateReceipt(nil), receipts...)
	current.UpdatedAt, current.Revision = time.Now().UTC(), current.Revision+1
	raw, err = marshalDurableJSON(current)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(s.store.Renderer(), agentRunTable, current.WorkspaceID).
		Set("memory_candidate_state", memoryCandidateState(current.Evidence.Memory)).Set("payload_json", raw).Set("updated_at", current.UpdatedAt.UnixMilli()).
		Where(query.And(agentRunKindPredicate(agentRunKindTask), query.Equal("run_id", current.ID), query.Equal("status", string(current.Status)), query.Equal("updated_at", previousUpdatedAt.UnixMilli()))).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	updated, err := agentTaskExactlyOneRow(result)
	if err != nil {
		return agentmodel.AgentTaskRun{}, err
	}
	if !updated {
		return agentmodel.AgentTaskRun{}, agentError("conflict", "agent.task.memory_candidate_receipt_conflict")
	}
	return current, tx.Commit()
}

func memoryCandidateReceiptsRetryable(receipts []agentsdk.AgentMemoryCandidateReceipt) bool {
	for _, receipt := range receipts {
		if receipt.Disposition == agentsdk.AgentMemoryCandidateFailed {
			return true
		}
	}
	return false
}
