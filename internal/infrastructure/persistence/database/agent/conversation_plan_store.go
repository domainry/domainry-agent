package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent/internal/execution"
	"github.com/domainry/domainry-orm/query"
)

var storedPlanAgreementFields = []string{"assumptions", "audience", "completion_conditions", "constraints", "deliverable", "due_at", "goal"}

func storedPlanStatus(status string) bool {
	switch status {
	case sdk.ConversationPlanStepPending, sdk.ConversationPlanStepInProgress, sdk.ConversationPlanStepCompleted, sdk.ConversationPlanStepBlocked, sdk.ConversationPlanStepSkipped, sdk.ConversationPlanStepNeedsReview:
		return true
	}
	return false
}

func storedPlanField(field string) bool {
	for _, value := range append(append([]string(nil), storedPlanAgreementFields...), "input", "dependencies") {
		if field == value {
			return true
		}
	}
	return false
}

func validStoredConversationPlan(plan sdk.ConversationPlan) bool {
	if !personalMemoryKey(plan.TaskID) || plan.Version < 1 || plan.AgreementRevision < 1 || !executionText(plan.Reason, 2048, true) || plan.Source == nil || !personalMemoryKey(plan.Source.ConversationID) || !personalMemoryKey(plan.Source.RunID) || plan.Source.BeforeStep < 1 || len(plan.Steps) < 1 || len(plan.Steps) > 32 {
		return false
	}
	ids, active := map[string]bool{}, 0
	for _, step := range plan.Steps {
		if !personalMemoryKey(step.ID) || ids[step.ID] || !executionText(step.Title, 512, true) || !storedPlanStatus(step.Status) || !executionText(step.Input, 2048, false) || !executionText(step.ExpectedOutput, 2048, true) || !personalMemoryKey(step.Executor.AgentID) || !personalMemoryKey(step.Executor.RunID) || len(step.DependsOn) > 32 || len(step.RequirementFields) < 1 || len(step.RequirementFields) > 9 || len(step.Evidence) > 32 || len(step.Artifacts) > 32 || !executionText(step.Outcome, 2048, false) || !executionText(step.Blocker, 2048, false) {
			return false
		}
		ids[step.ID] = true
		if step.Status == sdk.ConversationPlanStepInProgress {
			active++
		}
		if step.Status == sdk.ConversationPlanStepCompleted && strings.TrimSpace(step.Outcome) == "" || step.Status == sdk.ConversationPlanStepBlocked && strings.TrimSpace(step.Blocker) == "" {
			return false
		}
		for index, field := range step.RequirementFields {
			if !storedPlanField(field) || index > 0 && step.RequirementFields[index-1] >= field {
				return false
			}
		}
		for _, ref := range step.Evidence {
			if !personalMemoryKey(ref.ConversationID) || !personalMemoryKey(ref.RunID) || !personalMemoryKey(ref.CallID) || ref.Step < 0 || !artifactSHA(ref.SHA256) {
				return false
			}
		}
		for _, ref := range step.Artifacts {
			if !personalMemoryKey(ref.ID) || ref.Version < 1 || !artifactSHA(ref.SHA256) {
				return false
			}
		}
	}
	if active > 1 {
		return false
	}
	state := map[string]uint8{}
	var visit func(string) bool
	byID := map[string]sdk.ConversationPlanStep{}
	for _, step := range plan.Steps {
		byID[step.ID] = step
	}
	visit = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		seen := map[string]bool{}
		for _, dependency := range byID[id].DependsOn {
			if dependency == id || seen[dependency] {
				return false
			}
			seen[dependency] = true
			if _, ok := byID[dependency]; !ok || !visit(dependency) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range byID {
		if !visit(id) {
			return false
		}
	}
	for _, step := range plan.Steps {
		if step.Status != sdk.ConversationPlanStepInProgress && step.Status != sdk.ConversationPlanStepCompleted {
			continue
		}
		for _, dependency := range step.DependsOn {
			status := byID[dependency].Status
			if status != sdk.ConversationPlanStepCompleted && status != sdk.ConversationPlanStepSkipped {
				return false
			}
		}
	}
	raw, err := marshalDurableJSON(plan)
	return err == nil && len(raw) <= 256*1024
}

func planMatchesUpdate(plan sdk.ConversationPlan, update sdk.ConversationPlanUpdate, run sdk.ConversationRun) bool {
	if plan.Source == nil || plan.Version != update.ExpectedVersion+1 || plan.AgreementRevision != update.AgreementRevision || plan.Reason != strings.TrimSpace(update.Reason) || len(plan.Steps) != len(update.Steps) || plan.Source.ConversationID != run.ConversationID || plan.Source.RunID != run.ID {
		return false
	}
	for index, input := range update.Steps {
		step := plan.Steps[index]
		fields := append([]string(nil), input.RequirementFields...)
		if len(fields) == 0 {
			fields = append([]string(nil), storedPlanAgreementFields...)
		}
		sort.Strings(fields)
		if step.ID != input.ID || step.Title != strings.TrimSpace(input.Title) || step.Status != input.Status || step.Input != strings.TrimSpace(input.Input) || step.ExpectedOutput != strings.TrimSpace(input.ExpectedOutput) || step.Outcome != strings.TrimSpace(input.Outcome) || step.Blocker != strings.TrimSpace(input.Blocker) || step.Executor.RunID == "" || step.Executor.RunID != run.ID && step.Status != sdk.ConversationPlanStepCompleted || conversationHash(step.DependsOn) != conversationHash(input.DependsOn) || conversationHash(step.RequirementFields) != conversationHash(fields) || conversationHash(step.Evidence) != conversationHash(input.Evidence) || conversationHash(step.Artifacts) != conversationHash(input.Artifacts) {
			return false
		}
	}
	return true
}

func conversationTaskPlanReference(taskID, clientID string) string {
	return conversationHash([]string{taskID, clientID})
}

func conversationTaskPlanItemKey(taskID string, version int64) string {
	return conversationHash([]any{taskID, version})
}

func conversationTaskPlanScope(task *sdk.ConversationTask, source *sdk.ConversationRunReference) (string, string) {
	conversationID, runID := task.ExecutionConversationID, task.ExecutionRunID
	if (conversationID == "" || runID == "") && source != nil {
		conversationID, runID = source.ConversationID, source.RunID
	}
	if (conversationID == "" || runID == "") && len(task.PreviousExecutionRuns) > 0 {
		previous := task.PreviousExecutionRuns[len(task.PreviousExecutionRuns)-1]
		conversationID, runID = previous.ConversationID, previous.RunID
	}
	if conversationID == "" {
		conversationID = task.SourceConversationID
	}
	if runID == "" {
		runID = task.SourceRunID
	}
	return conversationID, runID
}

func (s *ConversationStore) insertConversationTaskPlan(ctx context.Context, tx *sql.Tx, owner, conversationID, runID, clientID string, plan sdk.ConversationPlan) error {
	if conversationID == "" || runID == "" {
		return conversationError("conflict", "plan_source_invalid")
	}
	statement, args, err := query.NewInsertBuilder(s.store.Renderer(), conversationItemTable).
		Columns("owner_key", "conversation_id", "item_kind", "item_key", "reference_id", "subject_id", "run_id", "seq", "payload_json").
		Values(owner, conversationID, conversationItemTaskPlan, conversationTaskPlanItemKey(plan.TaskID, plan.Version), conversationTaskPlanReference(plan.TaskID, clientID), plan.TaskID, runID, plan.Version, conversationJSON(plan)).Build()
	return conversationExec(ctx, tx, statement, args, err)
}

func (s *ConversationStore) conversationTaskIDsForSource(ctx context.Context, db conversationDB, owner, conversationID string) ([]string, error) {
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationTaskTable).Columns("task_id").Where(query.And(conversationTaskKindPredicate(conversationTaskKindTask), query.Equal("owner_key", owner), query.Equal("source_conversation_id", conversationID))).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *ConversationStore) conversationTaskPlanPayloadsForSource(ctx context.Context, db conversationDB, owner, conversationID string) ([]json.RawMessage, error) {
	ids, err := s.conversationTaskIDsForSource(ctx, db, owner, conversationID)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	values := make([]any, len(ids))
	for index := range ids {
		values[index] = ids[index]
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("conversation_id", "payload_json").Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemTaskPlan), query.In("subject_id", values...))).OrderBy(query.Ascending("subject_id"), query.Ascending("seq")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var itemConversationID string
		var raw []byte
		if err = rows.Scan(&itemConversationID, &raw); err != nil {
			return nil, err
		}
		// Items already selected by the conversation graph are not emitted a
		// second time. This branch only closes over task history written in a
		// delegated/execution conversation.
		if itemConversationID == conversationID {
			continue
		}
		out = append(out, append(json.RawMessage(nil), raw...))
	}
	return out, rows.Err()
}

