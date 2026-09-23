package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

func (s *ConversationStore) ConversationExternalAgentTasks(ctx context.Context, agentID string, limit int, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationTask, bool, error) {
	if err := conversationAuthority(a); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 32 {
		return nil, false, conversationError("bad_request", "external_agent_query_invalid")
	}
	out := []agentsdk.ConversationTask{}
	for offset := 0; ; offset += 64 {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("owner_key", conversationOwner(a)), query.Or(query.Equal("status", agentsdk.ConversationTaskStatusQueued), query.Equal("status", agentsdk.ConversationTaskStatusRunning), query.Equal("status", agentsdk.ConversationTaskStatusCancelled)))).OrderBy(query.Ascending("created_at"), query.Ascending("task_id")).Limit(64).Offset(offset).Build()
		if err != nil {
			return nil, false, err
		}
		rows, err := s.store.Database().QueryContext(ctx, q, args...)
		if err != nil {
			return nil, false, err
		}
		read := 0
		for rows.Next() {
			read++
			row, scanErr := scanConversationTask(rows)
			if scanErr != nil {
				_ = rows.Close()
				return nil, false, scanErr
			}
			if row.authority != a || row.task.Agent == nil || row.task.Agent.ID != agentID || row.task.Agent.External == nil || row.task.ExternalExecution == nil {
				continue
			}
			if row.task.Status == agentsdk.ConversationTaskStatusCancelled && row.task.ExternalExecution.Status != "stop_requested" {
				continue
			}
			out = append(out, row.task)
			if len(out) > limit {
				_ = rows.Close()
				return out[:limit], false, nil
			}
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return nil, false, rowErr
		}
		if read < 64 {
			return out, true, nil
		}
	}
}

func (s *ConversationStore) externalAgentTaskRow(ctx context.Context, tx *sql.Tx, taskID string, a agentsdk.ConversationAuthority) (conversationTaskRow, error) {
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(conversationOwner(a), taskID)).Build()
	if err != nil {
		return conversationTaskRow{}, err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return row, err
	}
	if row.authority != a {
		return row, conversationError("forbidden", "external_agent_identity_mismatch")
	}
	return row, nil
}

func validExternalAgentTask(task agentsdk.ConversationTask, agentID string) bool {
	return task.DelegationID != "" && task.Agent != nil && task.Agent.ID == agentID && task.Agent.External != nil && task.ExternalExecution != nil && task.Agent.External.Protocol == task.ExternalExecution.Protocol && task.Agent.External.Version == task.ExternalExecution.Version && conversationHash(task.Agent.External.Capabilities) == conversationHash(task.ExternalExecution.Capabilities)
}

func (s *ConversationStore) ClaimConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentClaim, a agentsdk.ConversationAuthority) (persistence.ConversationExternalAgentTaskRecord, error) {
	var out persistence.ConversationExternalAgentTaskRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		key := conversationHash([]string{"external-agent-claim", taskID, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			out.Replay = replay
			return err
		}
		row, err := s.externalAgentTaskRow(ctx, tx, taskID, a)
		if err != nil {
			return err
		}
		task := row.task
		if !validExternalAgentTask(task, in.AgentID) {
			return conversationError("not_found", "external_agent_task_not_found")
		}
		if task.Status != agentsdk.ConversationTaskStatusQueued || task.ExternalExecution.Status != "waiting_claim" {
			return conversationError("conflict", "external_agent_task_claimed")
		}
		if conversationHash(in.Capabilities) != conversationHash(task.ExternalExecution.Capabilities) {
			return conversationError("conflict", "external_agent_capability_mismatch")
		}
		d, err := s.conversationDelegation(ctx, tx, task.DelegationID, a)
		if err != nil {
			return err
		}
		if d.TaskID != task.ID || d.ToAgentID != in.AgentID || d.Status != "accepted" {
			return conversationError("conflict", "external_agent_task_unavailable")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		exec := task.ExternalExecution
		exec.Status, exec.ClaimClientID = "running", in.ClientID
		exec.SessionID = "exts_" + conversationHash([]any{conversationOwner(a), taskID, in.ClientID, exec.Attempt})[:32]
		exec.ClaimedAt, exec.UpdatedAt = &now, now
		task.Status, task.UpdatedAt = agentsdk.ConversationTaskStatusRunning, now
		setConversationTaskGoalPhase(&task, now)
		q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(a), task.ID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
		previous := d.Revision
		d.Status, d.Revision, d.UpdatedAt = "running", previous+1, now
		if err = s.saveConversationDelegation(ctx, tx, d, previous, a); err != nil {
			return err
		}
		out = persistence.ConversationExternalAgentTaskRecord{Task: task, Delegation: d}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

func validExternalEvent(event agentsdk.ConversationExternalAgentEvent, details bool) bool {
	if event.Kind != "progress" && event.Kind != "tool" && event.Kind != "message" && event.Kind != "completed" && event.Kind != "failed" && event.Kind != "cancelled" {
		return false
	}
	if strings.TrimSpace(event.Summary) == "" || len(event.Summary) > 2048 || len(event.Detail) > 8192 || len(event.Tool) > 255 || len(event.Usage) > 8192 {
		return false
	}
	if !details && (event.Kind == "tool" || event.Detail != "" || event.Tool != "" || len(event.Usage) > 0) {
		return false
	}
	return event.Progress == nil || *event.Progress >= 0 && *event.Progress <= 1
}

func (s *ConversationStore) acknowledgeExternalMessages(ctx context.Context, tx *sql.Tx, d agentsdk.ConversationDelegation, task agentsdk.ConversationTask, in agentsdk.ConversationExternalAgentReport, a agentsdk.ConversationAuthority) error {
	if len(in.AcknowledgedMessages) > 8 {
		return conversationError("bad_request", "external_agent_messages_invalid")
	}
	seen := map[string]bool{}
	for _, id := range in.AcknowledgedMessages {
		if seen[id] {
			return conversationError("bad_request", "external_agent_messages_invalid")
		}
		seen[id] = true
		p := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("message_id", id), query.Equal("delegation_id", d.ID), query.Equal("conversation_id", task.ExecutionConversationID), query.Equal("consumed_run_id", ""))
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationAgentMessageTable).Columns("payload_json").Where(p).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, q, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			return conversationError("conflict", "agent_message_consumed")
		} else if err != nil {
			return err
		}
		var message agentsdk.ConversationAgentMessage
		if err = json.Unmarshal(raw, &message); err != nil {
			return err
		}
		ready, err := s.peerMessageReady(ctx, tx, message, a)
		if err != nil {
			return err
		}
		if !ready || message.ToAgentID != task.Agent.ID {
			return conversationError("conflict", "agent_message_not_ready")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		message.ConsumedAtStep, message.ConsumedByRunID, message.ConsumedAt = int(task.ExternalExecution.LastEventSeq), in.SessionID, &now
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationAgentMessageTable).Set("consumed_run_id", in.SessionID).Set("payload_json", conversationJSON(message)).Where(p).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
	}
	return nil
}

