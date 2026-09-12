package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func executionScope(claim persistence.ConversationClaim, number int) query.Predicate {
	return query.And(conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID), query.Equal("step_no", number))
}

func (s *ConversationStore) executionRead(ctx context.Context, db conversationDB, table string, predicate query.Predicate, out any) (bool, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), table).Columns("payload_json").Where(predicate).Build()
	if err != nil {
		return false, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, q, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}

func (s *ConversationStore) executionWrite(ctx context.Context, tx *sql.Tx, table string, claim persistence.ConversationClaim, number int, callID string, payload any, insert bool) error {
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > 4*1024*1024 {
		return conversationError("bad_request", "execution_payload_invalid")
	}
	p := executionScope(claim, number)
	columns := []string{"owner_key", "conversation_id", "run_id", "step_no", "payload_json"}
	values := []any{conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, number, raw}
	if callID != "" {
		p = query.And(p, query.Equal("call_key", conversationHash(callID)))
		columns = append(columns, "call_key")
		values = append(values, conversationHash(callID))
	}
	if insert {
		q, args, e := query.NewInsertBuilder(s.store.Renderer(), table).Columns(columns...).Values(values...).Build()
		return conversationExec(ctx, tx, q, args, e)
	}
	q, args, e := query.NewUpdateBuilder(s.store.Renderer(), table).Set("payload_json", raw).Where(p).Build()
	return conversationCAS(ctx, tx, q, args, e)
}

func (s *ConversationStore) executionEvent(ctx context.Context, tx *sql.Tx, row conversationRunRow, kind string, data map[string]any) error {
	old := row
	data["attempt"] = row.Run.Attempt
	if err := s.event(ctx, tx, &row, kind, data); err != nil {
		return err
	}
	return s.saveRun(ctx, tx, row, old)
}

func (s *ConversationStore) ExecutionStep(ctx context.Context, claim persistence.ConversationClaim, number int, input *agentsdk.ConversationStepRequest) (persistence.ConversationExecutionStep, bool, error) {
	var out persistence.ConversationExecutionStep
	found := false
	if number < 0 || number >= 256 {
		return out, false, conversationError("bad_request", "step_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		found, err = s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, number), &out)
		if err != nil {
			return err
		}
		if found {
			if input != nil && conversationHash(*input) != conversationHash(out.Input) {
				return conversationError("conflict", "step_input_conflict")
			}
			return nil
		}
		if input == nil {
			return nil
		}
		if len(input.Messages) == 0 || input.ModelIdentity.Fingerprint == "" || input.IdempotencyKey == "" || input.MaxToolCalls < 1 || input.MaxToolCalls > 64 || input.MaxOutputBytes < 1 || input.MaxArgumentBytes < 1 {
			return conversationError("bad_request", "step_input_invalid")
		}
		if number > 0 {
			var previous persistence.ConversationExecutionStep
			exists, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, number-1), &previous)
			if err != nil {
				return err
			}
			if !exists || previous.Result == nil || previous.Result.FinishReason != "tool_calls" {
				return conversationError("conflict", "previous_step_incomplete")
			}
			for _, call := range previous.Result.Message.ToolCalls {
				var execution persistence.ConversationToolExecution
				exists, err = s.readExecutionTool(ctx, tx, claim, number-1, call.ID, &execution)
				if err != nil {
					return err
				}
				if !exists || execution.State != "completed" {
					return conversationError("conflict", "previous_tools_incomplete")
				}
			}
		}
		now := time.Now().UTC()
		out = persistence.ConversationExecutionStep{Number: number, Input: *input, CreatedAt: now, UpdatedAt: now}
		if err = s.executionWrite(ctx, tx, "_agent_conversation_steps", claim, number, "", out, true); err != nil {
			return err
		}
		found = true
		old := row
		if info := input.Compaction; info != nil {
			if err = s.event(ctx, tx, &row, "context.tools_compacted", map[string]any{"step": number, "version": info.Version, "results": info.Results, "before_bytes": info.BeforeBytes, "after_bytes": info.AfterBytes}); err != nil {
				return err
			}
		}
		if err = s.event(ctx, tx, &row, "step.started", map[string]any{"step": number, "model": input.ModelIdentity.Model, "attempt": row.Run.Attempt}); err != nil {
			return err
		}
		return s.saveRun(ctx, tx, row, old)
	})
	return out, found, err
}

