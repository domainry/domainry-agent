package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

type storedConversationTaskCompletion struct {
	RequestHash string                               `json:"request_hash"`
	Completion  sdk.ConversationTaskCompletionRecord `json:"completion"`
	Task        sdk.ConversationTask                 `json:"task"`
}

func taskCompletionDelivery(submission sdk.ConversationTaskCompletionSubmission, briefVersion int64) sdk.ConversationDelegationDelivery {
	return sdk.ConversationDelegationDelivery{
		Conditions: submission.Conditions, AgreementRevision: int64(submission.AgreementRevision), BriefVersion: briefVersion,
		Summary: submission.Summary, Data: submission.Data, Evidence: []sdk.ConversationRunReference{}, Unresolved: []string{},
	}
}

func validStoredTaskCompletion(record sdk.ConversationTaskCompletionRecord) bool {
	if record.Revision < 1 || record.Kind == "" || record.Submission.AgreementRevision < 1 || !executionText(record.Submission.Summary, 8192, true) || len(record.Submission.Data) > 65536 || len(record.Submission.Data) > 0 && !json.Valid(record.Submission.Data) || record.Submission.Source == nil || !personalMemoryKey(record.Submission.Source.ConversationID) || !personalMemoryKey(record.Submission.Source.RunID) || record.Submission.Source.BeforeStep < 1 || len(record.Submission.Artifacts) > 32 {
		return false
	}
	for _, ref := range record.Submission.Artifacts {
		if !personalMemoryKey(ref.ID) || ref.Version < 1 || !artifactSHA(ref.SHA256) {
			return false
		}
	}
	raw, err := marshalDurableJSON(record)
	return err == nil && len(raw) <= 256*1024
}

func (s *ConversationStore) evaluateTaskCompletion(ctx context.Context, db conversationDB, task sdk.ConversationTask, submission sdk.ConversationTaskCompletionSubmission, review *sdk.ConversationDeliveryReview, method string, actor sdk.ConversationAuthority, source *sdk.ConversationRunReference) (sdk.ConversationDeliveryVerification, error) {
	if task.Brief == nil {
		return sdk.ConversationDeliveryVerification{}, conversationError("conflict", "task_completion_missing")
	}
	delivery := taskCompletionDelivery(submission, task.Brief.Version)
	out := sdk.ConversationDeliveryVerification{
		DeliveryDigest: conversationHash(delivery), BriefVersion: task.Brief.Version, AgreementRevision: max(1, task.AgreementRevision),
		ActorID: actor.UserID, Blockers: []string{}, Source: source, CheckedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	lookup := func(ref sdk.ConversationResultReference) (persistence.ConversationToolExecution, error) {
		var record persistence.ConversationToolExecution
		claim := persistence.ConversationClaim{Authority: actor, Run: sdk.ConversationRun{ID: ref.RunID, ConversationID: ref.ConversationID}}
		found, err := s.readExecutionTool(ctx, db, claim, ref.Step, ref.CallID, &record)
		if err != nil {
			return record, err
		}
		if !found || record.State != "completed" || record.Result == nil || conversationHash(record.Result) != ref.SHA256 {
			return record, conversationError("conflict", "completion_evidence_changed")
		}
		return record, nil
	}
	var err error
	out.Checks, out.Ready, err = execution.EvaluateCompletion(*task.Brief, delivery, review, method, lookup)
	if err != nil {
		var coded *sdk.Error
		if errors.As(err, &coded) {
			return out, err
		}
		return out, conversationError("bad_request", "completion_assessment_invalid")
	}
	for _, check := range out.Checks {
		if check.Verdict != "met" || check.Method == "recipient" || check.Method == "pending" {
			out.Blockers = append(out.Blockers, "completion_conditions_pending")
			break
		}
	}
	if submission.AgreementRevision != max(1, task.AgreementRevision) || task.Brief.Version != max(1, task.AgreementRevision) {
		out.Ready = false
		out.Blockers = append(out.Blockers, "completion_outdated")
	}
	return out, nil
}

func taskCompletionMatchesSubmit(record sdk.ConversationTaskCompletionRecord, submit sdk.ConversationTaskCompletionSubmit, run sdk.ConversationRun) bool {
	if record.Revision != submit.ExpectedRevision+1 || record.Kind != "agent_assessment" || record.Submission.AgreementRevision != submit.AgreementRevision || record.Submission.Summary != strings.TrimSpace(submit.Summary) || conversationHash(record.Submission.Data) != conversationHash(submit.Data) || conversationHash(record.Submission.Conditions) != conversationHash(submit.Conditions) || conversationHash(record.Submission.Artifacts) != conversationHash(submit.Artifacts) || record.Submission.Source == nil {
		return false
	}
	return record.Submission.Source.ConversationID == run.ConversationID && record.Submission.Source.RunID == run.ID
}

func (s *ConversationStore) insertTaskCompletion(ctx context.Context, tx *sql.Tx, owner, taskID, clientID string, value storedConversationTaskCompletion) error {
	conversationID, runID := value.Task.SourceConversationID, value.Task.SourceRunID
	if value.Completion.Submission.Source != nil {
		if conversationID == "" {
			conversationID = value.Completion.Submission.Source.ConversationID
		}
		if runID == "" {
			runID = value.Completion.Submission.Source.RunID
		}
	}
	itemKey := conversationTaskCompletionItemKey(taskID, value.Completion.Revision)
	reference := conversationTaskItemReference(taskID, clientID)
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationItemTable).Columns("owner_key", "conversation_id", "item_kind", "item_key", "reference_id", "subject_id", "run_id", "seq", "payload_json").Values(owner, conversationID, conversationItemCompletion, itemKey, reference, taskID, runID, value.Completion.Revision, conversationJSON(value)).Build()
	return conversationExec(ctx, tx, statement, args, err)
}

