package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

const (
	conversationRunStepTable       = "_agent_run_steps"
	conversationRunStepKindStep    = "step"
	conversationRunStepKindTool    = "tool_call"
	conversationRunStepKindSources = "step_sources"
)

func validConversationStepContext(input *agentsdk.ConversationStepRequest) bool {
	if input.ContextWindow != nil {
		window := input.ContextWindow
		if window.LimitBytes < 4096 || window.InputBytes < 1 || window.InputBytes > window.LimitBytes || window.PressurePermille < 0 || window.PressurePermille > 1000 || window.PressurePermille != min(1000, window.InputBytes*1000/window.LimitBytes) {
			return false
		}
	}
	if input.Compaction != nil {
		value := input.Compaction
		if value.Version != 1 || value.Results < 0 || value.Intervals < 0 || value.Results+value.Intervals < 1 || value.BeforeBytes < 1 || value.AfterBytes < 1 || value.AfterBytes >= value.BeforeBytes {
			return false
		}
	}
	if input.Context == nil {
		for _, message := range input.Messages {
			if message.ContextSourceKey != "" {
				return false
			}
		}
		return true
	}
	manifest := input.Context
	if manifest.Version != 1 || len(manifest.Sources) < 1 || len(manifest.Sources) > 32 || len(manifest.Changes) > 64 || manifest.RefreshedAt.IsZero() || len(manifest.StablePrefixHash) != 64 || len(manifest.DynamicHash) != 64 || input.ContextWindow == nil {
		return false
	}
	seen := map[string]bool{}
	for _, reference := range manifest.Sources {
		if !personalMemoryKey(reference.Key) || seen[reference.Key] || reference.MessageIndex < 0 || reference.MessageIndex >= len(input.Messages) || input.Messages[reference.MessageIndex].ContextSourceKey != reference.Key || conversationHash(input.Messages[reference.MessageIndex].Content) != reference.MessageHash || len(reference.DefinitionHash) != 64 || len(reference.ContentHash) != 64 || len(reference.MessageHash) != 64 || strings.TrimSpace(reference.Version) == "" || len(reference.Version) > 256 || reference.UpdatedAt.IsZero() || len(reference.Sources) > 64 {
			return false
		}
		seen[reference.Key] = true
	}
	for _, change := range manifest.Changes {
		if !seen[change.Key] || strings.TrimSpace(change.PreviousVersion) == "" || strings.TrimSpace(change.CurrentVersion) == "" || change.PreviousVersion == change.CurrentVersion || change.ChangedAt.IsZero() {
			return false
		}
	}
	return true
}

func conversationContextView(window *agentsdk.ConversationContextWindow, manifest *agentsdk.ConversationContextManifest, compaction *agentsdk.ConversationContextCompaction) *agentsdk.ConversationContextView {
	view := &agentsdk.ConversationContextView{Window: window, Compaction: compaction}
	if manifest != nil {
		for _, source := range manifest.Sources {
			view.Sources = append(view.Sources, agentsdk.ConversationContextSourceView{Key: source.Key, Kind: source.Kind, Scope: source.Scope, Refresh: source.Refresh, Version: source.Version, Order: source.Order, StablePrefix: source.StablePrefix, UpdatedAt: source.UpdatedAt})
		}
		view.Changes = append([]agentsdk.ConversationContextSourceChange(nil), manifest.Changes...)
	}
	if view.Window == nil && view.Compaction == nil && len(view.Sources) == 0 {
		return nil
	}
	return view
}

func executionScope(claim persistence.ConversationClaim, number int) query.Predicate {
	return query.And(conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID), query.Equal("step_no", number))
}

func conversationRunStepKindPredicate(kind string) query.Predicate {
	return query.Equal("record_kind", kind)
}

func (s *ConversationStore) payloadRead(ctx context.Context, db conversationDB, table string, predicate query.Predicate, out any) (bool, error) {
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
	return true, unmarshalDurableJSON(raw, out)
}

func (s *ConversationStore) executionRead(ctx context.Context, db conversationDB, kind string, predicate query.Predicate, out any) (bool, error) {
	return s.payloadRead(ctx, db, conversationRunStepTable, query.And(conversationRunStepKindPredicate(kind), predicate), out)
}

