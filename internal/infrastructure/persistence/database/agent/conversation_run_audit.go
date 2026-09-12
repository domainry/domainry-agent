package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

const conversationRunAuditLimit = 4096

type conversationRunAuditData struct {
	Step           int                               `json:"step"`
	Attempt        int                               `json:"attempt"`
	CallID         string                            `json:"call_id"`
	Tool           string                            `json:"tool"`
	ActionKey      string                            `json:"action_key"`
	Status         string                            `json:"status"`
	Revision       string                            `json:"revision"`
	ConfirmationID string                            `json:"confirmation_id"`
	ErrorCode      string                            `json:"error_code"`
	Usage          map[string]any                    `json:"usage"`
	Interaction    *agentsdk.ConversationInteraction `json:"interaction"`
}

func conversationRunStep(run *agentsdk.ConversationRun, number int) *agentsdk.ConversationStepView {
	for index := range run.Steps {
		if run.Steps[index].Number == number {
			return &run.Steps[index]
		}
	}
	return nil
}

func conversationRunTool(run *agentsdk.ConversationRun, step int, callID string) *agentsdk.ConversationToolView {
	value := conversationRunStep(run, step)
	if value == nil {
		return nil
	}
	for index := range value.Calls {
		if value.Calls[index].ID == callID {
			return &value.Calls[index]
		}
	}
	return nil
}

func auditDuration(started map[string]time.Time, key string, completed time.Time) int64 {
	start, ok := started[key]
	if !ok || completed.Before(start) {
		return 0
	}
	return completed.Sub(start).Milliseconds()
}

