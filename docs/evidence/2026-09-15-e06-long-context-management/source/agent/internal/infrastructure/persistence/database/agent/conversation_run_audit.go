package agent

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
)

const conversationRunAuditLimit = 4096

type conversationRunAuditData struct {
	ActorID        string                            `json:"actor_id"`
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
	ParallelWidth  int                               `json:"parallel_width"`
	ParallelBatch  int                               `json:"parallel_batch"`
	Context        *agentsdk.ConversationContextView `json:"context"`
}

func conversationUsageInteger(value any) (int64, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	var number float64
	if json.Unmarshal(raw, &number) != nil || number < 0 || number > math.MaxInt64 || math.Trunc(number) != number {
		return 0, false
	}
	return int64(number), true
}

func conversationNestedUsage(usage map[string]any, path ...string) (int64, bool) {
	var value any = usage
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			raw, err := json.Marshal(value)
			if err != nil || json.Unmarshal(raw, &object) != nil {
				return 0, false
			}
		}
		value, ok = object[key]
		if !ok {
			return 0, false
		}
	}
	return conversationUsageInteger(value)
}

func conversationCacheUsage(usage map[string]any) (read, creation int64) {
	creation, _ = conversationNestedUsage(usage, "cache_creation_input_tokens")
	for _, path := range [][]string{{"cache_read_input_tokens"}, {"prompt_tokens_details", "cached_tokens"}, {"input_tokens_details", "cached_tokens"}, {"cached_tokens"}} {
		if value, ok := conversationNestedUsage(usage, path...); ok {
			read = value
			break
		}
	}
	return read, creation
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
	run.Metrics = agentsdk.ConversationRunMetrics{Steps: len(run.Steps)}
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
	toolSeen, parallelCallSeen, parallelBatchSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	parallelBatchByCall := map[string]string{}
	parallelActive := 0
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
		case event.Type == "context.assembled":
			entry.Type, entry.Status = "context", "assembled"
			if data.Context != nil {
				run.Context = data.Context
				if data.Context.Window != nil {
					run.Metrics.PeakContextBytes = max(run.Metrics.PeakContextBytes, data.Context.Window.InputBytes)
					run.Metrics.ContextLimitBytes = max(run.Metrics.ContextLimitBytes, data.Context.Window.LimitBytes)
				}
			}
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
			if entry.Status == "completed" || entry.Status == "failed" || entry.Status == "cancelled" {
				parallelActive = 0
				parallelBatchByCall = map[string]string{}
			}
		case event.Type == "step.started":
			entry.Type, entry.Status = "step", "prepared"
			if step := conversationRunStep(run, data.Step); step != nil && data.Context != nil {
				step.Context = data.Context
				if data.Context.Window != nil {
					run.Metrics.PeakContextBytes = max(run.Metrics.PeakContextBytes, data.Context.Window.InputBytes)
					run.Metrics.ContextLimitBytes = max(run.Metrics.ContextLimitBytes, data.Context.Window.LimitBytes)
				}
				if data.Context.Compaction != nil {
					run.Metrics.ContextCompactions++
					run.Metrics.CompactedResults += data.Context.Compaction.Results
					run.Metrics.CompactedIntervals += data.Context.Compaction.Intervals
				}
			}
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
				read, creation := conversationCacheUsage(data.Usage)
				run.Metrics.CacheReadInputTokens += read
				run.Metrics.CacheCreationInputTokens += creation
				if step.Context != nil {
					step.Context.CacheReadInputTokens, step.Context.CacheCreationInputTokens = read, creation
				}
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
			attemptKey := conversationHash([]any{data.Attempt, data.Step, data.CallID})
			toolStarted[key] = event.CreatedAt
			if !toolSeen[key] {
				toolSeen[key] = true
				run.Metrics.ToolCalls++
			}
			if data.ParallelWidth > 1 {
				batchKey := conversationHash([]any{data.Attempt, data.Step, data.ParallelBatch})
				if _, active := parallelBatchByCall[attemptKey]; !active {
					parallelBatchByCall[attemptKey] = batchKey
					parallelActive++
				}
				if !parallelCallSeen[key] {
					parallelCallSeen[key] = true
					run.Metrics.ParallelToolCalls++
				}
				if !parallelBatchSeen[batchKey] {
					parallelBatchSeen[batchKey] = true
					run.Metrics.ParallelToolBatches++
				}
				run.Metrics.PeakParallelTools = max(run.Metrics.PeakParallelTools, parallelActive)
			}
			if tool := conversationRunTool(run, data.Step, data.CallID); tool != nil {
				started := event.CreatedAt
				tool.StartedAt = &started
			}
		case event.Type == "tool.receipt.reused":
			entry.Type, entry.Status = "outcome_receipt", "reused"
			key := conversationHash([]any{data.Step, data.CallID})
			if !toolSeen[key] {
				toolSeen[key] = true
				run.Metrics.ToolCalls++
			}
		case event.Type == "tool.inspection.started" || event.Type == "tool.inspection.completed":
			entry.Type, entry.Status = "outcome_inspection", data.Status
			entry.ActorID = data.ActorID
			if tool := conversationRunTool(run, data.Step, data.CallID); tool != nil {
				entry.Tool = tool.Name
			}
		case strings.HasPrefix(event.Type, "tool."):
			entry.Type, entry.Status = "tool", strings.TrimPrefix(event.Type, "tool.")
			if data.Status != "" {
				entry.Status = data.Status
			}
			key := conversationHash([]any{data.Step, data.CallID})
			attemptKey := conversationHash([]any{data.Attempt, data.Step, data.CallID})
			if _, parallel := parallelBatchByCall[attemptKey]; parallel && event.Type != "tool.waiting" {
				delete(parallelBatchByCall, attemptKey)
				parallelActive = max(0, parallelActive-1)
			}
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
	if run.Metrics.ModelCalls == 0 && run.Model != "" {
		run.Metrics.ModelCalls = 1
		read, creation := conversationCacheUsage(run.Usage)
		run.Metrics.CacheReadInputTokens = read
		run.Metrics.CacheCreationInputTokens = creation
		if run.Context != nil {
			run.Context.CacheReadInputTokens, run.Context.CacheCreationInputTokens = read, creation
		}
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