func (s *ConversationStore) executionWrite(ctx context.Context, tx *sql.Tx, kind string, claim persistence.ConversationClaim, number int, callID string, payload any, insert bool) error {
	raw, err := marshalDurableJSON(payload)
	if err != nil || len(raw) > 4*1024*1024 {
		return conversationError("bad_request", "execution_payload_invalid")
	}
	callKey := kind
	if callID != "" {
		callKey = conversationHash(callID)
	}
	p := query.And(conversationRunStepKindPredicate(kind), executionScope(claim, number), query.Equal("call_key", callKey))
	columns := []string{"owner_key", "conversation_id", "run_id", "record_kind", "step_no", "call_key", "payload_json"}
	values := []any{conversationOwner(claim.Authority), claim.Run.ConversationID, claim.Run.ID, kind, number, callKey, raw}
	if insert {
		q, args, e := query.NewInsertBuilder(s.store.Renderer(), conversationRunStepTable).Columns(columns...).Values(values...).Build()
		return conversationExec(ctx, tx, q, args, e)
	}
	q, args, e := query.NewUpdateBuilder(s.store.Renderer(), conversationRunStepTable).Set("payload_json", raw).Where(p).Build()
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
		found, err = s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &out)
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
		if len(input.Messages) == 0 || !validStoredConversationStepMessages(input.Messages, claim.Run.ConversationID) || storedConversationStepMessagesHaveImages(input.Messages) && !input.ModelCapabilities.ImageInput || input.ModelIdentity.Fingerprint == "" || input.IdempotencyKey == "" || input.MaxToolCalls < 1 || input.MaxToolCalls > 64 || input.MaxOutputBytes < 1 || input.MaxArgumentBytes < 1 || input.MaxParallelTools < 0 || input.MaxParallelTools > 16 || !validConversationStepContext(input) {
			return conversationError("bad_request", "step_input_invalid")
		}
		if number > 0 {
			var previous persistence.ConversationExecutionStep
			exists, err := s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number-1), &previous)
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
		if err = s.consumeConversationPeerInbox(ctx, tx, claim, input.InboxMessageIDs, number); err != nil {
			return err
		}
		if err = s.recordAgreementAdoption(ctx, tx, row.Run, claim.Authority); err != nil {
			return err
		}
		out = persistence.ConversationExecutionStep{Number: number, Input: *input, CreatedAt: now, UpdatedAt: now}
		if err = s.executionWrite(ctx, tx, conversationRunStepKindStep, claim, number, "", out, true); err != nil {
			return err
		}
		if len(input.ContextSources) > 64 {
			return conversationError("bad_request", "source_limit_exceeded")
		}
		for _, ref := range input.ContextSources {
			if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || ref.BeforeStep < 0 || ref.BeforeStep > 257 || ref.ConversationID == claim.Run.ConversationID && ref.RunID == claim.Run.ID && (ref.BeforeStep == 0 || ref.BeforeStep > number+1) {
				return conversationError("bad_request", "source_reference_invalid")
			}
		}
		if len(input.ContextSources) > 0 {
			if err = s.executionWrite(ctx, tx, conversationRunStepKindSources, claim, number, "", input.ContextSources, true); err != nil {
				return err
			}
		}
		found = true
		old := row
		if info := input.Compaction; info != nil {
			if err = s.event(ctx, tx, &row, "context.tools_compacted", map[string]any{"step": number, "version": info.Version, "results": info.Results, "intervals": info.Intervals, "before_bytes": info.BeforeBytes, "after_bytes": info.AfterBytes}); err != nil {
				return err
			}
		}
		contextView := conversationContextView(input.ContextWindow, input.Context, input.Compaction)
		if err = s.event(ctx, tx, &row, "step.started", map[string]any{"step": number, "model": input.ModelIdentity.Model, "attempt": row.Run.Attempt, "context": contextView}); err != nil {
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
		if !executionText(call.ID, 256, true) || !allowed[call.Name] || seen[call.ID] || unmarshalDurableJSON([]byte(call.Arguments), &args) != nil || args == nil || size > step.Input.MaxArgumentBytes {
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
		found, err := s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &step)
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
		if row.Run.BackgroundTask != nil && row.Run.BackgroundTask.DelegationID != "" {
			if err = s.lockConversationWorkspaceCapacity(ctx, tx, claim.Authority); err != nil {
				return err
			}
			if err = s.completeConversationWorkStep(ctx, tx, claim, number, result); err != nil {
				return err
			}
		}
		step.Result = &result
		step.UpdatedAt = time.Now().UTC()
		if err = s.executionWrite(ctx, tx, conversationRunStepKindStep, claim, number, "", step, false); err != nil {
			return err
		}
		parallelWidth, parallelCalls := 1, 0
		if width := agentsdk.ConversationParallelReadWidth(step.Input.Tools, result.Message.ToolCalls, step.Input.MaxParallelTools); width > 1 {
			parallelWidth, parallelCalls = width, len(result.Message.ToolCalls)
		}
		return s.executionEvent(ctx, tx, row, "step.completed", map[string]any{"step": number, "finish_reason": result.FinishReason, "tool_calls": len(result.Message.ToolCalls), "calls": result.Message.ToolCalls, "text": result.Message.Content, "model": result.Model, "usage": result.Usage, "parallel_width": parallelWidth, "parallel_calls": parallelCalls})
	})
}

