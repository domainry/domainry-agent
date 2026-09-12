package agent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-orm/query"
)

type conversationTaskRow struct {
	task        agentsdk.ConversationTask
	authority   agentsdk.ConversationAuthority
	requestHash string
	schedule    *agentsdk.ConversationTaskScheduleRef
}

var conversationTaskColumns = []string{"payload_json", "authority_json", "request_hash", "scheduled_plan_id", "scheduler_run_id", "scheduled_for"}

type conversationTaskCursor struct {
	Owner, Query, ID string
	Created, Cutoff  int64
}

func scanConversationTask(row interface{ Scan(...any) error }) (conversationTaskRow, error) {
	var out conversationTaskRow
	var raw, authority []byte
	var planID, schedulerRunID sql.NullString
	var scheduledFor sql.NullInt64
	if err := row.Scan(&raw, &authority, &out.requestHash, &planID, &schedulerRunID, &scheduledFor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, conversationError("not_found", "task_not_found")
		}
		return out, err
	}
	if err := json.Unmarshal(raw, &out.task); err != nil {
		return out, err
	}
	if err := json.Unmarshal(authority, &out.authority); err != nil {
		return out, err
	}
	if planID.Valid || schedulerRunID.Valid || scheduledFor.Valid {
		if !planID.Valid || !schedulerRunID.Valid || !scheduledFor.Valid {
			return out, conversationError("conflict", "scheduled_task_metadata_invalid")
		}
		out.schedule = &agentsdk.ConversationTaskScheduleRef{PlanID: planID.String, SchedulerRunID: schedulerRunID.String, ScheduledFor: time.UnixMilli(scheduledFor.Int64).UTC()}
	}
	return out, nil
}

func conversationTaskReceipt(task agentsdk.ConversationTask) agentsdk.ConversationTaskReceipt {
	allowed := make([]string, 0, len(task.ToolScope))
	for _, tool := range task.ToolScope {
		allowed = append(allowed, tool.Key)
	}
	return agentsdk.ConversationTaskReceipt{
		ID: task.ID, Status: task.Status, AllowedTools: allowed, Budget: task.Budget,
		SourceConversationID: task.SourceConversationID, SourceRunID: task.SourceRunID, CreatedAt: task.CreatedAt,
	}
}