func (s *ConversationStore) ApplyConversationTaskCompletionTool(ctx context.Context, in sdk.ConversationToolRequest, prepared sdk.ConversationTaskCompletionRecord) (sdk.ConversationToolResult, error) {
	definition := sdk.ConversationTaskCompletionSubmitTool()
	return s.applyLocalTool(ctx, in, []sdk.ConversationToolDefinition{definition}, func(tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (sdk.ConversationToolResult, error) {
		var submit sdk.ConversationTaskCompletionSubmit
		if call.Call.Name != definition.Key || unmarshalDurableJSON([]byte(call.Call.Arguments), &submit) != nil || !personalMemoryKey(submit.ClientID) {
			return sdk.ConversationToolResult{}, conversationError("bad_request", "task_completion_invalid")
		}
		run, err := s.runRow(ctx, tx, claim.Run.ConversationID, claim.Run.ID, claim.Authority)
		if err != nil || run.Run.BackgroundTask == nil || run.Run.BackgroundTask.TaskID == "" {
			return sdk.ConversationToolResult{}, conversationError("conflict", "task_completion_run_invalid")
		}
		owner, taskID := conversationOwner(claim.Authority), run.Run.BackgroundTask.TaskID
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemCompletion), query.Equal("subject_id", taskID), query.Equal("reference_id", conversationTaskItemReference(taskID, submit.ClientID)))).Build()
		if err != nil {
			return sdk.ConversationToolResult{}, err
		}
		var prior []byte
		if err = tx.QueryRowContext(ctx, statement, args...).Scan(&prior); err == nil {
			return sdk.ConversationToolResult{}, conversationError("conflict", "task_completion_idempotency_conflict")
		} else if !errors.Is(err, sql.ErrNoRows) {
			return sdk.ConversationToolResult{}, err
		}
		statement, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(owner, taskID)).Build()
		if err != nil {
			return sdk.ConversationToolResult{}, err
		}
		row, err := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		if err != nil {
			return sdk.ConversationToolResult{}, err
		}
		currentRevision := int64(0)
		if row.task.Completion != nil {
			currentRevision = row.task.Completion.Revision
		}
		if row.task.Status != sdk.ConversationTaskStatusRunning || row.task.ExecutionRunID != run.Run.ID || row.task.CompletionMode != sdk.ConversationTaskCompletionModeAssessed || run.Run.BackgroundTask.CompletionMode != sdk.ConversationTaskCompletionModeAssessed || row.task.DelegationID != "" || row.task.FollowUp != nil || currentRevision != submit.ExpectedRevision || max(1, row.task.AgreementRevision) != submit.AgreementRevision || !taskCompletionMatchesSubmit(prepared, submit, run.Run) || !validStoredTaskCompletion(prepared) {
			return sdk.ConversationToolResult{}, conversationError("conflict", "task_completion_changed")
		}
		actorReview := &sdk.ConversationDeliveryReview{DeliveryDigest: conversationHash(taskCompletionDelivery(prepared.Submission, row.task.Brief.Version)), Conditions: prepared.Submission.Conditions}
		verification, err := s.evaluateTaskCompletion(ctx, tx, row.task, prepared.Submission, actorReview, "agent", claim.Authority, prepared.Submission.Source)
		if err != nil {
			return sdk.ConversationToolResult{}, err
		}
		if run.Run.Agent != nil {
			verification.AgentID = run.Run.Agent.ID
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.Verification, prepared.RecordedAt, prepared.Submission.SubmittedAt = verification, now, now
		previousUpdated := row.task.UpdatedAt
		row.task.Completion, row.task.UpdatedAt = &prepared, now
		statement, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(row.task)).Where(query.And(conversationTaskPredicate(owner, taskID), query.Equal("status", sdk.ConversationTaskStatusRunning), query.Equal("updated_at", previousUpdated.UnixMilli()))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return sdk.ConversationToolResult{}, err
		}
		requestHash := conversationHash(submit)
		if err = s.insertTaskCompletion(ctx, tx, owner, taskID, submit.ClientID, storedConversationTaskCompletion{RequestHash: requestHash, Completion: prepared, Task: row.task}); err != nil {
			return sdk.ConversationToolResult{}, err
		}
		return sdk.ConversationToolResult{Status: "completed", ResourceID: taskID, Content: conversationJSON(map[string]any{"completion": prepared})}, nil
	})
}