func (s *ConversationStore) deleteConversationTaskPlansForSource(ctx context.Context, tx *sql.Tx, owner, conversationID string) error {
	ids, err := s.conversationTaskIDsForSource(ctx, tx, owner, conversationID)
	if err != nil || len(ids) == 0 {
		return err
	}
	values := make([]any, len(ids))
	for index := range ids {
		values[index] = ids[index]
	}
	statement, args, err := query.NewDeleteBuilder(s.store.Renderer(), conversationItemTable).Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemTaskPlan), query.In("subject_id", values...))).Build()
	return conversationExec(ctx, tx, statement, args, err)
}

func (s *ConversationStore) ApplyConversationTaskPlanTool(ctx context.Context, in sdk.ConversationToolRequest, prepared sdk.ConversationPlan) (sdk.ConversationToolResult, error) {
	definition := sdk.ConversationPlanUpdateTool()
	return s.applyLocalTool(ctx, in, []sdk.ConversationToolDefinition{definition}, func(tx *sql.Tx, claim persistence.ConversationClaim, call persistence.ConversationToolExecution) (sdk.ConversationToolResult, error) {
		var update sdk.ConversationPlanUpdate
		if call.Call.Name != definition.Key || unmarshalDurableJSON([]byte(call.Call.Arguments), &update) != nil || !personalMemoryKey(update.ClientID) {
			return sdk.ConversationToolResult{}, conversationError("bad_request", "plan_invalid")
		}
		run, err := s.runRow(ctx, tx, claim.Run.ConversationID, claim.Run.ID, claim.Authority)
		if err != nil || run.Run.BackgroundTask == nil || run.Run.BackgroundTask.TaskID == "" {
			return sdk.ConversationToolResult{}, conversationError("conflict", "plan_task_invalid")
		}
		owner, taskID := conversationOwner(claim.Authority), run.Run.BackgroundTask.TaskID
		statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("seq").Where(query.And(query.Equal("owner_key", owner), query.Equal("item_kind", conversationItemTaskPlan), query.Equal("reference_id", conversationTaskPlanReference(taskID, update.ClientID)))).Build()
		if err != nil {
			return sdk.ConversationToolResult{}, err
		}
		var reused int64
		if err = tx.QueryRowContext(ctx, statement, args...).Scan(&reused); err == nil {
			return sdk.ConversationToolResult{}, conversationError("conflict", "plan_idempotency_conflict")
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
		currentVersion := int64(0)
		if row.task.Plan != nil {
			currentVersion = row.task.Plan.Version
		}
		executionConversationID := row.task.ExecutionConversationID
		if executionConversationID == "" {
			executionConversationID = row.task.SourceConversationID
		}
		if row.task.Status != sdk.ConversationTaskStatusRunning || row.task.ExecutionRunID != run.Run.ID || executionConversationID != run.Run.ConversationID || currentVersion != update.ExpectedVersion || max(1, row.task.AgreementRevision) != update.AgreementRevision || prepared.TaskID != taskID {
			return sdk.ConversationToolResult{}, conversationError("conflict", "plan_changed")
		}
		if !planMatchesUpdate(prepared, update, run.Run) {
			return sdk.ConversationToolResult{}, conversationError("conflict", "plan_payload_changed")
		}
		if !validStoredConversationPlan(prepared) {
			return sdk.ConversationToolResult{}, conversationError("bad_request", "plan_invalid")
		}
		if row.task.Plan != nil {
			for _, old := range row.task.Plan.Steps {
				if old.Status != sdk.ConversationPlanStepCompleted {
					continue
				}
				found := false
				for _, next := range prepared.Steps {
					if next.ID == old.ID {
						found = true
						if conversationHash(next) != conversationHash(old) {
							return sdk.ConversationToolResult{}, conversationError("conflict", "plan_completed_step_changed")
						}
					}
				}
				if !found {
					return sdk.ConversationToolResult{}, conversationError("conflict", "plan_completed_step_changed")
				}
			}
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		prepared.CreatedAt = now
		if err = s.insertConversationTaskPlan(ctx, tx, owner, run.Run.ConversationID, run.Run.ID, update.ClientID, prepared); err != nil {
			return sdk.ConversationToolResult{}, err
		}
		previousUpdated := row.task.UpdatedAt
		row.task.Plan, row.task.UpdatedAt = &prepared, now
		statement, args, err = query.NewUpdateBuilder(s.store.Renderer(), conversationTaskTable).Set("updated_at", now.UnixMilli()).Set("payload_json", conversationJSON(row.task)).Where(query.And(conversationTaskPredicate(owner, taskID), query.Equal("status", sdk.ConversationTaskStatusRunning), query.Equal("updated_at", previousUpdated.UnixMilli()))).Build()
		if err = conversationCAS(ctx, tx, statement, args, err); err != nil {
			return sdk.ConversationToolResult{}, err
		}
		return sdk.ConversationToolResult{Status: "completed", ResourceID: taskID, Content: conversationAPIJSON(map[string]any{"plan": prepared})}, nil
	})
}

func (s *ConversationStore) ConversationTaskPlans(ctx context.Context, taskID string, before int64, a sdk.ConversationAuthority) (sdk.ConversationPlanHistory, error) {
	out := sdk.ConversationPlanHistory{Items: []sdk.ConversationPlan{}, Complete: true}
	if err := conversationAuthority(a); err != nil {
		return out, err
	}
	if !personalMemoryKey(taskID) || before < 0 {
		return out, conversationError("bad_request", "plan_query_invalid")
	}
	if _, err := s.ConversationTask(ctx, taskID, a); err != nil {
		return out, err
	}
	predicate := query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("item_kind", conversationItemTaskPlan), query.Equal("subject_id", taskID))
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
		var plan sdk.ConversationPlan
		if err = rows.Scan(&raw); err == nil {
			err = unmarshalDurableJSON(raw, &plan)
		}
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, plan)
	}
	if len(out.Items) > 20 {
		out.Complete = false
		out.Items = out.Items[:20]
		out.NextBefore = out.Items[len(out.Items)-1].Version
	}
	return out, rows.Err()
}