func (s *ConversationStore) ApplyConversationTaskTool(ctx context.Context, in agentsdk.ConversationToolRequest, prepared agentsdk.ConversationTask) (agentsdk.ConversationToolResult, error) {
	definition := agentsdk.BackgroundTaskConversationTool()
	result, err := s.applyLocalTool(ctx, in, []agentsdk.ConversationToolDefinition{definition}, func(tx *sql.Tx, claim agentpersistence.ConversationClaim, call agentpersistence.ConversationToolExecution) (agentsdk.ConversationToolResult, error) {
		var start agentsdk.ConversationTaskStart
		if call.Call.Name != definition.Key || json.Unmarshal([]byte(call.Call.Arguments), &start) != nil {
			return agentsdk.ConversationToolResult{}, conversationError("bad_request", "task_start_invalid")
		}
		if prepared.ID != "" || prepared.Status != "" || prepared.ExecutionRunID != "" || prepared.FollowUp != nil || prepared.SourceConversationID != in.ConversationID || prepared.SourceRunID != in.RunID || prepared.Goal != start.Goal || prepared.Input != start.Input || prepared.Budget != start.Budget || len(prepared.ToolScope) != len(start.AllowedTools) {
			return agentsdk.ConversationToolResult{}, conversationError("conflict", "tool_input_conflict")
		}
		seen := map[string]bool{}
		for index, tool := range prepared.ToolScope {
			if tool.Key != start.AllowedTools[index] || tool.Key == definition.Key || seen[tool.Key] || tool.Version == "" || tool.ActionKey == "" || tool.DefinitionHash == "" {
				return agentsdk.ConversationToolResult{}, conversationError("conflict", "tool_input_conflict")
			}
			seen[tool.Key] = true
		}
		countQuery, countArgs, countErr := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).
			Projections(query.Project(query.CountAll())).
			Where(query.And(query.Equal("owner_key", conversationOwner(claim.Authority)), query.Equal("source_run_id", claim.Run.ID))).
			Build()
		if countErr != nil {
			return agentsdk.ConversationToolResult{}, countErr
		}
		children := 0
		if err := tx.QueryRowContext(ctx, countQuery, countArgs...).Scan(&children); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		if children >= agentsdk.ConversationTaskMaxChildrenPerRun {
			return agentsdk.ConversationToolResult{}, conversationError("conflict", "task_child_limit")
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.ID = "task_" + conversationHash(call.IdempotencyKey)[:32]
		prepared.Status = agentsdk.ConversationTaskStatusQueued
		prepared.CreatedAt, prepared.UpdatedAt = now, now
		q, args, buildErr := query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns(
			"owner_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json",
		).Values(
			conversationOwner(claim.Authority), prepared.ID, claim.Authority.RuntimeID, prepared.SourceConversationID, prepared.SourceRunID,
			prepared.Status, conversationJSON(claim.Authority), conversationHash([]any{call.Definition, call.Call}), now.UnixMilli(), now.UnixMilli(), conversationJSON(prepared),
		).Build()
		if err := conversationExec(ctx, tx, q, args, buildErr); err != nil {
			return agentsdk.ConversationToolResult{}, err
		}
		return agentsdk.ConversationToolResult{
			Completion: "accepted", Status: "completed", ResourceID: prepared.ID,
			Content: conversationJSON(map[string]any{"task": conversationTaskReceipt(prepared)}),
		}, nil
	})
	return result, err
}

func (s *ConversationStore) AcceptScheduledConversationTask(ctx context.Context, in agentsdk.ScheduledConversationTaskRequest, prepared agentsdk.ConversationTask) (agentsdk.ScheduledConversationTaskReceipt, error) {
	var out agentsdk.ScheduledConversationTaskReceipt
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(in.Authority); err != nil {
			return err
		}
		if prepared.ID != "" || prepared.Status != "" || prepared.ExecutionRunID != "" || prepared.SourceConversationID != in.ConversationID || prepared.SourceRunID != in.SourceRunID ||
			prepared.Goal != in.Input.Goal || prepared.Input != in.Input.Input || prepared.Budget != in.Input.Budget || !equalConversationFollowUpScope(prepared.FollowUp, in.Input.FollowUp) || !equalConversationTaskTools(prepared, in.Input.AllowedTools) {
			return conversationError("conflict", "scheduled_task_input_conflict")
		}
		conversation, err := s.get(ctx, tx, in.ConversationID, in.Authority)
		if err != nil {
			return err
		}
		if in.SourceRunID != "" {
			if _, err := s.runRow(ctx, tx, conversation.ID, in.SourceRunID, in.Authority); err != nil {
				return err
			}
		}
		requestHash := conversationHash(in)
		prepared.ID = "task_" + conversationHash([]any{conversationOwner(in.Authority), in.IdempotencyKey})[:32]
		prepared.Status = agentsdk.ConversationTaskStatusQueued
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.CreatedAt, prepared.UpdatedAt = now, now
		statement, args, buildErr := query.NewInsertBuilder(s.store.Renderer(), conversationTaskTable).Columns(
			"owner_key", "task_id", "runtime_id", "source_conversation_id", "source_run_id", "status", "authority_json", "request_hash", "created_at", "updated_at", "payload_json", "scheduled_plan_id", "scheduler_run_id", "scheduled_for",
		).Values(
			conversationOwner(in.Authority), prepared.ID, in.Authority.RuntimeID, prepared.SourceConversationID, prepared.SourceRunID,
			prepared.Status, conversationJSON(in.Authority), requestHash, now.UnixMilli(), now.UnixMilli(), conversationJSON(prepared), in.PlanID, in.SchedulerRunID, in.ScheduledFor.UnixMilli(),
		).OnConflictDoNothing("owner_key", "task_id").Build()
		if buildErr != nil {
			return buildErr
		}
		result, err := tx.ExecContext(ctx, statement, args...)
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 1 {
			out.Task = prepared
			return nil
		}
		if inserted != 0 {
			return conversationError("conflict", "scheduled_task_idempotency_conflict")
		}
		statement, args, buildErr = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(in.Authority)), query.Equal("task_id", prepared.ID))).Build()
		if buildErr != nil {
			return buildErr
		}
		existing, err := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		if err != nil {
			return err
		}
		if existing.requestHash != requestHash || conversationOwner(existing.authority) != conversationOwner(in.Authority) || existing.schedule == nil || existing.schedule.PlanID != in.PlanID || existing.schedule.SchedulerRunID != in.SchedulerRunID || !existing.schedule.ScheduledFor.Equal(in.ScheduledFor) {
			return conversationError("conflict", "scheduled_task_idempotency_conflict")
		}
		out.Task, out.Replay = existing.task, true
		return nil
	})
	return out, err
}

