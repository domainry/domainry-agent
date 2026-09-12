package agent

import (
	"context"
	"database/sql"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

const interactionTable = "_agent_conversation_interactions"

func interactionScope(claim persistence.ConversationClaim, step int, callID, kind string) query.Predicate {
	return query.And(executionScope(claim, step), query.Equal("call_key", conversationHash(callID)), query.Equal("kind", kind))
}

func (s *ConversationStore) readInteraction(ctx context.Context, db conversationDB, claim persistence.ConversationClaim, step int, callID, kind string) (persistence.ConversationInteractionRecord, bool, error) {
	var out persistence.ConversationInteractionRecord
	found, err := s.executionRead(ctx, db, interactionTable, interactionScope(claim, step, callID, kind), &out)
	return out, found, err
}

func (s *ConversationStore) writeInteraction(ctx context.Context, tx *sql.Tx, a agentsdk.ConversationAuthority, record persistence.ConversationInteractionRecord, insert bool) error {
	i := record.Interaction
	expires := int64(0)
	if !i.ExpiresAt.IsZero() {
		expires = i.ExpiresAt.UnixMilli()
	}
	if insert {
		q, args, err := query.NewInsertBuilder(s.store.Renderer(), interactionTable).
			Columns("owner_key", "conversation_id", "run_id", "step_no", "call_key", "kind", "interaction_id", "runtime_id", "status", "expires_at", "payload_json").
			Values(conversationOwner(a), i.ConversationID, i.RunID, i.Step, conversationHash(i.CallID), i.Kind, i.ID, a.RuntimeID, i.Status, expires, conversationJSON(record)).Build()
		return conversationExec(ctx, tx, q, args, err)
	}
	q, args, err := query.NewUpdateBuilder(s.store.Renderer(), interactionTable).
		Set("status", i.Status).Set("expires_at", expires).Set("payload_json", conversationJSON(record)).
		Where(query.And(conversationScope(a, i.ConversationID), query.Equal("run_id", i.RunID), query.Equal("interaction_id", i.ID))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) ExecutionInteraction(ctx context.Context, claim persistence.ConversationClaim, step int, callID, kind string) (persistence.ConversationInteractionRecord, bool, error) {
	var out persistence.ConversationInteractionRecord
	var found bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.claimed(ctx, tx, claim); err != nil {
			return err
		}
		var err error
		out, found, err = s.readInteraction(ctx, tx, claim, step, callID, kind)
		return err
	})
	return out, found, err
}

