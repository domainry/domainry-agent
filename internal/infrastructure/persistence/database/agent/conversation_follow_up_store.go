package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

const (
	conversationFollowUpStateTable = "_agent_conversation_follow_up_states"
	conversationFollowUpEventTable = "_agent_conversation_follow_up_events"
)

type conversationFollowUpState struct {
	Status          string
	ObservationHash string
	Occurrence      int
	LastTaskID      string
	CreatedAt       int64
}

func parseConversationFollowUpReport(raw string) (agentsdk.ConversationFollowUpReport, string, error) {
	var report agentsdk.ConversationFollowUpReport
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil {
		return report, "", conversationError("bad_request", "follow_up_report_invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return report, "", conversationError("bad_request", "follow_up_report_invalid")
	}
	report.Status, report.Observation, report.Summary = strings.TrimSpace(report.Status), strings.TrimSpace(report.Observation), strings.TrimSpace(report.Summary)
	if (report.Status != agentsdk.ConversationFollowUpReportActive && report.Status != agentsdk.ConversationFollowUpReportCompleted) ||
		report.Observation == "" || report.Summary == "" || len(report.Observation) > 8192 || len(report.Summary) > 4000 ||
		!utf8.ValidString(report.Observation) || !utf8.ValidString(report.Summary) || strings.ContainsRune(report.Observation, 0) || strings.ContainsRune(report.Summary, 0) {
		return report, "", conversationError("bad_request", "follow_up_report_invalid")
	}
	observation := report.Observation
	if json.Valid([]byte(observation)) {
		var value any
		if json.Unmarshal([]byte(observation), &value) == nil {
			var compact bytes.Buffer
			encoder := json.NewEncoder(&compact)
			encoder.SetEscapeHTML(false)
			if encoder.Encode(value) == nil {
				observation = strings.TrimSpace(compact.String())
			}
		}
	}
	digest := sha256.Sum256([]byte(observation))
	return report, hex.EncodeToString(digest[:]), nil
}

func (s *ConversationStore) readConversationFollowUpState(ctx context.Context, tx *sql.Tx, owner, planID string) (conversationFollowUpState, bool, error) {
	var state conversationFollowUpState
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationFollowUpStateTable).
		Columns("status", "observation_hash", "occurrence", "last_task_id", "created_at").
		Where(query.And(query.Equal("owner_key", owner), query.Equal("plan_id", planID))).Build()
	if err != nil {
		return state, false, err
	}
	err = tx.QueryRowContext(ctx, statement, args...).Scan(&state.Status, &state.ObservationHash, &state.Occurrence, &state.LastTaskID, &state.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	return state, err == nil, err
}

func (s *ConversationStore) saveConversationFollowUpState(ctx context.Context, tx *sql.Tx, owner, runtimeID, planID string, state conversationFollowUpState, insert bool, now time.Time) error {
	if insert {
		statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationFollowUpStateTable).
			Columns("owner_key", "plan_id", "runtime_id", "status", "observation_hash", "occurrence", "last_task_id", "created_at", "updated_at").
			Values(owner, planID, runtimeID, state.Status, state.ObservationHash, state.Occurrence, state.LastTaskID, now.UnixMilli(), now.UnixMilli()).Build()
		return conversationExec(ctx, tx, statement, args, err)
	}
	statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationFollowUpStateTable).
		Set("status", state.Status).Set("observation_hash", state.ObservationHash).Set("occurrence", state.Occurrence).
		Set("last_task_id", state.LastTaskID).Set("updated_at", now.UnixMilli()).
		Where(query.And(query.Equal("owner_key", owner), query.Equal("plan_id", planID))).Build()
	return conversationCAS(ctx, tx, statement, args, err)
}

func (s *ConversationStore) enqueueConversationFollowUpEvent(ctx context.Context, tx *sql.Tx, event agentsdk.ConversationFollowUpEvent) error {
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationFollowUpEventTable).
		Columns("owner_key", "event_id", "runtime_id", "status", "lease_owner", "fence", "lease_expires_at", "attempt", "next_attempt_at", "payload_json", "created_at", "updated_at").
		Values(conversationOwner(event.Authority), event.ID, event.Authority.RuntimeID, "pending", "", 0, 0, 0, 0, conversationJSON(event), event.OccurredAt.UnixMilli(), event.OccurredAt.UnixMilli()).
		OnConflictDoNothing("owner_key", "event_id").Build()
	return conversationExec(ctx, tx, statement, args, err)
}

