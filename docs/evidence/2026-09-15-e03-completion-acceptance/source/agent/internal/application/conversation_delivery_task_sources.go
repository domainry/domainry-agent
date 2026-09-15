package application

import (
	"context"
	"fmt"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func taskResultReadDefinition(key string) (sdk.ConversationToolDefinition, bool) {
	switch key {
	case "task_start", "task_get", "task_list", "task_cancel", "task_resume", "task_update", "task_review":
		for _, definition := range sdk.PersonalConversationTools() {
			if definition.Key == key {
				return definition, true
			}
		}
	}
	return sdk.ConversationToolDefinition{}, false
}

func taskCompletionAtRevision(ctx context.Context, repo persistence.ConversationTaskCompletionRepository, taskID string, revision int64, a sdk.ConversationAuthority) (sdk.ConversationTaskCompletionRecord, error) {
	if revision < 1 {
		return sdk.ConversationTaskCompletionRecord{}, invalidPersonalReceipt()
	}
	before := int64(0)
	for read := 0; read < 256; {
		history, err := repo.ConversationTaskCompletionHistory(ctx, taskID, before, a)
		if err != nil {
			return sdk.ConversationTaskCompletionRecord{}, err
		}
		for _, item := range history.Items {
			read++
			if item.Revision == revision {
				return item, nil
			}
		}
		if history.Complete {
			break
		}
		if history.NextBefore < 1 || history.NextBefore == before || len(history.Items) == 0 {
			return sdk.ConversationTaskCompletionRecord{}, invalidPersonalReceipt()
		}
		before = history.NextBefore
	}
	return sdk.ConversationTaskCompletionRecord{}, invalidPersonalReceipt()
}

func (audit *conversationSourceAudit) taskCompletionToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	definition := sdk.ConversationTaskCompletionSubmitTool()
	if record.Call.Name != definition.Key || conversationDigest(definition) != conversationDigest(record.Definition) || record.Result == nil || record.Result.Status != "completed" {
		return nil, fmt.Errorf("task completion source definition: %w", invalidPersonalReceipt())
	}
	var out struct {
		Completion sdk.ConversationTaskCompletionRecord `json:"completion"`
	}
	if decodePersonalReceipt(record.Result.Content, &out) != nil || out.Completion.Submission.Source == nil || out.Completion.Submission.Source.ConversationID != owner.ConversationID || out.Completion.Submission.Source.RunID != owner.RunID || out.Completion.Submission.Source.BeforeStep != record.Step+1 {
		return nil, fmt.Errorf("task completion result: %w", invalidPersonalReceipt())
	}
	run, err := audit.s.repo.Run(ctx, owner.ConversationID, owner.RunID, audit.a)
	if err != nil {
		return nil, err
	}
	if run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" || record.Result.ResourceID != run.BackgroundTask.TaskID {
		return nil, fmt.Errorf("task completion identity: %w", invalidPersonalReceipt())
	}
	repo, ok := audit.s.repo.(persistence.ConversationTaskCompletionRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "task_completion_unavailable")
	}
	saved, err := taskCompletionAtRevision(ctx, repo, run.BackgroundTask.TaskID, out.Completion.Revision, audit.a)
	if err != nil {
		return nil, err
	}
	if conversationDigest(saved) != conversationDigest(out.Completion) {
		return nil, fmt.Errorf("task completion revision: %w", invalidPersonalReceipt())
	}
	if err = audit.s.checkConversationTaskCompletionSources(ctx, saved, audit.a); err != nil {
		return nil, err
	}
	return []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}, ctx.Err()
}

func (audit *conversationSourceAudit) deliveryTaskToolResult(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) (bool, []sdk.ConversationRunReference, error) {
	definition, ok := taskResultReadDefinition(record.Call.Name)
	if !ok {
		return false, nil, nil
	}
	handled, err := audit.attestDeliveryPersonalRecord(ctx, owner, record, definition)
	if !handled || err != nil {
		if err != nil {
			err = fmt.Errorf("task source attestation: %w", err)
		}
		return handled, nil, err
	}
	roots, err := audit.taskToolRecord(ctx, owner, record)
	if err == nil {
		err = ctx.Err()
	}
	return true, roots, err
}

