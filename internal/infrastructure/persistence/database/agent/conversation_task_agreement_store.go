package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	persistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

type conversationTaskAgreementUpdateRecord struct {
	RequestHash string                              `json:"request_hash"`
	Update      sdk.ConversationTaskAgreementUpdate `json:"update"`
	Task        sdk.ConversationTask                `json:"task"`
}

func (s *ConversationStore) readConversationTaskAgreementUpdate(ctx context.Context, tx *sql.Tx, owner, taskID, clientID string) (conversationTaskAgreementUpdateRecord, bool, error) {
	var raw []byte
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemAgreement), query.Equal("subject_id", taskID), query.Equal("reference_id", conversationTaskItemReference(taskID, clientID)))).Build()
	if err != nil {
		return conversationTaskAgreementUpdateRecord{}, false, err
	}
	err = tx.QueryRowContext(ctx, statement, args...).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return conversationTaskAgreementUpdateRecord{}, false, nil
	}
	if err != nil {
		return conversationTaskAgreementUpdateRecord{}, false, err
	}
	var out conversationTaskAgreementUpdateRecord
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, false, err
	}
	normalizeConversationTaskGoal(&out.Task)
	return out, true, nil
}

func appendConversationTaskPreviousRun(task *sdk.ConversationTask) {
	if task.ExecutionRunID == "" {
		return
	}
	conversationID := task.ExecutionConversationID
	if conversationID == "" {
		conversationID = task.SourceConversationID
	}
	ref := sdk.ConversationRunReference{ConversationID: conversationID, RunID: task.ExecutionRunID}
	for _, existing := range task.PreviousExecutionRuns {
		if existing.ConversationID == ref.ConversationID && existing.RunID == ref.RunID {
			return
		}
	}
	task.PreviousExecutionRuns = append(task.PreviousExecutionRuns, ref)
}

func validStoredConversationTaskBrief(brief sdk.ConversationTaskBrief) bool {
	if brief.Version < 1 || !executionText(brief.Goal, 2048, true) || !executionText(brief.Deliverable, 2048, true) || !executionText(brief.Audience, 512, true) || len(brief.Constraints) > 32 || len(brief.CompletionConditions) < 1 || len(brief.CompletionConditions) > 32 || len(brief.Assumptions) > 32 || brief.DueAt != nil && brief.DueAt.IsZero() || execution.ValidateCompletionRules(brief) != nil {
		return false
	}
	for _, group := range [][]string{brief.Constraints, brief.CompletionConditions, brief.Assumptions} {
		for _, value := range group {
			if !executionText(value, 2048, true) {
				return false
			}
		}
	}
	allowed := map[string]bool{"goal": true, "deliverable": true, "audience": true, "constraints": true, "completion_conditions": true, "assumptions": true, "due_at": true}
	seen := map[string]bool{}
	for _, fields := range [][]string{brief.ExplicitFields, brief.InferredFields} {
		for _, field := range fields {
			if !allowed[field] || seen[field] {
				return false
			}
			seen[field] = true
		}
	}
	for _, field := range []string{"goal", "deliverable", "audience", "constraints", "completion_conditions", "assumptions"} {
		if !seen[field] {
			return false
		}
	}
	if brief.DueAt != nil && !seen["due_at"] {
		return false
	}
	raw, err := json.Marshal(brief)
	return err == nil && len(raw) <= 8192
}

func (s *ConversationStore) UpdateConversationTaskAgreement(ctx context.Context, taskID string, in sdk.ConversationTaskAgreementUpdate, a sdk.ConversationAuthority) (sdk.ConversationTask, bool, error) {
	var out sdk.ConversationTask
	replay := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := conversationAuthority(a); err != nil {
			return err
		}
		if !personalMemoryKey(taskID) || !personalMemoryKey(in.ClientID) || in.ExpectedRevision < 1 || !executionText(in.Reason, 4096, true) || !validStoredConversationTaskBrief(in.Brief) {
			return conversationError("bad_request", "task_agreement_invalid")
		}
		owner, requestHash := conversationOwner(a), conversationHash(in)
		if existing, found, err := s.readConversationTaskAgreementUpdate(ctx, tx, owner, taskID, in.ClientID); err != nil {
			return err
		} else if found {
			if existing.RequestHash != requestHash {
				return conversationError("conflict", "task_agreement_idempotency_conflict")
			}
			out, replay = existing.Task, true
			return nil
		}
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns(conversationTaskColumns...).Where(conversationTaskPredicate(owner, taskID)).Build()
		if err != nil {
			return err
		}
		row, err := scanConversationTask(tx.QueryRowContext(ctx, statement, args...))
		if err != nil {
			return err
		}
		out = row.task
		if out.DelegationID != "" {
			return conversationError("conflict", "task_agreement_delegated")
		}
		if out.Status == sdk.ConversationTaskStatusRunning {
			return conversationError("conflict", "task_agreement_active")
		}
		if out.Status == sdk.ConversationTaskStatusCompleted {
			return conversationError("conflict", "task_completed")
		}
		currentVersion := int64(1)
		if out.Brief != nil {
			currentVersion = max(1, out.Brief.Version)
		}
		if in.ExpectedRevision != max(1, out.AgreementRevision) || in.Brief.Version != currentVersion+1 {
			return conversationError("conflict", "task_agreement_changed")
		}
		fromStatus, fromUpdated := out.Status, out.UpdatedAt
		if out.ExecutionRunID != "" {
			appendConversationTaskPreviousRun(&out)
			out.ExecutionRunID = ""
			out.Status = sdk.ConversationTaskStatusCancelled
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		brief := in.Brief
		changedFields := conversationPlanBriefFields(out.Brief, brief)
		out.Goal, out.Brief, out.AgreementRevision, out.UpdatedAt = brief.Goal, &brief, max(1, out.AgreementRevision)+1, now
		out.CompletionMode = sdk.ConversationTaskCompletionModeAssessed
		out.ErrorCode, out.ResultMessageID, out.CompletedAt, out.CompletionEventID, out.CompletionEventSeq = "", "", nil, "", 0
		out.GoalProgress.RemainingItems, out.GoalProgress.CompletedItems = append([]string(nil), brief.CompletionConditions...), []string{}
		if err = s.supersedeConversationTaskPlan(ctx, tx, owner, &out, out.AgreementRevision, changedFields, "Task agreement changed: "+strings.TrimSpace(in.Reason)); err != nil {
			return err
		}
		setConversationTaskGoalPhase(&out, now)
		statement, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("status", out.Status).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(out)).Where(query.And(conversationTaskPredicate(owner, taskID), query.Equal("status", fromStatus), query.Equal("updated_at", fromUpdated.UnixMilli()))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return err
		}
		record := conversationTaskAgreementUpdateRecord{RequestHash: requestHash, Update: in, Task: out}
		reference := conversationTaskItemReference(taskID, in.ClientID)
		statement, args, err = query.NewInsertBuilder(s.store.Renderer(), conversationItemTable).Columns("owner_key", "conversation_id", "item_kind", "item_key", "reference_id", "subject_id", "run_id", "seq", "payload_json").Values(owner, out.SourceConversationID, conversationItemAgreement, reference, reference, taskID, out.SourceRunID, out.AgreementRevision, conversationJSON(record)).Build()
		return conversationExec(ctx, tx, statement, args, err)
	})
	return out, replay, err
}

var _ persistence.ConversationTaskAgreementRepository = (*ConversationStore)(nil)