func (s *ConversationStore) readExecutionTool(ctx context.Context, db conversationDB, claim persistence.ConversationClaim, number int, callID string, out *persistence.ConversationToolExecution) (bool, error) {
	// A persisted receipt is a full snapshot. Decoding into a prior receipt
	// merges omitted fields (including a cleared error or completion marker).
	var saved persistence.ConversationToolExecution
	found, err := s.executionRead(ctx, db, conversationRunStepKindTool, query.And(executionScope(claim, number), query.Equal("call_key", conversationHash(callID))), &saved)
	if err == nil {
		*out = saved
	}
	return found, err
}

func (s *ConversationStore) ExecutionTools(ctx context.Context, claim persistence.ConversationClaim, number int) ([]persistence.ConversationToolExecution, error) {
	out := []persistence.ConversationToolExecution{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &step)
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

func (s *ConversationStore) executionSubtools(ctx context.Context, db conversationDB, claim persistence.ConversationClaim, number int, parentCallID string) ([]persistence.ConversationToolExecution, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("payload_json").
		Where(query.And(conversationRunStepKindPredicate(conversationRunStepKindTool), executionScope(claim, number))).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []persistence.ConversationToolExecution{}
	for rows.Next() {
		var raw []byte
		var record persistence.ConversationToolExecution
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = unmarshalDurableJSON(raw, &record); err != nil {
			return nil, err
		}
		if record.ParentCallID == parentCallID && parentCallID != "" {
			out = append(out, record)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DispatchIndex < out[j].DispatchIndex })
	return out, nil
}

func (s *ConversationStore) ExecutionSubtools(ctx context.Context, claim persistence.ConversationClaim, number int, parentCallID string) ([]persistence.ConversationToolExecution, error) {
	if parentCallID == "" {
		return nil, conversationError("bad_request", "parent_call_invalid")
	}
	var out []persistence.ConversationToolExecution
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		var err error
		out, err = s.executionSubtools(ctx, tx, claim, number, parentCallID)
		return err
	})
	return out, err
}

func (s *ConversationStore) executionSubtoolCount(ctx context.Context, db conversationDB, claim persistence.ConversationClaim) (int, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("payload_json").
		Where(query.And(conversationRunStepKindPredicate(conversationRunStepKindTool), conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID))).Build()
	if err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		var record persistence.ConversationToolExecution
		if err = rows.Scan(&raw); err != nil {
			return 0, err
		}
		if err = unmarshalDurableJSON(raw, &record); err != nil {
			return 0, err
		}
		if record.ParentCallID != "" {
			count++
		}
	}
	return count, rows.Err()
}

func (s *ConversationStore) ExecutionSubtoolCount(ctx context.Context, claim persistence.ConversationClaim) (int, error) {
	count := 0
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		var err error
		count, err = s.executionSubtoolCount(ctx, tx, claim)
		return err
	})
	return count, err
}