func (s *ConversationStore) WaitExecution(ctx context.Context, claim persistence.ConversationClaim, wait persistence.ConversationWait) (agentsdk.ConversationInteraction, error) {
	var out agentsdk.ConversationInteraction
	if wait.Kind != "input" && wait.Kind != "confirmation" && wait.Kind != "reconciliation" || !executionText(wait.Question, 4096, true) || len(wait.Choices) > 8 || wait.TTL < 0 || wait.TTL > 7*24*time.Hour {
		return out, conversationError("bad_request", "interaction_invalid")
	}
	for _, choice := range wait.Choices {
		if !executionText(choice, 512, true) {
			return out, conversationError("bad_request", "interaction_invalid")
		}
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		old, err := s.claimed(ctx, tx, claim)
		if err != nil {
			return err
		}
		var step persistence.ConversationExecutionStep
		found, err := s.executionRead(ctx, tx, "_agent_conversation_steps", executionScope(claim, wait.Step), &step)
		if err != nil {
			return err
		}
		if !found || step.Result == nil {
			return conversationError("conflict", "step_incomplete")
		}
		var call agentsdk.ConversationToolCall
		for _, candidate := range step.Result.Message.ToolCalls {
			if candidate.ID == wait.CallID {
				call = candidate
				break
			}
			var previous persistence.ConversationToolExecution
			found, err = s.readExecutionTool(ctx, tx, claim, wait.Step, candidate.ID, &previous)
			if err != nil {
				return err
			}
			if !found || previous.State != "completed" {
				return conversationError("conflict", "previous_tool_incomplete")
			}
		}
		if call.ID == "" {
			return conversationError("not_found", "tool_call_not_found")
		}
		var definition agentsdk.ConversationToolDefinition
		for _, candidate := range step.Input.Tools {
			if candidate.Key == call.Name {
				definition = candidate
				break
			}
		}
		if definition.Key == "" {
			return conversationError("conflict", "tool_definition_missing")
		}
		operations, err := frozenConfirmationOperations(step, wait)
		if err != nil {
			return err
		}
		var execution persistence.ConversationToolExecution
		exists, err := s.readExecutionTool(ctx, tx, claim, wait.Step, call.ID, &execution)
		if err != nil {
			return err
		}
		if wait.Kind == "reconciliation" {
			if !exists || execution.State != "uncertain" {
				return conversationError("conflict", "interaction_invalid")
			}
		} else if exists {
			return conversationError("conflict", "tool_already_started")
		}
		record, found, err := s.readInteraction(ctx, tx, claim, wait.Step, call.ID, wait.Kind)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if !found {
			expires := time.Time{}
			if wait.Kind != "reconciliation" {
				ttl := wait.TTL
				if ttl == 0 {
					ttl = 24 * time.Hour
				}
				expires = now.Add(ttl)
			}
			record.Interaction = agentsdk.ConversationInteraction{ID: conversationID("cint_"), ConversationID: claim.Run.ConversationID, RunID: claim.Run.ID, Step: wait.Step, CallID: call.ID, Kind: wait.Kind, Status: "pending", Question: wait.Question, Choices: wait.Choices, Tool: call.Name, ToolVersion: definition.Version, ActionKey: definition.ActionKey, Arguments: call.Arguments, ArgumentsHash: conversationHash(call.Arguments), DefinitionHash: conversationHash(definition), Revision: 1, CreatedAt: now, ExpiresAt: expires}
			record.Interaction.Operations = operations
		} else if wait.Kind == "reconciliation" && record.Interaction.Kind == "reconciliation" && record.Interaction.Status == "cancelled" {
			record.Interaction.Status = "pending"
			record.Interaction.Revision++
		} else if record.Interaction.Kind != wait.Kind || record.Interaction.Status != "pending" {
			return conversationError("conflict", "interaction_closed")
		}
		if err = s.writeInteraction(ctx, tx, claim.Authority, record, !found); err != nil {
			return err
		}
		v := old
		v.Run.Interaction = &record.Interaction
		v.Run.Status = map[string]string{"input": "waiting_user", "confirmation": "waiting_confirmation", "reconciliation": "needs_reconciliation"}[wait.Kind]
		v.Run.ErrorCode = ""
		v.Owner, v.Expires = "", 0
		v.Fence++
		if err = s.event(ctx, tx, &v, "tool.waiting", map[string]any{"step": wait.Step, "call_id": call.ID, "status": v.Run.Status, "effect": definition.Effect, "attempt": v.Run.Attempt}); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "run."+v.Run.Status, map[string]any{"interaction": record.Interaction, "attempt": v.Run.Attempt}); err != nil {
			return err
		}
		out = record.Interaction
		return s.saveRun(ctx, tx, v, old)
	})
	return out, err
}