func conversationFollowUpEventID(parts ...any) string {
	return "followup_" + conversationHash(parts)[:64]
}

func (s *ConversationStore) recordConversationFollowUpTerminal(ctx context.Context, tx *sql.Tx, row conversationTaskRow, run agentsdk.ConversationRun, result string, now time.Time) (agentsdk.ConversationTask, error) {
	task := row.task
	if task.FollowUp == nil || row.schedule == nil || run.Status == "cancelled" {
		return task, nil
	}
	owner := conversationOwner(row.authority)
	state, found, err := s.readConversationFollowUpState(ctx, tx, owner, row.schedule.PlanID)
	if err != nil {
		return task, err
	}
	if found && state.Status == agentsdk.ConversationFollowUpReportCompleted {
		// Completion is terminal for every later outcome. A Scheduler window that
		// was already in flight must not reopen the follow-up or report a stale
		// failure after the user has received completion.
		return task, nil
	}
	state.Occurrence++
	state.LastTaskID = task.ID
	if !found {
		state.CreatedAt = now.UnixMilli()
		state.Status = agentsdk.ConversationFollowUpReportActive
	}
	kind, summary, errorCode := "", "", run.ErrorCode
	if run.Status == "completed" {
		report, observationHash, parseErr := parseConversationFollowUpReport(result)
		if parseErr != nil {
			task.Status = agentsdk.ConversationTaskStatusFailed
			task.ErrorCode = "follow_up_report_invalid"
			kind, errorCode = agentsdk.ConversationFollowUpEventFailed, task.ErrorCode
		} else {
			summary = report.Summary
			if report.Status == agentsdk.ConversationFollowUpReportCompleted {
				kind = agentsdk.ConversationFollowUpEventCompleted
				state.Status = agentsdk.ConversationFollowUpReportCompleted
			} else if found && state.ObservationHash != "" && state.ObservationHash != observationHash {
				kind = agentsdk.ConversationFollowUpEventChanged
			}
			state.ObservationHash = observationHash
		}
	} else {
		kind = agentsdk.ConversationFollowUpEventFailed
		if errorCode == "" {
			errorCode = "task_failed"
		}
	}
	if err = s.saveConversationFollowUpState(ctx, tx, owner, row.authority.RuntimeID, row.schedule.PlanID, state, !found, now); err != nil {
		return task, err
	}
	if kind == "" {
		return task, nil
	}
	event := agentsdk.ConversationFollowUpEvent{
		ID: conversationFollowUpEventID(row.schedule.PlanID, task.ID, run.ID, kind), Kind: kind, Authority: row.authority,
		PlanID: row.schedule.PlanID, TaskID: task.ID, RunID: run.ID, Goal: task.Goal, Summary: summary,
		ErrorCode: errorCode, Occurrence: state.Occurrence, OccurredAt: now,
	}
	return task, s.enqueueConversationFollowUpEvent(ctx, tx, event)
}

func (s *ConversationStore) recordConversationFollowUpWaiting(ctx context.Context, tx *sql.Tx, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority, interaction agentsdk.ConversationInteraction, now time.Time) error {
	if run.BackgroundTask == nil || run.BackgroundTask.FollowUp == nil {
		return nil
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).
		Where(query.And(query.Equal("owner_key", conversationOwner(authority)), query.Equal("task_id", run.BackgroundTask.TaskID), query.Equal("source_conversation_id", run.ConversationID))).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
	if err != nil {
		return err
	}
	if row.schedule == nil || row.task.FollowUp == nil {
		return conversationError("conflict", "follow_up_scope_changed")
	}
	state, found, err := s.readConversationFollowUpState(ctx, tx, conversationOwner(authority), row.schedule.PlanID)
	if err != nil {
		return err
	}
	if found && state.Status == agentsdk.ConversationFollowUpReportCompleted {
		return nil
	}
	occurrence := state.Occurrence + 1
	event := agentsdk.ConversationFollowUpEvent{
		ID:   conversationFollowUpEventID(row.schedule.PlanID, row.task.ID, run.ID, interaction.ID, interaction.Revision),
		Kind: agentsdk.ConversationFollowUpEventNeedsAction, Authority: authority, PlanID: row.schedule.PlanID,
		TaskID: row.task.ID, RunID: run.ID, Goal: row.task.Goal, Question: interaction.Question, Occurrence: occurrence, OccurredAt: now,
	}
	return s.enqueueConversationFollowUpEvent(ctx, tx, event)
}