func (s *ConversationStore) projectConversationRunAudit(ctx context.Context, run *agentsdk.ConversationRun, a agentsdk.ConversationAuthority) error {
	run.CorrelationID = run.ID
	run.Metrics.Steps = len(run.Steps)
	run.Audit = []agentsdk.ConversationRunAuditEvent{}
	run.AuditComplete = true
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), "_agent_conversation_events").Columns("payload_json").
		Where(query.And(conversationScope(a, run.ConversationID), query.Equal("run_id", run.ID))).OrderBy(query.Ascending("seq")).Build()
	if err != nil {
		return err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	stepStarted, toolStarted := map[string]time.Time{}, map[string]time.Time{}
	toolSeen := map[string]bool{}
	for rows.Next() {
		var raw []byte
		var event agentsdk.ConversationEvent
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &event); err != nil {
			return err
		}
		var data conversationRunAuditData
		encoded, _ := json.Marshal(event.Data)
		_ = json.Unmarshal(encoded, &data)
		entry := agentsdk.ConversationRunAuditEvent{Seq: event.Seq, Step: data.Step, Attempt: data.Attempt, CallID: data.CallID, Tool: data.Tool, ActionKey: data.ActionKey, AuthorizationRevision: data.Revision, ErrorCode: data.ErrorCode, OccurredAt: event.CreatedAt}
		include := true
		switch {
		case event.Type == "run.queued":
			entry.Type, entry.Status = "run", "queued"
		case event.Type == "run.started":
			entry.Type, entry.Status = "run", "running"
			if run.StartedAt == nil {
				started := event.CreatedAt
				run.StartedAt = &started
				if !run.CreatedAt.IsZero() && !started.Before(run.CreatedAt) {
					run.QueueDurationMilliseconds = started.Sub(run.CreatedAt).Milliseconds()
				}
			}
		case strings.HasPrefix(event.Type, "run."):
			entry.Type, entry.Status = "run", strings.TrimPrefix(event.Type, "run.")
			if run.Terminal() && (entry.Status == "completed" || entry.Status == "failed" || entry.Status == "cancelled") {
				completed := event.CreatedAt
				run.CompletedAt = &completed
			}
		case event.Type == "step.started":
			entry.Type, entry.Status = "step", "prepared"
		case event.Type == "step.attempt.started":
			entry.Type, entry.Status = "model", "started"
			run.Metrics.ModelCalls++
			key := conversationHash([]any{data.Step, data.Attempt})
			stepStarted[key] = event.CreatedAt
			if step := conversationRunStep(run, data.Step); step != nil {
				started := event.CreatedAt
				step.StartedAt = &started
			}
		case event.Type == "step.completed":
			entry.Type, entry.Status = "model", "completed"
			key := conversationHash([]any{data.Step, data.Attempt})
			entry.DurationMilliseconds = auditDuration(stepStarted, key, event.CreatedAt)
			if step := conversationRunStep(run, data.Step); step != nil {
				completed := event.CreatedAt
				step.CompletedAt, step.DurationMilliseconds, step.Usage = &completed, entry.DurationMilliseconds, data.Usage
			}
		case event.Type == "authorization.checked":
			entry.Type, entry.Status = "authorization", data.Status
			entry.InteractionID = data.ConfirmationID
			run.Metrics.AuthorizationChecks++
			if tool := conversationRunTool(run, data.Step, data.CallID); tool != nil {
				if tool.Authorization == nil {
					tool.Authorization = &agentsdk.ConversationAuthorizationView{}
				}
				tool.Authorization.Status, tool.Authorization.Revision = data.Status, data.Revision
				tool.Authorization.Checks++
			}
		case event.Type == "tool.started":
			entry.Type, entry.Status = "tool", "started"
			run.Metrics.ToolAttempts++
			key := conversationHash([]any{data.Step, data.CallID})
			toolStarted[key] = event.CreatedAt
			if !toolSeen[key] {
				toolSeen[key] = true
				run.Metrics.ToolCalls++
			}
			if tool := conversationRunTool(run, data.Step, data.CallID); tool != nil {
				started := event.CreatedAt
				tool.StartedAt = &started
			}
		case strings.HasPrefix(event.Type, "tool."):
			entry.Type, entry.Status = "tool", strings.TrimPrefix(event.Type, "tool.")
			if data.Status != "" {
				entry.Status = data.Status
			}
			key := conversationHash([]any{data.Step, data.CallID})
			entry.DurationMilliseconds = auditDuration(toolStarted, key, event.CreatedAt)
			if tool := conversationRunTool(run, data.Step, data.CallID); tool != nil {
				if entry.Tool == "" {
					entry.Tool = tool.Name
				}
				if event.Type != "tool.waiting" {
					completed := event.CreatedAt
					tool.CompletedAt, tool.DurationMilliseconds = &completed, entry.DurationMilliseconds
				}
			}
		case strings.HasPrefix(event.Type, "interaction.") && data.Interaction != nil:
			interaction := data.Interaction
			entry.Type, entry.Status = "interaction", interaction.Status
			entry.Step, entry.CallID, entry.Tool, entry.ActionKey = interaction.Step, interaction.CallID, interaction.Tool, interaction.ActionKey
			entry.InteractionID, entry.ActorID = interaction.ID, interaction.RespondedBy
			if interaction.Kind == "confirmation" {
				entry.Type = "confirmation"
				if event.Type == "interaction.responded" {
					run.Metrics.ConfirmationDecisions++
				}
				if tool := conversationRunTool(run, interaction.Step, interaction.CallID); tool != nil {
					tool.Confirmation = &agentsdk.ConversationConfirmationView{ID: interaction.ID, Status: interaction.Status, RespondedBy: interaction.RespondedBy, RespondedAt: interaction.RespondedAt}
				}
			}
		default:
			include = false
		}
		if include {
			if len(run.Audit) >= conversationRunAuditLimit {
				run.AuditComplete = false
				continue
			}
			run.Audit = append(run.Audit, entry)
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if run.StartedAt != nil {
		until := run.UpdatedAt
		if run.CompletedAt != nil {
			until = *run.CompletedAt
		}
		if !until.Before(*run.StartedAt) {
			run.DurationMilliseconds = until.Sub(*run.StartedAt).Milliseconds()
		}
	}
	return nil
}