func (s *ConversationStore) executionTopLevelCallCount(ctx context.Context, db conversationDB, claim persistence.ConversationClaim) (int, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationRunStepTable).Columns("payload_json").
		Where(query.And(conversationRunStepKindPredicate(conversationRunStepKindStep), conversationScope(claim.Authority, claim.Run.ConversationID), query.Equal("run_id", claim.Run.ID))).Build()
	if err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		var step persistence.ConversationExecutionStep
		if err = rows.Scan(&raw); err != nil {
			return 0, err
		}
		if err = unmarshalDurableJSON(raw, &step); err != nil {
			return 0, err
		}
		if step.Result != nil {
			count += len(step.Result.Message.ToolCalls)
		}
	}
	return count, rows.Err()
}

// PrepareExecutionSubtool durably reserves one deterministic run_code dispatch
// before authorization. It never starts the tool and cannot grant execution.
func (s *ConversationStore) PrepareExecutionSubtool(ctx context.Context, claim persistence.ConversationClaim, number int, parentCallID string, dispatchIndex int, call agentsdk.ConversationToolCall, definition agentsdk.ConversationToolDefinition, maxCalls int) (persistence.ConversationToolExecution, bool, error) {
	var out persistence.ConversationToolExecution
	replayed := false
	if parentCallID == "" || dispatchIndex < 0 || dispatchIndex >= 64 || maxCalls < 1 || maxCalls > 64 || !executionText(call.ID, 256, true) || !executionText(call.Name, 64, true) || call.Name != definition.Key || call.Name == agentsdk.ConversationCodeToolKey || len(call.Arguments) > 1024*1024 || !json.Valid([]byte(call.Arguments)) {
		return out, false, conversationError("bad_request", "code_dispatch_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &step)
		if err != nil {
			return err
		}
		if !found || step.Result == nil || step.Result.FinishReason != "tool_calls" {
			return conversationError("conflict", "step_not_ready")
		}
		var parent persistence.ConversationToolExecution
		found, err = s.readExecutionTool(ctx, tx, claim, number, parentCallID, &parent)
		if err != nil {
			return err
		}
		if !found || parent.ParentCallID != "" || parent.Call.Name != agentsdk.ConversationCodeToolKey || parent.State != "started" {
			return conversationError("conflict", "code_parent_not_running")
		}
		frozen := false
		for _, candidate := range step.Input.Tools {
			if candidate.Key == definition.Key && conversationHash(candidate) == conversationHash(definition) {
				frozen = true
				break
			}
		}
		if !frozen {
			return conversationError("conflict", "tool_definition_missing")
		}
		found, err = s.readExecutionTool(ctx, tx, claim, number, call.ID, &out)
		if err != nil {
			return err
		}
		if found {
			replayed = true
			if out.ParentCallID != parentCallID || out.DispatchIndex != dispatchIndex || conversationHash(out.Call) != conversationHash(call) || conversationHash(out.Definition) != conversationHash(definition) {
				return conversationError("conflict", "code_dispatch_changed")
			}
			return nil
		}
		children, err := s.executionSubtools(ctx, tx, claim, number, parentCallID)
		if err != nil {
			return err
		}
		if dispatchIndex != len(children) {
			return conversationError("conflict", "code_dispatch_order")
		}
		topLevel, err := s.executionTopLevelCallCount(ctx, tx, claim)
		if err != nil {
			return err
		}
		nested, err := s.executionSubtoolCount(ctx, tx, claim)
		if err != nil {
			return err
		}
		if topLevel+nested >= maxCalls {
			return conversationError("rate_limited", "execution_limit")
		}
		now := time.Now().UTC()
		out = persistence.ConversationToolExecution{ParentCallID: parentCallID, DispatchIndex: dispatchIndex, Step: number, Call: call, Definition: definition, IdempotencyKey: "conversation-tool:" + conversationHash([]any{conversationOwner(claim.Authority), claim.Run.ID, number, call.ID}), State: "queued", CreatedAt: now, UpdatedAt: now, LeaseOwner: claim.Owner, Fence: claim.Fence}
		if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, call.ID, out, true); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "tool.queued", map[string]any{"step": number, "call_id": call.ID, "tool": call.Name, "arguments": call.Arguments, "parent_call_id": parentCallID, "dispatch_index": dispatchIndex, "effect": definition.Effect})
	})
	return out, replayed, err
}