// Task controls return task projections, which can contain private input,
// waiting questions, execution previews and artifact metadata. Delivery access
// removes the need to execute a control again, never the source read checks.
func (audit *conversationSourceAudit) taskToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	ctx, audit = audit.rawExecutionSourceAudit(ctx)
	definition, known := taskResultReadDefinition(record.Call.Name)
	if !known || conversationDigest(definition) != conversationDigest(record.Definition) || record.Result == nil {
		return nil, fmt.Errorf("task source definition: %w", invalidPersonalReceipt())
	}
	repo, ok := audit.s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "tasks_unavailable")
	}
	var views []sdk.ConversationTaskDetail
	compactViews := map[string]conversationTaskToolView{}
	updates := map[string]conversationTaskAgreementToolInput{}
	var receipt *sdk.ConversationTaskReceipt
	switch record.Call.Name {
	case "task_start":
		var out struct {
			Task sdk.ConversationTaskReceipt `json:"task"`
		}
		if decodePersonalReceipt(record.Result.Content, &out) != nil || record.Result.Completion != "accepted" || record.Result.ResourceID != out.Task.ID || out.Task.SourceConversationID != owner.ConversationID || out.Task.SourceRunID != owner.RunID {
			return nil, invalidPersonalReceipt()
		}
		receipt = &out.Task
		views = []sdk.ConversationTaskDetail{{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: out.Task.ID, SourceConversationID: out.Task.SourceConversationID, SourceRunID: out.Task.SourceRunID, CreatedAt: out.Task.CreatedAt}}}
	case "task_list":
		var page sdk.ConversationTaskPage
		if decodePersonalReceipt(record.Result.Content, &page) != nil || len(page.Items) > 20 {
			return nil, fmt.Errorf("task list result: %w", invalidPersonalReceipt())
		}
		for _, item := range page.Items {
			views = append(views, sdk.ConversationTaskDetail{ConversationTaskSummary: item})
		}
	default:
		var id string
		if record.Call.Name == "task_update" {
			var args conversationTaskAgreementToolInput
			if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil {
				return nil, fmt.Errorf("task update arguments: %w", invalidPersonalReceipt())
			}
			id = args.ID
			updates[id] = args
		} else {
			var args struct {
				ID string `json:"id"`
			}
			if decodePersonalReceipt([]byte(record.Call.Arguments), &args) != nil {
				return nil, fmt.Errorf("task control arguments: %w", invalidPersonalReceipt())
			}
			id = args.ID
		}
		var out struct {
			Task conversationTaskToolView `json:"task"`
		}
		if decodePersonalReceipt(record.Result.Content, &out) != nil || out.Task.ID != id {
			return nil, fmt.Errorf("task control result: %w", invalidPersonalReceipt())
		}
		compactViews[out.Task.ID] = out.Task
		views = []sdk.ConversationTaskDetail{{ConversationTaskSummary: sdk.ConversationTaskSummary{ID: out.Task.ID}}}
	}
	roots := []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}
	readRun := func(ref sdk.ConversationRunReference) error {
		if ref.RunID == "" {
			return nil
		}
		if ref.ConversationID == owner.ConversationID && ref.RunID == owner.RunID {
			// The task's source may be the run which created/read it. Its input
			// depends only on the prefix before this call, not on this receipt.
			ref.BeforeStep = record.Step + 1
		}
		part, err := audit.run(ctx, ref)
		if err == nil {
			roots = mergeConversationSources(roots, part)
		}
		return err
	}
	seen := map[string]bool{}
	for _, saved := range views {
		if saved.ID == "" || seen[saved.ID] || saved.Progress.Steps < 0 || saved.Progress.Steps > 256 || len(saved.Artifacts) > 50 {
			return nil, fmt.Errorf("task summary shape: %w", invalidPersonalReceipt())
		}
		seen[saved.ID] = true
		current, err := repo.ConversationTask(ctx, saved.ID, audit.a)
		if err != nil {
			return nil, err
		}
		if compact, ok := compactViews[saved.ID]; ok {
			if compact.ID != current.ID {
				return nil, fmt.Errorf("task summary identity: %w", invalidPersonalReceipt())
			}
			saved = sdk.ConversationTaskDetail{ConversationTaskSummary: sdk.ConversationTaskSummary{
				ID: current.ID, Status: compact.Status, Goal: current.Goal, Brief: compact.Brief, AgreementRevision: compact.AgreementRevision, GoalProgress: compact.GoalProgress, Plan: compact.Plan,
				SourceConversationID: current.SourceConversationID, SourceRunID: current.SourceRunID, DelegationID: current.DelegationID, ExecutionConversationID: current.ExecutionConversationID,
				ExecutionRunID: compact.ExecutionRunID, PreviousExecutionRuns: compact.PreviousExecutionRuns, Progress: compact.Progress, Waiting: compact.Waiting,
				Result: compact.Result, Artifacts: compact.Artifacts, CompletionMode: compact.CompletionMode, Completion: compact.Completion, CompletionEventID: compact.CompletionEventID, CompletionEventSeq: current.CompletionEventSeq, ErrorCode: compact.ErrorCode,
				CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt,
			}}
		} else if current.ID != saved.ID || current.SourceConversationID != saved.SourceConversationID || current.SourceRunID != saved.SourceRunID || !current.CreatedAt.Equal(saved.CreatedAt) {
			return nil, fmt.Errorf("task summary identity: %w", invalidPersonalReceipt())
		}
		if update, ok := updates[saved.ID]; ok && (update.ExpectedRevision+1 != saved.AgreementRevision || conversationDigest(update.Brief) != conversationDigest(saved.Brief)) {
			return nil, fmt.Errorf("task update agreement: %w", invalidPersonalReceipt())
		}
		if err := audit.s.authorizeCollaborationTask(ctx, current, "execution_read", audit.a); err != nil {
			return nil, err
		}
		if saved.Completion != nil {
			completions, ok := audit.s.repo.(persistence.ConversationTaskCompletionRepository)
			if !ok {
				return nil, conversationFailure("unavailable", "task_completion_unavailable")
			}
			recorded, err := taskCompletionAtRevision(ctx, completions, saved.ID, saved.Completion.Revision, audit.a)
			if err != nil {
				return nil, err
			}
			if conversationDigest(recorded) != conversationDigest(*saved.Completion) {
				return nil, fmt.Errorf("task completion revision: %w", invalidPersonalReceipt())
			}
			if err = audit.s.checkConversationTaskCompletionSources(ctx, recorded, audit.a); err != nil {
				return nil, err
			}
		}
		// Even same-delegation raw task input/details require execution_read.
		// Do not use the narrower provenance-only delivery permission here.
		for _, id := range []string{current.SourceConversationID, conversationTaskExecutionConversation(current)} {
			if _, err := audit.s.repo.Get(ctx, id, audit.a); err != nil {
				return nil, err
			}
			if err := audit.s.authorizeCollaborationConversation(ctx, id, "execution_read", audit.a); err != nil {
				return nil, err
			}
		}
		if receipt != nil {
			if conversationDigest(receipt.AllowedTools) != conversationDigest(conversationTaskAllowedTools(current)) || receipt.Budget != current.Budget {
				return nil, invalidPersonalReceipt()
			}
		} else if saved.DelegationID != current.DelegationID || saved.ExecutionConversationID != current.ExecutionConversationID {
			return nil, fmt.Errorf("task summary collaboration: %w", invalidPersonalReceipt())
		}
		if err := readRun(sdk.ConversationRunReference{ConversationID: current.SourceConversationID, RunID: current.SourceRunID}); err != nil {
			return nil, err
		}
		if saved.ExecutionRunID != "" {
			id := conversationTaskExecutionConversation(current)
			run, err := audit.s.repo.Run(ctx, id, saved.ExecutionRunID, audit.a)
			if err != nil {
				return nil, err
			}
			if run.BackgroundTask == nil || run.BackgroundTask.TaskID != saved.ID {
				return nil, fmt.Errorf("task execution identity: %w", invalidPersonalReceipt())
			}
			if saved.Progress.Steps > len(run.Steps) {
				return nil, fmt.Errorf("task execution progress: %w", invalidPersonalReceipt())
			}
			if err := readRun(sdk.ConversationRunReference{ConversationID: id, RunID: saved.ExecutionRunID, BeforeStep: saved.Progress.Steps + 1}); err != nil {
				return nil, err
			}
			if saved.Result != nil {
				history, ok := audit.s.repo.(persistence.ConversationHistoryRepository)
				if !ok {
					return nil, conversationFailure("unavailable", "task_result_unavailable")
				}
				message, err := history.HistoryMessage(ctx, id, saved.Result.MessageID, audit.a)
				if err != nil {
					return nil, err
				}
				preview := truncateUTF8(message.Content, 4096)
				if message.RunID != saved.ExecutionRunID || message.Role != "assistant" || message.BackgroundTaskID != saved.ID || saved.Result.Preview != preview || saved.Result.Bytes != len(message.Content) || saved.Result.Complete != (len(preview) == len(message.Content)) {
					return nil, fmt.Errorf("task result identity: %w", invalidPersonalReceipt())
				}
			}
		} else if saved.Result != nil || len(saved.Steps) > 0 || saved.Waiting != nil || saved.Interaction != nil || len(saved.Artifacts) > 0 {
			return nil, fmt.Errorf("task execution omitted: %w", invalidPersonalReceipt())
		}
		for _, meta := range saved.Artifacts {
			if err := audit.connectedTool(ctx, "artifact_read"); err != nil {
				return nil, err
			}
			reader, err := audit.s.artifactAccess(ctx, audit.a, "artifact_read", map[string]any{"id": meta.ID, "version": meta.Version})
			if err != nil {
				return nil, err
			}
			artifact, err := reader.ArtifactRecord(ctx, meta.ID, meta.Version, audit.a)
			if err != nil {
				return nil, err
			}
			if meta.Version < 1 || meta.SourceConversationID != conversationTaskExecutionConversation(current) || meta.SourceRunID != saved.ExecutionRunID || conversationDigest(artifact.Artifact) != conversationDigest(meta) {
				return nil, invalidPersonalReceipt()
			}
			if err := audit.artifactSources(ctx, artifact); err != nil {
				return nil, err
			}
		}
	}
	return roots, ctx.Err()
}
