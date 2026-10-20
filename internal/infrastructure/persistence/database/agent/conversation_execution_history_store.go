package agent

import (
	"context"
	"database/sql"

	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

// ExecutionHistory validates the current owner/lease/fence once. Both queries
// read hot result/receipt rows directly; neither joins input snapshots/catalogs
// nor asks the database to parse any JSON.
func (s *ConversationStore) ExecutionHistory(ctx context.Context, claim persistence.ConversationClaim, before int) ([]persistence.ConversationExecutionStep, []persistence.ConversationToolExecution, error) {
	if before < 0 || before > 256 {
		return nil, nil, conversationError("bad_request", "step_invalid")
	}
	steps := []persistence.ConversationExecutionStep{}
	tools := []persistence.ConversationToolExecution{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		for _, table := range []string{conversationRunStepTable, conversationToolCallTable} {
			statement, args, err := s.executionHistoryStatement(claim, before, table)
			if err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, statement, args...)
			if err != nil {
				return err
			}
			readErr := func() error {
				defer rows.Close()
				for rows.Next() {
					var number int
					var raw []byte
					if err := rows.Scan(&number, &raw); err != nil {
						return err
					}
					if table == conversationRunStepTable {
						var value executionStepHeader
						if err := unmarshalDurableJSON(raw, &value); err != nil {
							return err
						}
						if value.Number != number {
							return conversationError("conflict", "step_input_conflict")
						}
						steps = append(steps, persistence.ConversationExecutionStep{Number: number, Result: value.Result, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
					} else {
						var value persistence.ConversationToolExecution
						if err := unmarshalDurableJSON(raw, &value); err != nil {
							return err
						}
						if value.Step != number {
							return conversationError("conflict", "tool_result_invalid")
						}
						tools = append(tools, value)
					}
				}
				return rows.Err()
			}()
			if readErr != nil {
				return readErr
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return steps, tools, nil
}

func (s *ConversationStore) executionHistoryStatement(claim persistence.ConversationClaim, before int, table string) (string, []any, error) {
	if table != conversationRunStepTable && table != conversationToolCallTable {
		return "", nil, conversationError("bad_request", "execution_kind_invalid")
	}
	return query.NewSelectBuilder(s.store.Renderer(), table).Columns("step_no", "payload_json").Where(query.And(
		conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID), query.LessThan("step_no", before),
	)).OrderBy(query.Ascending("step_no")).Build()
}
