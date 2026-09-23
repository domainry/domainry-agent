package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func sameEffectArguments(a, b string) bool {
	decode := func(raw string) (any, error) {
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		var v any
		err := d.Decode(&v)
		return v, err
	}
	x, e := decode(a)
	if e != nil {
		return false
	}
	y, e := decode(b)
	return e == nil && conversationHash(x) == conversationHash(y)
}
func (s *ConversationStore) ReuseConversationDelegationEffect(ctx context.Context, claim persistence.ConversationClaim, number int, call sdk.ConversationToolCall, definition sdk.ConversationToolDefinition) (sdk.ConversationToolResult, bool, error) {
	var out sdk.ConversationToolResult
	found := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		task := row.Run.BackgroundTask
		if task == nil || task.Handoff == nil || definition.Effect != "write" {
			return nil
		}
		for _, effect := range task.Handoff.Effects {
			if effect.Status != "completed" || effect.Tool != call.Name || !sameEffectArguments(effect.Arguments, call.Arguments) {
				continue
			}
			ref := effect.Reference
			d, err := s.conversationDelegation(ctx, tx, task.DelegationID, claim.Authority)
			if err != nil {
				return err
			}
			originalAuthority, err := s.assignmentRunAuthority(ctx, tx, d, ref.ConversationID, claim.Authority)
			if err != nil {
				return err
			}
			oldRun, err := s.runRow(ctx, tx, ref.ConversationID, ref.RunID, originalAuthority)
			if err != nil {
				return err
			}
			if !oldRun.Run.Terminal() || oldRun.Run.BackgroundTask == nil || oldRun.Run.BackgroundTask.DelegationID != task.DelegationID {
				return conversationError("conflict", "delegation_handoff_changed")
			}
			var original persistence.ConversationToolExecution
			exists, err := s.readExecutionTool(ctx, tx, persistence.ConversationClaim{Authority: originalAuthority, Run: oldRun.Run}, ref.Step, ref.CallID, &original)
			if err != nil {
				return err
			}
			if !exists || original.State != "completed" || original.Result == nil || conversationHash(original.Result) != ref.SHA256 {
				return conversationError("conflict", "delegation_handoff_changed")
			}
			if conversationHash(original.Definition) != conversationHash(definition) {
				return conversationError("conflict", "delegation_effect_changed")
			}
			if !sameEffectArguments(original.Call.Arguments, call.Arguments) {
				return conversationError("conflict", "delegation_handoff_changed")
			}
			var step persistence.ConversationExecutionStep
			exists, err = s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &step)
			if err != nil {
				return err
			}
			if !exists || step.Result == nil || step.Result.FinishReason != "tool_calls" {
				return conversationError("conflict", "step_not_ready")
			}
			selected := false
			frozen := false
			for _, d := range step.Input.Tools {
				frozen = frozen || conversationHash(d) == conversationHash(definition)
			}
			for _, c := range step.Result.Message.ToolCalls {
				if c.ID == call.ID {
					selected = conversationHash(c) == conversationHash(call)
					break
				}
				var previous persistence.ConversationToolExecution
				present, e := s.readExecutionTool(ctx, tx, claim, number, c.ID, &previous)
				if e != nil {
					return e
				}
				if !present || previous.State != "completed" {
					return conversationError("conflict", "previous_tool_incomplete")
				}
			}
			if !selected || !frozen {
				return conversationError("conflict", "tool_input_conflict")
			}
			var existing persistence.ConversationToolExecution
			exists, err = s.readExecutionTool(ctx, tx, claim, number, call.ID, &existing)
			if err != nil {
				return err
			}
			if exists {
				if existing.State != "completed" || existing.Result == nil {
					return conversationError("conflict", "delegation_reconciliation_required")
				}
				if conversationHash(existing.Result) != conversationHash(original.Result) {
					return conversationError("conflict", "tool_result_conflict")
				}
				out = *existing.Result
				found = true
				return nil
			}
			now := time.Now().UTC()
			record := persistence.ConversationToolExecution{Step: number, Call: call, Definition: definition, IdempotencyKey: original.IdempotencyKey, State: "started", LeaseOwner: claim.Owner, Fence: claim.Fence, ReusedFrom: &ref, CreatedAt: now, UpdatedAt: now}
			if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, call.ID, record, true); err != nil {
				return err
			}
			if err = s.finishExecutionToolReceipt(ctx, tx, claim, number, call.ID, *original.Result, row); err != nil {
				return err
			}
			row, err = s.runRow(ctx, tx, claim.Run.ConversationID, claim.Run.ID, claim.Authority)
			if err != nil {
				return err
			}
			if err = s.executionEvent(ctx, tx, row, "tool.receipt.reused", map[string]any{"step": number, "call_id": call.ID, "tool": call.Name, "status": "reused", "reference": ref}); err != nil {
				return err
			}
			out = *original.Result
			found = true
			return nil
		}
		return nil
	})
	return out, found, err
}