func (s *ConversationStore) RespondExecution(ctx context.Context, id, runID string, response agentsdk.ConversationInteractionResponse, a agentsdk.ConversationAuthority) (agentsdk.ConversationRun, error) {
	var out agentsdk.ConversationRun
	if response.Scope != "" && (response.Scope != "listed_operations" || response.Decision != "approve") {
		return out, conversationError("bad_request", "interaction_scope_invalid")
	}
	if response.ExpectedRevision < 1 || !executionText(response.ClientID, 96, true) || !executionText(response.InteractionID, 96, true) || !executionText(response.Answer, 16384, false) {
		return out, conversationError("bad_request", "interaction_response_invalid")
	}
	expired := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		expired = false
		c, err := s.get(ctx, tx, id, a)
		if err != nil {
			return err
		}
		old, err := s.runRow(ctx, tx, id, runID, a)
		if err != nil {
			return err
		}
		var record persistence.ConversationInteractionRecord
		found, err := s.executionRead(ctx, tx, interactionTable, query.And(conversationScope(a, id), query.Equal("run_id", runID), query.Equal("interaction_id", response.InteractionID)), &record)
		if err != nil {
			return err
		}
		if !found {
			return conversationError("not_found", "interaction_not_found")
		}
		if record.Response != nil {
			if conversationHash(record.Response) != conversationHash(response) {
				return conversationError("conflict", "interaction_response_conflict")
			}
			out = old.Run
			return nil
		}
		i := &record.Interaction
		if i.Status != "pending" || old.Run.Interaction == nil || old.Run.Interaction.ID != i.ID || !old.Run.Waiting() || old.Run.BackgroundTask == nil && c.ActiveRunID != runID {
			return conversationError("conflict", "interaction_closed")
		}
		if i.Revision != response.ExpectedRevision {
			return conversationError("conflict", "revision_conflict")
		}
		now := time.Now().UTC()
		if !i.ExpiresAt.IsZero() && !i.ExpiresAt.After(now) {
			expired = true
			return s.expireInteraction(ctx, tx, old, c, record)
		}
		if c.Archived {
			return conversationError("conflict", "archived")
		}
		status := ""
		if i.Kind == "input" && response.Decision == "answer" && strings.TrimSpace(response.Answer) != "" {
			status = "answered"
		} else if i.Kind == "confirmation" && response.Answer == "" {
			status = map[string]string{"approve": "approved", "reject": "rejected"}[response.Decision]
		}
		if status == "" {
			return conversationError("bad_request", "interaction_response_invalid")
		}
		i.Status, i.Answer, i.RespondedBy, i.RespondedAt = status, response.Answer, a.UserID, &now
		if response.Scope == "listed_operations" {
			if err = s.approveListedOperations(ctx, tx, old, i); err != nil {
				return err
			}
		}
		i.Revision++
		record.Response = &response
		if err = s.writeInteraction(ctx, tx, a, record, false); err != nil {
			return err
		}
		content := response.Answer
		if i.Kind == "confirmation" {
			content = confirmationResponseMessage(*i, response)
		}
		message := agentsdk.ConversationMessage{ID: conversationID("msg_"), ConversationID: id, RunID: runID, InteractionID: i.ID, Seq: c.LastSeq + 1, Role: "user", Content: content, CreatedAt: now}
		if old.Run.BackgroundTask != nil {
			message.BackgroundTaskID = old.Run.BackgroundTask.TaskID
		}
		if err = s.insertMessage(ctx, tx, message, a); err != nil {
			return err
		}
		v := old
		v.Run.Interaction = i
		v.Authority = a
		v.Run.LastInputSeq = message.Seq
		v.Run.Status, v.Run.ErrorCode = "queued", ""
		v.Run.DraftText, v.Run.DraftBytes = "", 0
		v.Owner, v.Expires = "", 0
		v.Fence++
		c.LastSeq = message.Seq
		if status == "rejected" {
			v.Run.Status, v.Run.ErrorCode = "cancelled", "interaction_rejected"
			if c.ActiveRunID == runID {
				c.ActiveRunID = ""
			}
		}
		if err = s.save(ctx, tx, c, c.Revision, a); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "interaction.responded", map[string]any{"interaction": *i, "message_id": message.ID, "message_seq": message.Seq}); err != nil {
			return err
		}
		if err = s.event(ctx, tx, &v, "run."+v.Run.Status, map[string]any{"attempt": v.Run.Attempt, "draft_reset": true, "error_code": v.Run.ErrorCode}); err != nil {
			return err
		}
		out = v.Run
		return s.saveRun(ctx, tx, v, old)
	})
	if err == nil && expired {
		err = conversationError("conflict", "interaction_expired")
	}
	return out, err
}