func (s *ConversationStore) ReviewConversationTaskCompletion(ctx context.Context, taskID string, in sdk.ConversationTaskCompletionReviewRequest, a sdk.ConversationAuthority) (sdk.ConversationTask, bool, error) {
	var out sdk.ConversationTask
	replayed := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		owner, requestHash := conversationOwner(a), conversationHash(in)
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemCompletion), query.Equal("subject_id", taskID), query.Equal("reference_id", conversationTaskItemReference(taskID, in.ClientID)))).Build()
		if err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, statement, args...).Scan(&raw); err == nil {
			var saved storedConversationTaskCompletion
			if unmarshalDurableJSON(raw, &saved) != nil || saved.RequestHash != requestHash {
				return conversationError("conflict", "task_completion_idempotency_conflict")
			}
			out, replayed = saved.Task, true
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		statement, args, err = query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(owner, taskID)).Build()
		if err != nil {
			return err
		}
		row, err := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		if err != nil {
			return err
		}
		if row.task.Status != sdk.ConversationTaskStatusAwaitingReview || row.task.Completion == nil || row.task.Completion.Revision != in.ExpectedRevision || in.Review.DeliveryDigest != row.task.Completion.Verification.DeliveryDigest || row.task.Completion.Submission.AgreementRevision != max(1, row.task.AgreementRevision) {
			return conversationError("conflict", "task_completion_changed")
		}
		method := "user"
		var source *sdk.ConversationRunReference
		if in.ToolRequest != nil {
			if in.ToolRequest.Authority.UserID != a.UserID || in.ToolRequest.Authority.WorkspaceID != a.WorkspaceID || in.ToolRequest.Call.Name != "task_review" {
				return conversationError("forbidden", "task_completion_actor_invalid")
			}
			method = "agent"
			value := sdk.ConversationRunReference{ConversationID: in.ToolRequest.ConversationID, RunID: in.ToolRequest.RunID, BeforeStep: in.ToolRequest.Step + 1}
			source = &value
		}
		verification, err := s.evaluateTaskCompletion(ctx, tx, row.task, row.task.Completion.Submission, &in.Review, method, a, source)
		if err != nil {
			return err
		}
		if in.ToolRequest != nil {
			run, runErr := s.runRow(ctx, tx, in.ToolRequest.ConversationID, in.ToolRequest.RunID, a)
			if runErr != nil {
				return runErr
			}
			if run.Run.Agent != nil {
				verification.AgentID = run.Run.Agent.ID
			}
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		completion := sdk.ConversationTaskCompletionRecord{Revision: row.task.Completion.Revision + 1, Kind: method + "_review", Submission: row.task.Completion.Submission, Verification: verification, Reason: strings.TrimSpace(in.Reason), RecordedAt: now}
		previousStatus, previousUpdated := row.task.Status, row.task.UpdatedAt
		row.task.Completion, row.task.UpdatedAt = &completion, now
		if verification.Ready {
			row.task.Status, row.task.ErrorCode, row.task.CompletedAt = sdk.ConversationTaskStatusCompleted, "", &now
			row.task.CompletionEventID = "task_completion_" + conversationHash([]any{taskID, completion.Revision, verification.DeliveryDigest})[:32]
			row.task.CompletionEventSeq = 0
		}
		setConversationTaskGoalPhase(&row.task, now)
		statement, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", row.task.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(row.task)).Where(query.And(conversationTaskPredicate(owner, taskID), query.Equal("status", previousStatus), query.Equal("updated_at", previousUpdated.UnixMilli()))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return err
		}
		if err = s.insertTaskCompletion(ctx, tx, owner, taskID, in.ClientID, storedConversationTaskCompletion{RequestHash: requestHash, Completion: completion, Task: row.task}); err != nil {
			return err
		}
		out = row.task
		return nil
	})
	return out, replayed, err
}

