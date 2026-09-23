package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationStore) BeginConversationOutcomeInspection(ctx context.Context, id string, revision int64, in sdk.ConversationOutcomeInspectionRequest, clientID string, a sdk.ConversationAuthority) (persistence.ConversationOutcomeInspection, error) {
	var out persistence.ConversationOutcomeInspection
	if in.Step < 0 || in.Step >= 256 || !personalMemoryKey(in.RunID) || !executionText(in.CallID, 256, true) || !personalMemoryKey(clientID) {
		return out, conversationError("bad_request", "outcome_inspection_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if err := s.lockConversationWorkspaceCapacity(ctx, tx, a); err != nil {
			return err
		}
		d, err := s.conversationDelegation(ctx, tx, id, a)
		if err != nil {
			return err
		}
		if d.Revision != revision {
			return conversationError("conflict", "revision_conflict")
		}
		row, err := s.runRow(ctx, tx, d.ConversationID, in.RunID, a)
		if err != nil {
			return err
		}
		if row.Run.BackgroundTask == nil || row.Run.BackgroundTask.DelegationID != d.ID {
			return conversationError("forbidden", "delegation_actor_invalid")
		}
		if row.Run.Status != "cancelled" && row.Run.Status != "failed" && row.Run.Status != "needs_reconciliation" {
			return conversationError("conflict", "outcome_inspection_running")
		}
		claim := persistence.ConversationClaim{Run: row.Run, Authority: a}
		var call persistence.ConversationToolExecution
		found, err := s.readExecutionTool(ctx, tx, claim, in.Step, in.CallID, &call)
		if err != nil {
			return err
		}
		if !found || call.Definition.Effect != "write" {
			return conversationError("bad_request", "outcome_inspection_invalid")
		}
		out.Run, out.Record = row.Run, call
		if call.State == "completed" && call.Result != nil {
			return nil
		}
		now := time.Now().UTC()
		if pending := call.Inspection; pending != nil && pending.CompletedAt == nil && pending.ExpiresAt.After(now) {
			if pending.ClientID != clientID {
				return conversationError("conflict", "outcome_inspection_busy")
			}
		} else {
			call.Inspection = &persistence.ConversationToolInspection{Token: conversationID("inspect_"), ClientID: clientID, StartedAt: now, ExpiresAt: now.Add(time.Duration(call.Definition.TimeoutMillis)*time.Millisecond + 30*time.Second), ActorID: a.UserID}
			if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, in.Step, in.CallID, call, false); err != nil {
				return err
			}
			if err = s.executionEvent(ctx, tx, row, "tool.inspection.started", map[string]any{"step": in.Step, "call_id": in.CallID, "actor_id": a.UserID, "status": "reading", "checked_at": now}); err != nil {
				return err
			}
		}
		request := sdk.ConversationToolRequest{Authority: a, ConversationID: row.Run.ConversationID, RunID: row.Run.ID, Step: in.Step, Call: call.Call, Definition: call.Definition, IdempotencyKey: call.IdempotencyKey, OutcomeInspectionToken: call.Inspection.Token}
		approval, exists, err := s.readInteraction(ctx, tx, claim, in.Step, in.CallID, "confirmation")
		if err != nil {
			return err
		}
		if exists {
			i := approval.Interaction
			if i.Status == "approved" && i.RespondedAt != nil && i.RespondedBy == a.UserID && i.ArgumentsHash == conversationHash(call.Call.Arguments) && i.DefinitionHash == conversationHash(call.Definition) {
				request.ConfirmationID = i.ID
				request.Confirmation = &sdk.ConversationConfirmation{ID: i.ID, UserID: a.UserID, ActionKey: i.ActionKey, ToolVersion: i.ToolVersion, ArgumentsHash: i.ArgumentsHash, ApprovedAt: *i.RespondedAt}
			}
		}
		out.Record, out.Request = call, request
		return nil
	})
	return out, err
}