func validateExecutionResult(step persistence.ConversationExecutionStep, result agentsdk.ConversationStepResult) error {
	message := result.Message
	if message.Role != "assistant" || message.ToolCallID != "" || message.IsError || message.ResultReference != nil || !executionText(message.Content, step.Input.MaxOutputBytes, false) || len(message.ProviderState) > 0 && !json.Valid(message.ProviderState) {
		return conversationError("bad_request", "step_result_invalid")
	}
	if result.FinishReason != "stop" && result.FinishReason != "tool_calls" || (result.FinishReason == "tool_calls") != (len(message.ToolCalls) > 0) || len(message.ToolCalls) == 0 && strings.TrimSpace(message.Content) == "" || len(message.ToolCalls) > step.Input.MaxToolCalls {
		return conversationError("bad_request", "step_result_invalid")
	}
	allowed := map[string]bool{}
	for _, def := range step.Input.Tools {
		allowed[def.Key] = true
	}
	seen := map[string]bool{}
	size := 0
	for _, call := range message.ToolCalls {
		var args map[string]json.RawMessage
		size += len(call.Arguments)
		if !executionText(call.ID, 256, true) || !allowed[call.Name] || seen[call.ID] || json.Unmarshal([]byte(call.Arguments), &args) != nil || args == nil || size > step.Input.MaxArgumentBytes {
			return conversationError("bad_request", "step_result_invalid")
		}
		seen[call.ID] = true
	}
	return nil
}

func (s *ConversationStore) CompleteExecutionStep(ctx context.Context, claim persistence.ConversationClaim, number int, result agentsdk.ConversationStepResult) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, number), &step)
		if err != nil {
			return err
		}
		if !found {
			return conversationError("not_found", "step_not_found")
		}
		if step.Result != nil {
			if conversationHash(step.Result) != conversationHash(result) {
				return conversationError("conflict", "step_result_conflict")
			}
			return nil
		}
		if err = validateExecutionResult(step, result); err != nil {
			return err
		}
		step.Result = &result
		step.UpdatedAt = time.Now().UTC()
		if err = s.executionWrite(ctx, tx, "_agent_conversation_steps", claim, number, "", step, false); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "step.completed", map[string]any{"step": number, "finish_reason": result.FinishReason, "tool_calls": len(result.Message.ToolCalls), "calls": result.Message.ToolCalls, "text": result.Message.Content, "model": result.Model, "usage": result.Usage})
	})
}

func (s *ConversationStore) readExecutionTool(ctx context.Context, db conversationDB, claim persistence.ConversationClaim, number int, callID string, out *persistence.ConversationToolExecution) (bool, error) {
	return s.executionRead(ctx, db, "_agent_conversation_tool_calls", query.And(executionScope(claim, number), query.Equal("call_key", conversationHash(callID))), out)
}

func (s *ConversationStore) ExecutionTools(ctx context.Context, claim persistence.ConversationClaim, number int) ([]persistence.ConversationToolExecution, error) {
	out := []persistence.ConversationToolExecution{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, number), &step)
		if err != nil {
			return err
		}
		if !found {
			return conversationError("not_found", "step_not_found")
		}
		if step.Result == nil {
			return nil
		}
		for _, call := range step.Result.Message.ToolCalls {
			var execution persistence.ConversationToolExecution
			found, err = s.readExecutionTool(ctx, tx, claim, number, call.ID, &execution)
			if err != nil {
				return err
			}
			if found {
				out = append(out, execution)
			}
		}
		return nil
	})
	return out, err
}

func (s *ConversationStore) BeginExecutionTool(ctx context.Context, claim persistence.ConversationClaim, number int, callID string, authorization agentsdk.ConversationToolAuthorization) (persistence.ConversationToolExecution, bool, error) {
	var out persistence.ConversationToolExecution
	replayed := false
	if !authorization.Granted || authorization.ConfirmationRequired {
		return out, false, conversationError("forbidden", "tool_not_authorized")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, number), &step)
		if err != nil {
			return err
		}
		if !found || step.Result == nil || step.Result.FinishReason != "tool_calls" {
			return conversationError("conflict", "step_not_ready")
		}
		var selected *agentsdk.ConversationToolCall
		for _, call := range step.Result.Message.ToolCalls {
			if call.ID == callID {
				selected = &call
				break
			}
			var previous persistence.ConversationToolExecution
			found, err = s.readExecutionTool(ctx, tx, claim, number, call.ID, &previous)
			if err != nil {
				return err
			}
			if !found || previous.State != "completed" {
				return conversationError("conflict", "previous_tool_incomplete")
			}
		}
		if selected == nil {
			return conversationError("not_found", "tool_call_not_found")
		}
		found, err = s.readExecutionTool(ctx, tx, claim, number, callID, &out)
		if err != nil {
			return err
		}
		if found {
			replayed = true
			if out.State != "completed" {
				out.LeaseOwner, out.Fence = claim.Owner, claim.Fence
				if err = s.executionWrite(ctx, tx, "_agent_conversation_tool_calls", claim, number, callID, out, false); err != nil {
					return err
				}
				return s.executionEvent(ctx, tx, row, "tool.started", map[string]any{"step": number, "call_id": callID, "tool": selected.Name, "version": out.Definition.Version, "effect": out.Definition.Effect})
			}
			return nil
		}
		var definition agentsdk.ConversationToolDefinition
		for _, def := range step.Input.Tools {
			if def.Key == selected.Name {
				definition = def
				break
			}
		}
		if definition.Key == "" || definition.Version == "" {
			return conversationError("conflict", "tool_definition_missing")
		}
		now := time.Now().UTC()
		out = persistence.ConversationToolExecution{Step: number, Call: *selected, Definition: definition, IdempotencyKey: "conversation-tool:" + conversationHash([]any{conversationOwner(claim.Authority), claim.Run.ID, number, callID}), Authorization: authorization, State: "started", CreatedAt: now, UpdatedAt: now}
		out.LeaseOwner, out.Fence = claim.Owner, claim.Fence
		if err = s.executionWrite(ctx, tx, "_agent_conversation_tool_calls", claim, number, callID, out, true); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "tool.started", map[string]any{"step": number, "call_id": callID, "tool": selected.Name, "version": definition.Version, "effect": definition.Effect})
	})
	return out, replayed, err
}