func equalConversationTaskTools(task agentsdk.ConversationTask, tools []string) bool {
	if len(task.ToolScope) != len(tools) {
		return false
	}
	for index, tool := range task.ToolScope {
		if tool.Key != tools[index] {
			return false
		}
	}
	return true
}

func equalConversationFollowUpScope(left, right *agentsdk.ConversationFollowUpScope) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.CompletionCondition == right.CompletionCondition
}

func (s *ConversationStore) ConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	if err := conversationAuthority(a); err != nil {
		return agentsdk.ConversationTask{}, err
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", taskID))).Build()
	if err != nil {
		return agentsdk.ConversationTask{}, err
	}
	row, err := scanConversationTask(s.store.Database().QueryRowContext(ctx, q, args...))
	return row.task, err
}

func conversationTaskStatus(status string) bool {
	switch status {
	case "", agentsdk.ConversationTaskStatusQueued, agentsdk.ConversationTaskStatusRunning, agentsdk.ConversationTaskStatusCompleted, agentsdk.ConversationTaskStatusFailed, agentsdk.ConversationTaskStatusCancelled:
		return true
	default:
		return false
	}
}

func (s *ConversationStore) ConversationTasks(ctx context.Context, in agentsdk.ConversationTaskQuery, a agentsdk.ConversationAuthority) (agentpersistence.ConversationTaskRecordPage, error) {
	out := agentpersistence.ConversationTaskRecordPage{Items: []agentsdk.ConversationTask{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if in.Limit == 0 {
		in.Limit = 20
	}
	if !executionText(in.Query, 256, false) || !conversationTaskStatus(in.Status) || len(in.Cursor) > 2048 || in.Limit < 1 || in.Limit > 20 || in.SourceConversationID != "" && !personalMemoryKey(in.SourceConversationID) {
		return out, conversationError("bad_request", "task_query_invalid")
	}
	key := in
	key.Cursor = ""
	owner, hash := conversationOwner(a), conversationHash(key)
	cursor := conversationTaskCursor{Owner: owner, Query: hash, Cutoff: time.Now().UnixMilli()}
	filters := []query.Predicate{query.Equal("owner_key", owner), query.LessThanOrEqual("created_at", cursor.Cutoff)}
	if in.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Owner != owner || cursor.Query != hash || !personalMemoryKey(cursor.ID) || cursor.Created < 1 || cursor.Cutoff < cursor.Created {
			return out, conversationError("bad_request", "task_cursor_invalid")
		}
		filters = append(filters, query.Or(query.LessThan("created_at", cursor.Created), query.And(query.Equal("created_at", cursor.Created), query.LessThan("task_id", cursor.ID))))
	}
	if in.Status != "" {
		filters = append(filters, query.Equal("status", in.Status))
	}
	if in.SourceConversationID != "" {
		filters = append(filters, query.Equal("source_conversation_id", in.SourceConversationID))
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(filters...)).OrderBy(query.Descending("created_at"), query.Descending("task_id")).Limit(301).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	scanned := 0
	for rows.Next() {
		if scanned >= 300 || len(out.Items) >= in.Limit {
			out.Complete = false
			break
		}
		row, scanErr := scanConversationTask(rows)
		if scanErr != nil {
			return out, scanErr
		}
		scanned++
		cursor.ID, cursor.Created = row.task.ID, row.task.CreatedAt.UnixMilli()
		if strings.Contains(strings.ToLower(row.task.Goal), strings.ToLower(in.Query)) {
			out.Items = append(out.Items, row.task)
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if !out.Complete {
		raw, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}

func (s *ConversationStore) transitionQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority, resume bool) (agentsdk.ConversationTask, error) {
	var out agentsdk.ConversationTask
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", taskID))).Build()
		if err != nil {
			return err
		}
		row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
		if err != nil {
			return err
		}
		out = row.task
		from := out.Status
		if resume {
			if from == agentsdk.ConversationTaskStatusQueued {
				return nil
			}
			if from != agentsdk.ConversationTaskStatusCancelled || out.ExecutionRunID != "" {
				return conversationError("conflict", "task_resume_invalid")
			}
			out.Status, out.ErrorCode, out.CompletedAt = agentsdk.ConversationTaskStatusQueued, "", nil
			out.CompletionEventID, out.CompletionEventSeq = "", 0
		} else {
			if out.Terminal() {
				return nil
			}
			if from != agentsdk.ConversationTaskStatusQueued || out.ExecutionRunID != "" {
				return conversationError("conflict", "task_cancel_invalid")
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			out.Status, out.CompletedAt = agentsdk.ConversationTaskStatusCancelled, &now
		}
		out.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
		q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", out.Status).Set("updated_at", out.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("task_id", taskID), query.Equal("status", from))).Build()
		return conversationCAS(ctx, tx, q, args, err)
	})
	return out, err
}

func (s *ConversationStore) CancelQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return s.transitionQueuedConversationTask(ctx, taskID, a, false)
}

func (s *ConversationStore) ResumeQueuedConversationTask(ctx context.Context, taskID string, a agentsdk.ConversationAuthority) (agentsdk.ConversationTask, error) {
	return s.transitionQueuedConversationTask(ctx, taskID, a, true)
}

func (s *ConversationStore) LaunchConversationTask(ctx context.Context, runtimeID string) (agentpersistence.ConversationTaskLaunch, bool, error) {
	var launch agentpersistence.ConversationTaskLaunch
	// The worker spends almost all of its life with an empty queue. Avoid
	// opening a serializable transaction for that case: SQLite can otherwise
	// make the read transaction contend with large event writes from an
	// unrelated foreground run. The transaction below repeats the predicate,
	// so this hint does not participate in correctness or claiming.
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns("task_id").Where(query.And(query.Equal("runtime_id", runtimeID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).Limit(1).Build()
	if err != nil {
		return launch, false, err
	}
	var queued string
	if err = s.store.Database().QueryRowContext(ctx, q, args...).Scan(&queued); errors.Is(err, sql.ErrNoRows) {
		return launch, false, nil
	} else if err != nil {
		return launch, false, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("runtime_id", runtimeID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).OrderBy(query.Ascending("created_at"), query.Ascending("task_id")).Limit(32).Build()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		candidates := []conversationTaskRow{}
		for rows.Next() {
			row, scanErr := scanConversationTask(rows)
			if scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			candidates = append(candidates, row)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			task, authority := candidate.task, candidate.authority
			if conversationAuthority(authority) != nil || authority.RuntimeID != runtimeID || task.Status != agentsdk.ConversationTaskStatusQueued || task.SourceConversationID == "" || candidate.schedule == nil && task.SourceRunID == "" {
				continue
			}
			if candidate.schedule != nil && (!scheduledConversationStoreKey(candidate.schedule.PlanID) || !scheduledConversationStoreKey(candidate.schedule.SchedulerRunID) || candidate.schedule.ScheduledFor.IsZero()) {
				continue
			}
			conversation, getErr := s.get(ctx, tx, task.SourceConversationID, authority)
			if getErr != nil {
				var coded *agentsdk.Error
				if errors.As(getErr, &coded) && coded.Class == "not_found" {
					continue
				}
				return getErr
			}
			if conversation.ActiveRunID != "" {
				continue
			}
			if task.SourceRunID != "" {
				if _, sourceErr := s.runRow(ctx, tx, task.SourceConversationID, task.SourceRunID, authority); sourceErr != nil {
					return sourceErr
				}
			}
			messageText := agentsdk.ConversationTaskPrompt(task)
			now := time.Now().UTC().Truncate(time.Millisecond)
			run := conversationRunRow{Authority: authority, Run: agentsdk.ConversationRun{
				ID: conversationID("crun_"), ConversationID: conversation.ID, ClientMessageID: "background_" + task.ID,
				RequestHash: conversationHash([]any{task.ID, task.Goal, task.Input, task.ToolScope, task.Budget, task.FollowUp}), Status: "queued", UserSeq: conversation.LastSeq + 1,
				BackgroundTask: &agentsdk.ConversationTaskExecution{TaskID: task.ID, ToolScope: append([]agentsdk.ConversationTaskToolScope(nil), task.ToolScope...), Budget: task.Budget, FollowUp: task.FollowUp}, CreatedAt: now, UpdatedAt: now,
			}}
			message := agentsdk.ConversationMessage{
				ID: conversationID("msg_"), ConversationID: conversation.ID, RunID: run.Run.ID, Seq: run.Run.UserSeq,
				Role: "user", Content: messageText, BackgroundTaskID: task.ID, CreatedAt: now,
			}
			// A background run joins the source history but never occupies the
			// foreground activity slot. New user messages can proceed while the
			// existing worker executes this independently.
			conversation.LastSeq = message.Seq
			if err = s.save(ctx, tx, conversation, conversation.Revision, authority); err != nil {
				return err
			}
			if err = s.insertMessage(ctx, tx, message, authority); err != nil {
				return err
			}
			if err = s.event(ctx, tx, &run, "run.queued", map[string]any{"message_id": message.ID, "message_seq": message.Seq, "background_task_id": task.ID}); err != nil {
				return err
			}
			q, args, err = query.NewInsertBuilder(s.store.Renderer(), "_agent_conversation_runs").Columns("owner_key", "conversation_id", "run_id", "client_message_id", "runtime_id", "authority_json", "request_hash", "status", "lease_owner", "fence", "lease_expires_at", "event_seq", "created_at", "payload_json").Values(conversationOwner(authority), conversation.ID, run.Run.ID, run.Run.ClientMessageID, authority.RuntimeID, conversationJSON(authority), run.Run.RequestHash, run.Run.Status, "", 0, 0, run.EventSeq, now.UnixMilli(), conversationJSON(run.Run)).Build()
			if err = conversationExec(ctx, tx, q, args, err); err != nil {
				return err
			}
			task.Status, task.ExecutionRunID, task.UpdatedAt = agentsdk.ConversationTaskStatusRunning, run.Run.ID, now
			q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(query.Equal("owner_key", conversationOwner(authority)), query.Equal("task_id", task.ID), query.Equal("status", agentsdk.ConversationTaskStatusQueued))).Build()
			if err = conversationCAS(ctx, tx, q, args, err); err != nil {
				return err
			}
			launch = agentpersistence.ConversationTaskLaunch{Task: task, Run: run.Run, Authority: authority}
			return nil
		}
		return nil
	})
	return launch, launch.Task.ID != "", err
}

func scheduledConversationStoreKey(value string) bool {
	if value == "" || len(value) > 191 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

func (s *ConversationStore) finishConversationTask(ctx context.Context, tx *sql.Tx, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority, resultContent string) error {
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" {
		return nil
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(authority)), query.Equal("task_id", run.BackgroundTask.TaskID), query.Equal("source_conversation_id", run.ConversationID))).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	task := row.task
	if conversationOwner(row.authority) != conversationOwner(authority) || task.Status != agentsdk.ConversationTaskStatusRunning || task.ExecutionRunID != run.ID {
		return conversationError("conflict", "task_run_changed")
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	task.Status, task.ResultMessageID, task.ErrorCode, task.UpdatedAt, task.CompletedAt = agentsdk.ConversationTaskStatusFailed, run.AssistantMessageID, run.ErrorCode, now, &now
	if run.Status == "completed" {
		task.Status = agentsdk.ConversationTaskStatusCompleted
	} else if run.Status == "cancelled" {
		task.Status = agentsdk.ConversationTaskStatusCancelled
	}
	// Finish has already inserted run.<terminal> in this transaction. Keep its
	// stable owner-scoped event reference on the task in the same CAS write;
	// retrying Finish cannot create a second event or overwrite this receipt.
	task.CompletionEventSeq = run.LastEventSeq
	task.CompletionEventID = "task_event_" + conversationHash([]any{task.ID, run.ID, run.LastEventSeq, run.Status})[:32]
	row.task = task
	if task, err = s.recordConversationFollowUpTerminal(ctx, tx, row, run, resultContent, now); err != nil {
		return err
	}
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(query.Equal("owner_key", conversationOwner(row.authority)), query.Equal("task_id", task.ID), query.Equal("status", agentsdk.ConversationTaskStatusRunning))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

func (s *ConversationStore) resumeConversationTask(ctx context.Context, tx *sql.Tx, run agentsdk.ConversationRun, authority agentsdk.ConversationAuthority) error {
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" || run.Status != "queued" {
		return nil
	}
	q, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(query.And(query.Equal("owner_key", conversationOwner(authority)), query.Equal("task_id", run.BackgroundTask.TaskID))).Build()
	if err != nil {
		return err
	}
	row, err := scanConversationTask(tx.QueryRowContext(ctx, q, args...))
	if err != nil {
		return err
	}
	task := row.task
	if task.Status == agentsdk.ConversationTaskStatusRunning {
		return nil
	}
	if task.ExecutionRunID != run.ID || task.Status != agentsdk.ConversationTaskStatusFailed && task.Status != agentsdk.ConversationTaskStatusCancelled {
		return conversationError("conflict", "task_resume_invalid")
	}
	from := task.Status
	task.Status, task.ErrorCode, task.CompletedAt, task.ResultMessageID = agentsdk.ConversationTaskStatusRunning, "", nil, ""
	task.CompletionEventID, task.CompletionEventSeq, task.UpdatedAt = "", 0, time.Now().UTC().Truncate(time.Millisecond)
	q, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", task.Status).Set("updated_at", task.UpdatedAt.UnixMilli()).Set("payload_json", conversationJSON(task)).Where(query.And(query.Equal("owner_key", conversationOwner(authority)), query.Equal("task_id", task.ID), query.Equal("status", from))).Build()
	return conversationCAS(ctx, tx, q, args, err)
}

var _ agentpersistence.ConversationTaskMutationRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskWorkerRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskReadRepository = (*ConversationStore)(nil)
var _ agentpersistence.ConversationTaskControlRepository = (*ConversationStore)(nil)