type conversationFollowUpEventRow struct {
	OwnerKey, Status, LeaseOwner string
	Fence, LeaseExpires, Attempt int64
	Event                        agentsdk.ConversationFollowUpEvent
}

func scanConversationFollowUpEvent(row interface{ Scan(...any) error }) (conversationFollowUpEventRow, error) {
	var result conversationFollowUpEventRow
	var payload []byte
	err := row.Scan(&result.OwnerKey, &result.Status, &result.LeaseOwner, &result.Fence, &result.LeaseExpires, &result.Attempt, &payload)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(payload, &result.Event)
	return result, err
}

func (s *ConversationStore) ClaimConversationFollowUpEvent(ctx context.Context, runtimeID, owner string, ttl time.Duration) (persistence.ConversationFollowUpEventClaim, bool, error) {
	var claim persistence.ConversationFollowUpEventClaim
	if runtimeID == "" || owner == "" || ttl <= 0 {
		return claim, false, conversationError("bad_request", "follow_up_claim_invalid")
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		builder := query.NewSelectBuilder(s.store.Renderer(), conversationFollowUpEventTable).
			Columns("owner_key", "status", "lease_owner", "fence", "lease_expires_at", "attempt", "payload_json").
			Where(query.And(query.Equal("runtime_id", runtimeID), query.LessThanOrEqual("next_attempt_at", now.UnixMilli()), query.Or(
				query.Equal("status", "pending"), query.And(query.Equal("status", "publishing"), query.LessThanOrEqual("lease_expires_at", now.UnixMilli())),
			))).OrderBy(query.Ascending("created_at"), query.Ascending("event_id")).Limit(1)
		if profile := s.store.Profile(); profile != nil && profile.Capabilities().RowLock {
			var err error
			builder, err = profile.ApplyClaimLock(builder, profile.Capabilities().SkipLocked)
			if err != nil {
				return err
			}
		}
		statement, args, err := builder.Build()
		if err != nil {
			return err
		}
		row, err := scanConversationFollowUpEvent(tx.QueryRowContext(ctx, statement, args...))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		fence := row.Fence + 1
		statement, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationFollowUpEventTable).
			Set("status", "publishing").Set("lease_owner", owner).Set("fence", fence).Set("lease_expires_at", now.Add(ttl).UnixMilli()).
			Set("attempt", row.Attempt+1).Set("updated_at", now.UnixMilli()).
			Where(query.And(query.Equal("owner_key", row.OwnerKey), query.Equal("event_id", row.Event.ID), query.Equal("status", row.Status), query.Equal("fence", row.Fence))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return err
		}
		claim = persistence.ConversationFollowUpEventClaim{Event: row.Event, Owner: owner, Fence: fence}
		return nil
	})
	return claim, claim.Event.ID != "", err
}

func (s *ConversationStore) transitionConversationFollowUpEvent(ctx context.Context, claim persistence.ConversationFollowUpEventClaim, complete bool) error {
	if claim.Event.ID == "" || claim.Owner == "" || claim.Fence < 1 {
		return conversationError("bad_request", "follow_up_claim_invalid")
	}
	now := time.Now().UTC()
	status, next := "pending", now.Add(time.Second).UnixMilli()
	if complete {
		status, next = "published", 0
	}
	statement, args, err := query.NewUpdateBuilder(s.store.Renderer(), conversationFollowUpEventTable).
		Set("status", status).Set("lease_owner", "").Set("lease_expires_at", 0).Set("next_attempt_at", next).Set("updated_at", now.UnixMilli()).
		Where(query.And(query.Equal("owner_key", conversationOwner(claim.Event.Authority)), query.Equal("event_id", claim.Event.ID), query.Equal("status", "publishing"), query.Equal("lease_owner", claim.Owner), query.Equal("fence", claim.Fence))).Build()
	return conversationCAS(ctx, s.store.Database(), statement, args, err)
}

func (s *ConversationStore) CompleteConversationFollowUpEvent(ctx context.Context, claim persistence.ConversationFollowUpEventClaim) error {
	return s.transitionConversationFollowUpEvent(ctx, claim, true)
}

func (s *ConversationStore) ReleaseConversationFollowUpEvent(ctx context.Context, claim persistence.ConversationFollowUpEventClaim) error {
	return s.transitionConversationFollowUpEvent(ctx, claim, false)
}

var _ persistence.ConversationFollowUpEventRepository = (*ConversationStore)(nil)