func (s *ConversationStore) FinishExecutionTool(ctx context.Context, claim persistence.ConversationClaim, number int, callID string, result agentsdk.ConversationToolResult) error {
	if result.Completion != "" && (result.Completion != "accepted" || result.Status != "completed") {
		return conversationError("bad_request", "tool_result_invalid")
	}
	if result.Status != "completed" && result.Status != "failed" && result.Status != "pending" && result.Status != "uncertain" || len(result.Content) > 0 && !json.Valid(result.Content) || result.Status == "failed" && result.ErrorCode == "" {
		return conversationError("bad_request", "tool_result_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			row, err = s.cancelledToolReceiptRow(ctx, tx, claim, number, callID)
			if err != nil {
				return err
			}
		}
		return s.finishExecutionToolReceipt(ctx, tx, claim, number, callID, result, row)
	})
}

func (s *ConversationStore) finishExecutionTool(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, number int, callID string, result agentsdk.ConversationToolResult) error {
	row, err := s.claimed(ctx, tx, claim)
	if err != nil {
		return err
	}
	return s.finishExecutionToolReceipt(ctx, tx, claim, number, callID, result, row)
}

// Only the public receipt method accepts a cancelled run. Transactional local
// mutations keep using finishExecutionTool and its strict live-lease check.
func (s *ConversationStore) finishExecutionToolReceipt(ctx context.Context, tx *sql.Tx, claim persistence.ConversationClaim, number int, callID string, result agentsdk.ConversationToolResult, row conversationRunRow) error {
	var out persistence.ConversationToolExecution
	found, err := s.readExecutionTool(ctx, tx, claim, number, callID, &out)
	if err != nil {
		return err
	}
	if !found {
		return conversationError("not_found", "tool_call_not_found")
	}
	if out.State == "completed" {
		if conversationHash(out.Result) != conversationHash(result) {
			return conversationError("conflict", "tool_result_conflict")
		}
		return nil
	}
	if out.Definition.MaxOutputBytes < 1 || len(result.Content) > out.Definition.MaxOutputBytes {
		return conversationError("bad_request", "tool_result_exceeded")
	}
	if result.Completion != "" && (out.Definition.Effect != "write" || result.Completion != "accepted" || result.Status != "completed") {
		return conversationError("bad_request", "tool_result_invalid")
	}
	out.Result = &result
	out.State = "completed"
	if result.Status == "uncertain" {
		out.State = "uncertain"
	}
	out.UpdatedAt = time.Now().UTC()
	if err = s.executionWrite(ctx, tx, "_agent_conversation_tool_calls", claim, number, callID, out, false); err != nil {
		return err
	}
	old := row
	if i := row.Run.Interaction; out.State == "completed" && i != nil && i.Kind == "reconciliation" && i.Step == number && i.CallID == callID {
		if err = s.closeRunInteraction(ctx, tx, &row, "resolved"); err != nil {
			return err
		}
	}
	var reference *agentsdk.ConversationResultReference
	if out.State == "completed" {
		reference = &agentsdk.ConversationResultReference{ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Step: number, CallID: callID, SHA256: conversationHash(result)}
	}
	preview := historyPrefix(string(result.Content), 2048)
	var citations []agentsdk.ConversationCitation
	if result.Status == "completed" && (out.Call.Name == "knowledge_search" || out.Call.Name == "knowledge_read" || out.Call.Name == "knowledge_extract" || out.Call.Name == "attachment_search" || out.Call.Name == "attachment_read") {
		var evidence agentsdk.ConversationKnowledgeResult
		if json.Unmarshal(result.Content, &evidence) == nil {
			citations = evidence.Citations
		}
	}
	if err = s.event(ctx, tx, &row, "tool."+out.State, map[string]any{"step": number, "call_id": callID, "tool": out.Call.Name, "status": result.Status, "effect": out.Definition.Effect, "completion": result.Completion, "error_code": result.ErrorCode, "resource_id": result.ResourceID, "result_preview": preview, "result_reference": reference, "result_truncated": len(preview) < len(result.Content), "citations": citations, "attempt": row.Run.Attempt}); err != nil {
		return err
	}
	return s.saveRun(ctx, tx, row, old)
}

var _ persistence.ConversationExecutionRepository = (*ConversationStore)(nil)

func executionText(value string, limit int, required bool) bool {
	return limit > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}