func (s *ConversationStore) inspectionRow(ctx context.Context, db conversationDB, in sdk.ConversationToolRequest) (conversationRunRow, persistence.ConversationToolExecution, error) {
	var call persistence.ConversationToolExecution
	row, err := s.runRow(ctx, db, in.ConversationID, in.RunID, in.Authority)
	if err != nil {
		return row, call, err
	}
	if in.OutcomeInspectionToken == "" || row.Run.Status != "cancelled" && row.Run.Status != "failed" && row.Run.Status != "needs_reconciliation" {
		return row, call, conversationError("conflict", "outcome_inspection_superseded")
	}
	claim := persistence.ConversationClaim{Run: row.Run, Authority: in.Authority}
	found, err := s.readExecutionTool(ctx, db, claim, in.Step, in.Call.ID, &call)
	if err != nil {
		return row, call, err
	}
	if !found || call.Inspection == nil || call.Inspection.Token != in.OutcomeInspectionToken || call.Inspection.ActorID != in.Authority.UserID || !call.Inspection.ExpiresAt.After(time.Now()) || conversationHash(call.Call) != conversationHash(in.Call) || conversationHash(call.Definition) != conversationHash(in.Definition) || call.IdempotencyKey != in.IdempotencyKey {
		return row, call, conversationError("conflict", "outcome_inspection_superseded")
	}
	return row, call, nil
}

func (s *ConversationStore) VerifyConversationOutcomeInspection(ctx context.Context, in sdk.ConversationToolRequest) (bool, error) {
	valid := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		row, call, err := s.inspectionRow(ctx, tx, in)
		if err != nil {
			return err
		}
		if call.Inspection.CompletedAt != nil {
			return nil
		}
		if in.Confirmation == nil || in.ConfirmationID == "" {
			return nil
		}
		claim := persistence.ConversationClaim{Run: row.Run, Authority: in.Authority}
		record, found, err := s.readInteraction(ctx, tx, claim, in.Step, in.Call.ID, "confirmation")
		if err != nil {
			return err
		}
		i := record.Interaction
		c := in.Confirmation
		valid = found && i.Status == "approved" && i.ID == in.ConfirmationID && c.ID == i.ID && i.RespondedAt != nil && c.ApprovedAt.Equal(*i.RespondedAt) && i.RespondedBy == in.Authority.UserID && c.UserID == i.RespondedBy && c.ActionKey == i.ActionKey && c.ToolVersion == i.ToolVersion && c.ArgumentsHash == i.ArgumentsHash && i.ArgumentsHash == conversationHash(in.Call.Arguments) && i.DefinitionHash == conversationHash(in.Definition)
		return nil
	})
	return valid, err
}

func (s *ConversationStore) FinishConversationOutcomeInspection(ctx context.Context, inspection persistence.ConversationOutcomeInspection, result sdk.ConversationToolResult, a sdk.ConversationAuthority) error {
	if conversationOwner(a) != conversationOwner(inspection.Request.Authority) {
		return conversationError("forbidden", "principal_required")
	}
	if len(result.Content) > 0 && !json.Valid(result.Content) || result.Status != "completed" && result.Status != "failed" && result.Status != "uncertain" || result.Status == "failed" && result.ErrorCode == "" {
		return conversationError("bad_request", "tool_result_invalid")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		row, call, err := s.inspectionRow(ctx, tx, inspection.Request)
		if err != nil {
			return err
		}
		if call.Inspection.CompletedAt != nil {
			return nil
		}
		claim := persistence.ConversationClaim{Run: row.Run, Authority: a}
		// A late original receipt may already have settled the operation. It wins;
		// a subsequent query can neither replace it nor make it uncertain again.
		if call.State != "completed" {
			if err = s.finishExecutionToolReceipt(ctx, tx, claim, inspection.Request.Step, call.Call.ID, result, row); err != nil {
				return err
			}
			row, err = s.runRow(ctx, tx, row.Run.ConversationID, row.Run.ID, a)
			if err != nil {
				return err
			}
			if _, err = s.readExecutionTool(ctx, tx, claim, inspection.Request.Step, call.Call.ID, &call); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		call.Inspection.CompletedAt = &now
		if err = s.executionWrite(ctx, tx, conversationRunStepKindTool, claim, inspection.Request.Step, call.Call.ID, call, false); err != nil {
			return err
		}
		return s.executionEvent(ctx, tx, row, "tool.inspection.completed", map[string]any{"step": inspection.Request.Step, "call_id": call.Call.ID, "actor_id": a.UserID, "status": call.State, "checked_at": now})
	})
}