func (s *ConversationStore) BeginExecutionSubtool(ctx context.Context, claim persistence.ConversationClaim, number int, callID string, authorization agentsdk.ConversationToolAuthorization) (persistence.ConversationToolExecution, bool, error) {
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
		found, err := s.readExecutionTool(ctx, tx, claim, number, callID, &out)
		if err != nil {
			return err
		}
		if !found || out.ParentCallID == "" {
			return conversationError("not_found", "tool_call_not_found")
		}
		replayed = out.State != "queued"
		if out.State == "completed" {
			return nil
		}
		out.Authorization, out.LeaseOwner, out.Fence, out.UpdatedAt = authorization, claim.Owner, claim.Fence, time.Now().UTC()
		if out.State == "queued" {
			out.State = "started"
		}
		if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, callID, out, false); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "tool.started", map[string]any{"step": number, "call_id": callID, "tool": out.Call.Name, "version": out.Definition.Version, "effect": out.Definition.Effect, "parent_call_id": out.ParentCallID, "dispatch_index": out.DispatchIndex})
	})
	return out, replayed, err
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
		found, err := s.executionRead(ctx, tx, conversationRunStepKindStep, executionScope(claim, number), &step)
		if err != nil {
			return err
		}
		if !found || step.Result == nil || step.Result.FinishReason != "tool_calls" {
			return conversationError("conflict", "step_not_ready")
		}
		width := agentsdk.ConversationParallelReadWidth(step.Input.Tools, step.Result.Message.ToolCalls, step.Input.MaxParallelTools)
		selectedIndex := -1
		for index := range step.Result.Message.ToolCalls {
			if step.Result.Message.ToolCalls[index].ID == callID {
				selectedIndex = index
				break
			}
		}
		if selectedIndex < 0 {
			return conversationError("not_found", "tool_call_not_found")
		}
		selected := &step.Result.Message.ToolCalls[selectedIndex]
		parallelBatch := 0
		if width > 1 {
			parallelBatch = selectedIndex / width
		}
		requiredBefore := selectedIndex
		if width > 1 {
			requiredBefore = selectedIndex / width * width
		}
		for index := 0; index < requiredBefore; index++ {
			call := step.Result.Message.ToolCalls[index]
			var previous persistence.ConversationToolExecution
			found, err = s.readExecutionTool(ctx, tx, claim, number, call.ID, &previous)
			if err != nil {
				return err
			}
			if !found || previous.State != "completed" {
				return conversationError("conflict", "previous_tool_incomplete")
			}
		}
		found, err = s.readExecutionTool(ctx, tx, claim, number, callID, &out)
		if err != nil {
			return err
		}
		if found {
			replayed = true
			if out.State != "completed" {
				out.LeaseOwner, out.Fence = claim.Owner, claim.Fence
				if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, callID, out, false); err != nil {
					return err
				}
				return s.executionEvent(ctx, tx, row, "tool.started", map[string]any{"step": number, "call_id": callID, "tool": selected.Name, "version": out.Definition.Version, "effect": out.Definition.Effect, "parallel_width": width, "parallel_batch": parallelBatch})
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
		if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, callID, out, true); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "tool.started", map[string]any{"step": number, "call_id": callID, "tool": selected.Name, "version": definition.Version, "effect": definition.Effect, "parallel_width": width, "parallel_batch": parallelBatch})
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
	if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, number, callID, out, false); err != nil {
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
		if unmarshalDurableJSON(result.Content, &evidence) == nil {
			citations = evidence.Citations
		}
	}
	if err = s.event(ctx, tx, &row, "tool."+out.State, map[string]any{"step": number, "call_id": callID, "tool": out.Call.Name, "status": result.Status, "effect": out.Definition.Effect, "completion": result.Completion, "error_code": result.ErrorCode, "resource_id": result.ResourceID, "result_preview": preview, "result_reference": reference, "result_truncated": len(preview) < len(result.Content), "citations": citations, "attempt": row.Run.Attempt, "parent_call_id": out.ParentCallID, "dispatch_index": out.DispatchIndex}); err != nil {
		return err
	}
	return s.saveRun(ctx, tx, row, old)
}

var _ persistence.ConversationExecutionRepository = (*ConversationStore)(nil)
var _ persistence.ConversationCodeExecutionRepository = (*ConversationStore)(nil)

func executionText(value string, limit int, required bool) bool {
	return limit > 0 && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}