func (s *ConversationStore) ConversationTaskPlan(ctx context.Context, taskID string, version int64, a sdk.ConversationAuthority) (sdk.ConversationPlan, error) {
	if err := conversationAuthority(a); err != nil {
		return sdk.ConversationPlan{}, err
	}
	if !personalMemoryKey(taskID) || version < 1 {
		return sdk.ConversationPlan{}, conversationError("bad_request", "plan_query_invalid")
	}
	statement, args, err := query.NewSelectBuilder(s.store.Renderer(), conversationItemTable).Columns("payload_json").Where(query.And(query.Equal("owner_key", conversationOwner(a)), query.Equal("item_kind", conversationItemTaskPlan), query.Equal("subject_id", taskID), query.Equal("seq", version))).Build()
	if err != nil {
		return sdk.ConversationPlan{}, err
	}
	var raw []byte
	if err = s.store.Database().QueryRowContext(ctx, statement, args...).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return sdk.ConversationPlan{}, conversationError("not_found", "plan_not_found")
	} else if err != nil {
		return sdk.ConversationPlan{}, err
	}
	var plan sdk.ConversationPlan
	if err = unmarshalDurableJSON(raw, &plan); err != nil {
		return sdk.ConversationPlan{}, err
	}
	return plan, nil
}

func planFieldsAffected(step sdk.ConversationPlanStep, fields []string) bool {
	for _, changed := range fields {
		for _, used := range step.RequirementFields {
			if changed == used {
				return true
			}
		}
	}
	return false
}