func (s *ConversationStore) ReportConversationExternalAgentTask(ctx context.Context, taskID string, in agentsdk.ConversationExternalAgentReport, a agentsdk.ConversationAuthority) (persistence.ConversationExternalAgentTaskRecord, error) {
	var out persistence.ConversationExternalAgentTaskRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		key := conversationHash([]string{"external-agent-report", taskID, in.ClientID})
		if replay, err := s.collaborationReplay(ctx, tx, a, key, in, &out); err != nil || replay {
			out.Replay = replay
			return err
		}
		row, err := s.externalAgentTaskRow(ctx, tx, taskID, a)
		if err != nil {
			return err
		}
		task := row.task
		if !validExternalAgentTask(task, in.AgentID) {
			return conversationError("not_found", "external_agent_task_not_found")
		}
		exec := task.ExternalExecution
		if exec.SessionID != in.SessionID || exec.Status != "running" && exec.Status != "stop_requested" {
			return conversationError("conflict", "external_agent_session_invalid")
		}
		if exec.LastEventSeq != in.ExpectedLastEventSeq || len(in.Events) > 16 || len(in.Events) == 0 && len(in.AcknowledgedMessages) == 0 {
			return conversationError("conflict", "external_agent_event_cursor_invalid")
		}
		d, err := s.conversationDelegation(ctx, tx, task.DelegationID, a)
		if err != nil {
			return err
		}
		terminal := ""
		now := time.Now().UTC().Truncate(time.Millisecond)
		for i := range in.Events {
			event := in.Events[i]
			if event.Seq != in.ExpectedLastEventSeq+int64(i)+1 || !validExternalEvent(event, exec.DetailsAvailable) || terminal != "" {
				return conversationError("bad_request", "external_agent_event_invalid")
			}
			event.Attempt, event.CreatedAt = exec.Attempt, now
			if event.Kind == "completed" || event.Kind == "failed" || event.Kind == "cancelled" {
				terminal = event.Kind
			}
			exec.Events = append(exec.Events, event)
			exec.LastEventSeq = event.Seq
		}
		if len(exec.Events) > 128 {
			exec.Events, exec.EventsComplete = exec.Events[len(exec.Events)-128:], false
		}
		if err = s.acknowledgeExternalMessages(ctx, tx, d, task, in, a); err != nil {
			return err
		}
		exec.UpdatedAt = now
		if terminal == "completed" {
			if d.Status != "delivered" || d.Delivery == nil {
				return conversationError("conflict", "external_agent_delivery_required")
			}
			exec.Status, task.Status, task.CompletedAt = "completed", agentsdk.ConversationTaskStatusCompleted, &now
			task.CompletionEventSeq = exec.LastEventSeq
			task.CompletionEventID = "task_event_" + conversationHash([]any{task.ID, exec.SessionID, exec.LastEventSeq, terminal})[:32]
		} else if terminal == "failed" {
			exec.Status, task.Status, task.ErrorCode, task.CompletedAt = "failed", agentsdk.ConversationTaskStatusFailed, "external_agent_failed", &now
			if d.Status == "running" || d.Status == "accepted" {
				previous := d.Revision
				d.Status, d.Revision, d.UpdatedAt = "failed", previous+1, now
				if err = s.saveConversationDelegation(ctx, tx, d, previous, a); err != nil {
					return err
				}
			}
		} else if terminal == "cancelled" {
			if !exec.StopRequested || task.Status != agentsdk.ConversationTaskStatusCancelled || in.EffectState != "none" && in.EffectState != "known" && in.EffectState != "unknown" {
				return conversationError("conflict", "external_agent_stop_invalid")
			}
			exec.Status, exec.StopAcknowledged, exec.EffectState = "cancelled", true, in.EffectState
		} else if in.EffectState != "" {
			return conversationError("bad_request", "external_agent_effect_state_invalid")
		}
		task.UpdatedAt = now
		setConversationTaskGoalPhase(&task, now)
		q, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(conversationTaskPredicate(conversationOwner(a), task.ID), query.Equal("updated_at", row.task.UpdatedAt.UnixMilli()))).Build()
		if err = conversationCAS(ctx, tx, q, args, err); err != nil {
			return err
		}
		out = persistence.ConversationExternalAgentTaskRecord{Task: task, Delegation: d}
		return s.saveCollaborationMutation(ctx, tx, a, key, in, out)
	})
	return out, err
}

var _ persistence.ConversationExternalAgentRepository = (*ConversationStore)(nil)