func (s *ConversationStore) closeRunInteraction(ctx context.Context, tx *sql.Tx, row *conversationRunRow, status string) error {
	i := row.Run.Interaction
	if i == nil || i.Status != "pending" && !(status == "resolved" && i.Kind == "reconciliation" && i.Status == "cancelled") {
		return nil
	}
	record, found, err := s.readInteraction(ctx, tx, persistence.ConversationClaim{Authority: row.Authority, Run: row.Run}, i.Step, i.CallID, i.Kind)
	if err != nil {
		return err
	}
	if !found || record.Interaction.Status != i.Status {
		return conversationError("conflict", "interaction_closed")
	}
	record.Interaction.Status = status
	record.Interaction.Revision++
	row.Run.Interaction = &record.Interaction
	if err = s.writeInteraction(ctx, tx, row.Authority, record, false); err != nil {
		return err
	}
	return s.event(ctx, tx, row, "interaction."+status, map[string]any{"interaction": record.Interaction})
}

func (s *ConversationStore) expireInteraction(ctx context.Context, tx *sql.Tx, old conversationRunRow, c agentsdk.Conversation, record persistence.ConversationInteractionRecord) error {
	if record.Interaction.Status != "pending" || old.Run.Interaction == nil || old.Run.Interaction.ID != record.Interaction.ID || !old.Run.Waiting() {
		return nil
	}
	v := old
	if err := s.closeRunInteraction(ctx, tx, &v, "expired"); err != nil {
		return err
	}
	v.Run.Status, v.Run.ErrorCode = "failed", "interaction_expired"
	v.Owner, v.Expires = "", 0
	v.Fence++
	if c.ActiveRunID == v.Run.ID {
		c.ActiveRunID = ""
		if err := s.save(ctx, tx, c, c.Revision, v.Authority); err != nil {
			return err
		}
	}
	if err := s.event(ctx, tx, &v, "run.failed", map[string]any{"attempt": v.Run.Attempt, "error_code": v.Run.ErrorCode}); err != nil {
		return err
	}
	return s.saveRun(ctx, tx, v, old)
}

func (s *ConversationStore) ExpireInteractions(ctx context.Context, runtimeID string, limit int) (int, error) {
	if runtimeID == "" || limit < 1 || limit > 100 {
		return 0, conversationError("bad_request", "query_invalid")
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), interactionTable).Columns("owner_key", "conversation_id", "run_id").Where(query.And(query.Equal("runtime_id", runtimeID), query.Equal("status", "pending"), query.GreaterThan("expires_at", 0), query.LessThanOrEqual("expires_at", time.Now().UnixMilli()))).OrderBy(query.Ascending("expires_at")).Limit(limit).Build()
	if err != nil {
		return 0, err
	}
	rows, err := s.store.Database().QueryContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	type candidate struct{ owner, conversation, run string }
	items := []candidate{}
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.owner, &item.conversation, &item.run); err != nil {
			break
		}
		items = append(items, item)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close() // Release SQLite's connection before opening transactions.
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		changed := false
		err = s.transaction(ctx, func(tx *sql.Tx) error {
			changed = false
			q, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns(conversationRunColumns...).Where(query.And(query.Equal("owner_key", item.owner), query.Equal("conversation_id", item.conversation), query.Equal("run_id", item.run), query.Equal("runtime_id", runtimeID))).Build()
			if err != nil {
				return err
			}
			row, err := scanConversationRun(tx.QueryRowContext(ctx, q, args...))
			if err != nil {
				return err
			}
			i := row.Run.Interaction
			if i == nil || i.Status != "pending" || i.ExpiresAt.IsZero() || i.ExpiresAt.After(time.Now()) || !row.Run.Waiting() {
				return nil
			}
			c, err := s.get(ctx, tx, item.conversation, row.Authority)
			if err != nil {
				return err
			}
			record, found, err := s.readInteraction(ctx, tx, persistence.ConversationClaim{Authority: row.Authority, Run: row.Run}, i.Step, i.CallID, i.Kind)
			if err != nil || !found {
				return err
			}
			changed = true
			return s.expireInteraction(ctx, tx, row, c, record)
		})
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

var _ persistence.ConversationInteractionRepository = (*ConversationStore)(nil)
