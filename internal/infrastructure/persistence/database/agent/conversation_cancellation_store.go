package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// Mark only unresolved writes. A cancellation is not proof that an external
// effect failed, and completed/pending provider receipts remain authoritative.
// The caller commits these events with the cancellation in one transaction.
func (s *ConversationStore) interruptConversationWrites(ctx context.Context, tx *sql.Tx, row *conversationRunRow) error {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_tool_calls").Columns("payload_json").Where(query.And(conversationScope(row.Authority, row.Run.ConversationID), query.Equal("run_id", row.Run.ID))).Build()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	var calls []persistence.ConversationToolExecution
	for rows.Next() {
		var raw []byte
		var call persistence.ConversationToolExecution
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &call)
		}
		if err != nil {
			rows.Close()
			return err
		}
		if call.State == "started" && call.Definition.Effect == "write" {
			calls = append(calls, call)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	claim := persistence.ConversationClaim{Authority: row.Authority, Run: row.Run, Owner: row.Owner, Fence: row.Fence}
	for _, call := range calls {
		call.State = "uncertain"
		call.Result = &agentsdk.ConversationToolResult{Status: "uncertain", ErrorCode: "external_result_unknown"}
		call.UpdatedAt = time.Now().UTC()
		if err = s.executionWrite(ctx, tx, "_agent_conversation_tool_calls", claim, call.Step, call.Call.ID, call, false); err != nil {
			return err
		}
		if err = s.event(ctx, tx, row, "tool.uncertain", map[string]any{"step": call.Step, "call_id": call.Call.ID, "tool": call.Call.Name, "status": "uncertain", "error_code": "external_result_unknown", "attempt": row.Run.Attempt}); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) cancelledToolReceiptRow(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, number int, callID string) (conversationRunRow, error) {
	row, err := s.runRow(ctx, tx, claim.Run.ConversationID, claim.Run.ID, claim.Authority)
	if err != nil {
		return row, err
	}
	if row.Run.Status != "cancelled" || row.Owner != "" || claim.Fence < 1 || row.Fence-1 != claim.Fence || claim.Owner == "" {
		return row, conversationError("conflict", "lease_lost")
	}
	var call persistence.ConversationToolExecution
	found, err := s.readExecutionTool(ctx, tx, claim, number, callID, &call)
	if err != nil {
		return row, err
	}
	if !found || call.LeaseOwner != claim.Owner || call.Fence != claim.Fence {
		return row, conversationError("conflict", "lease_lost")
	}
	return row, nil
}