func (s *ConversationStore) ConversationTaskCompletionHistory(ctx context.Context, taskID string, before int64, a sdk.ConversationAuthority) (sdk.ConversationTaskCompletionHistory, error) {
	out := sdk.ConversationTaskCompletionHistory{Items: []sdk.ConversationTaskCompletionRecord{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if _, err := s.ConversationTask(ctx, taskID, a); err != nil {
		return out, err
	}
	predicate := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("item_kind", conversationItemCompletion), query.Equal("subject_id", taskID))
	if before > 0 {
		predicate = query.And(predicate, query.LessThan("seq", before))
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(predicate).OrderBy(query.Descending("seq")).Limit(21).Build()
	if err != nil {
		return out, err
	}
	rows, err := s.store.Database().QueryContext(ctx, statement, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var saved storedConversationTaskCompletion
		if err = rows.Scan(&raw); err == nil {
			err = unmarshalDurableJSON(raw, &saved)
		}
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, saved.Completion)
	}
	if len(out.Items) > 20 {
		out.Complete = false
		out.Items = out.Items[:20]
		out.NextBefore = out.Items[len(out.Items)-1].Revision
	}
	return out, rows.Err()
}

// finishConversationTaskCompletion records the execution-end boundary even
// when the model omitted completion_submit. Legacy tasks use their literal
// default condition (a saved response exists) as a small program check.
func (s *ConversationStore) finishConversationTaskCompletion(ctx context.Context, tx *sql.Tx, authority sdk.ConversationAuthority, task *sdk.ConversationTask, run sdk.ConversationRun, resultContent string) error {
	if task.DelegationID != "" || task.FollowUp != nil || run.Status != "completed" {
		return nil
	}
	if task.Completion != nil && task.Completion.Submission.AgreementRevision == max(1, task.AgreementRevision) && task.Completion.Submission.Source != nil && task.Completion.Submission.Source.RunID == run.ID {
		return nil
	}
	if task.Brief == nil {
		brief := sdk.DefaultConversationTaskBrief(task.Goal)
		task.Brief = &brief
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := sdk.ConversationRunReference{ConversationID: run.ConversationID, RunID: run.ID, BeforeStep: max(1, len(run.Steps))}
	conditions := make([]sdk.ConversationConditionAssessment, len(task.Brief.CompletionConditions))
	checks := make([]sdk.ConversationCompletionCheck, len(task.Brief.CompletionConditions))
	legacyReady := task.CompletionMode == sdk.ConversationTaskCompletionModeLegacyResponse && strings.TrimSpace(resultContent) != "" && run.AssistantMessageID != ""
	for index, requirement := range task.Brief.CompletionConditions {
		conditions[index] = sdk.ConversationConditionAssessment{Condition: index, Verdict: "unknown", Basis: "", Receipts: []sdk.ConversationResultReference{}}
		checks[index] = sdk.ConversationCompletionCheck{Condition: index, Requirement: requirement, Method: "pending", Verdict: "unknown", Basis: "等待逐项核对", Receipts: []sdk.ConversationResultReference{}}
		if legacyReady {
			conditions[index].Verdict, conditions[index].Basis = "met", "任务回复已生成并持久保存"
			checks[index].Method, checks[index].Verdict, checks[index].Basis = "program", "met", "任务回复已生成并持久保存"
		}
	}
	submission := sdk.ConversationTaskCompletionSubmission{AgreementRevision: max(1, task.AgreementRevision), Summary: strings.TrimSpace(resultContent), Conditions: conditions, Artifacts: []sdk.ConversationArtifactReference{}, Source: &source, SubmittedAt: now}
	if submission.Summary == "" {
		submission.Summary = "执行已结束，尚未提交可验收摘要"
	}
	delivery := taskCompletionDelivery(submission, task.Brief.Version)
	kind, blockers := "execution_end", []string{"completion_submission_missing"}
	if task.CompletionMode == sdk.ConversationTaskCompletionModeLegacyResponse {
		kind, blockers = "legacy_response", []string{}
		if !legacyReady {
			blockers = append(blockers, "completion_conditions_pending")
		}
	}
	verification := sdk.ConversationDeliveryVerification{DeliveryDigest: conversationHash(delivery), BriefVersion: task.Brief.Version, AgreementRevision: max(1, task.AgreementRevision), Checks: checks, Ready: legacyReady, Blockers: blockers, ActorID: authority.UserID, Source: &source, CheckedAt: now}
	record := sdk.ConversationTaskCompletionRecord{Revision: 1, Kind: kind, Submission: submission, Verification: verification, Reason: "Recorded when the execution run ended", RecordedAt: now}
	if task.Completion != nil {
		record.Revision = task.Completion.Revision + 1
	}
	task.Completion = &record
	clientID, requestHash := "system-finish-"+run.ID, conversationHash([]any{task.ID, run.ID, run.LastEventSeq, resultContent})
	return s.insertTaskCompletion(ctx, tx, conversationOwner(authority), task.ID, clientID, storedConversationTaskCompletion{RequestHash: requestHash, Completion: record, Task: *task})
}

var _ persistence.ConversationTaskCompletionRepository = (*ConversationStore)(nil)