func (s *ConversationStore) supersedeConversationTaskPlan(ctx context.Context, tx *sql.Tx, owner string, task *sdk.ConversationTask, revision int64, fields []string, reason string) error {
	if task.Plan == nil {
		return nil
	}
	raw, _ := marshalDurableJSON(task.Plan)
	var plan sdk.ConversationPlan
	if unmarshalDurableJSON(raw, &plan) != nil {
		return conversationError("conflict", "plan_invalid")
	}
	conversationID, runID := conversationTaskPlanScope(task, plan.Source)
	plan.Version++
	plan.AgreementRevision = max(1, revision)
	plan.Reason = reason
	plan.Source = nil
	plan.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	affected := map[string]bool{}
	for _, step := range plan.Steps {
		if planFieldsAffected(step, fields) {
			affected[step.ID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, step := range plan.Steps {
			if affected[step.ID] {
				continue
			}
			for _, dependency := range step.DependsOn {
				if affected[dependency] {
					affected[step.ID], changed = true, true
					break
				}
			}
		}
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.Status != sdk.ConversationPlanStepCompleted && affected[step.ID] {
			step.Status, step.Blocker = sdk.ConversationPlanStepNeedsReview, reason
		}
	}
	clientID := "system_" + conversationHash([]any{task.ID, plan.Version, revision, fields, reason})[:32]
	if err := s.insertConversationTaskPlan(ctx, tx, owner, conversationID, runID, clientID, plan); err != nil {
		return err
	}
	task.Plan = &plan
	return nil
}

func conversationPlanBriefFields(before *sdk.ConversationTaskBrief, after sdk.ConversationTaskBrief) []string {
	if before == nil {
		return append([]string(nil), storedPlanAgreementFields...)
	}
	return execution.ChangedBriefFields(*before, after)
}

func (s *ConversationStore) closeConversationTaskPlan(ctx context.Context, tx *sql.Tx, owner string, task *sdk.ConversationTask, run sdk.ConversationRun) error {
	if task.Plan == nil {
		return nil
	}
	raw, _ := marshalDurableJSON(task.Plan)
	var plan sdk.ConversationPlan
	if unmarshalDurableJSON(raw, &plan) != nil {
		return conversationError("conflict", "plan_invalid")
	}
	conversationID, runID := conversationTaskPlanScope(task, plan.Source)
	changed := false
	reason := run.ErrorCode
	if run.Status == "completed" {
		reason = "execution completed with unfinished plan steps"
	} else if reason == "" {
		reason = "execution " + run.Status
	}
	for index := range plan.Steps {
		status := plan.Steps[index].Status
		unfinishedCompletedRun := run.Status == "completed" && (status == sdk.ConversationPlanStepPending || status == sdk.ConversationPlanStepInProgress)
		unfinishedStoppedRun := run.Status != "completed" && status == sdk.ConversationPlanStepInProgress
		if !unfinishedCompletedRun && !unfinishedStoppedRun {
			continue
		}
		changed = true
		plan.Steps[index].Blocker = reason
		if run.Status == "cancelled" || run.Status == "completed" {
			plan.Steps[index].Status = sdk.ConversationPlanStepNeedsReview
		} else {
			plan.Steps[index].Status = sdk.ConversationPlanStepBlocked
		}
	}
	if !changed {
		return nil
	}
	plan.Version++
	plan.Reason = reason
	plan.Source = nil
	plan.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	clientID := "system_" + conversationHash([]any{task.ID, plan.Version, run.ID, run.Status, reason})[:32]
	if run.ConversationID != "" && run.ID != "" {
		conversationID, runID = run.ConversationID, run.ID
	}
	if err := s.insertConversationTaskPlan(ctx, tx, owner, conversationID, runID, clientID, plan); err != nil {
		return err
	}
	task.Plan = &plan
	return nil
}

var _ persistence.ConversationTaskPlanRepository = (*ConversationStore)(nil)
