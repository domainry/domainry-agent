package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func planSourceMatchesUpdate(plan sdk.ConversationPlan, update sdk.ConversationPlanUpdate, owner sdk.ConversationRunReference, step int) bool {
	if !conversationKey(plan.TaskID) || plan.Version != update.ExpectedVersion+1 || plan.AgreementRevision != update.AgreementRevision || plan.Reason != strings.TrimSpace(update.Reason) || plan.Source == nil || plan.Source.ConversationID != owner.ConversationID || plan.Source.RunID != owner.RunID || plan.Source.BeforeStep != step+1 || plan.CreatedAt.IsZero() || len(plan.Steps) != len(update.Steps) || !validConversationPlanDAG(plan.Steps) {
		return false
	}
	for index, input := range update.Steps {
		fields := append([]string(nil), input.RequirementFields...)
		if len(fields) == 0 {
			fields = append([]string(nil), conversationPlanAgreementFields...)
		}
		sort.Strings(fields)
		saved := plan.Steps[index]
		if saved.ID != input.ID || saved.Title != strings.TrimSpace(input.Title) || saved.Status != input.Status || saved.Input != strings.TrimSpace(input.Input) || saved.ExpectedOutput != strings.TrimSpace(input.ExpectedOutput) || saved.Outcome != strings.TrimSpace(input.Outcome) || saved.Blocker != strings.TrimSpace(input.Blocker) || !conversationKey(saved.Executor.AgentID) || !conversationKey(saved.Executor.RunID) || saved.Executor.RunID != owner.RunID && saved.Status != sdk.ConversationPlanStepCompleted || conversationDigest(saved.DependsOn) != conversationDigest(input.DependsOn) || conversationDigest(saved.RequirementFields) != conversationDigest(fields) || conversationDigest(saved.Evidence) != conversationDigest(input.Evidence) || conversationDigest(saved.Artifacts) != conversationDigest(input.Artifacts) {
			return false
		}
	}
	return true
}

func (audit *conversationSourceAudit) planToolRecord(ctx context.Context, owner sdk.ConversationRunReference, record persistence.ConversationToolExecution) ([]sdk.ConversationRunReference, error) {
	ctx, cancel := audit.s.sourceAccessContext(ctx)
	defer cancel()
	definition := sdk.ConversationPlanUpdateTool()
	if conversationDigest(record.Definition) != conversationDigest(definition) || record.State != "completed" || record.Result == nil || record.Result.Status != "completed" || record.Result.ErrorCode != "" {
		return nil, fmt.Errorf("plan source definition: %w", invalidPersonalReceipt())
	}
	if err := audit.connectedTool(ctx, definition.Key); err != nil {
		return nil, err
	}
	released := releasedSourcePurpose(ctx) != ""
	if released {
		if err := audit.authorizeReleasedSourceRead(ctx); err != nil {
			return nil, err
		}
	}
	producer := audit.evidenceAuthority(owner)
	reader, ok := audit.s.repo.(persistence.ConversationExecutionReadRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "execution_read_unavailable")
	}
	if _, err := audit.s.repo.Get(ctx, owner.ConversationID, producer); err != nil {
		return nil, err
	}
	original, err := reader.ReadExecutionCall(ctx, owner.ConversationID, owner.RunID, record.Step, record.Call.ID, producer)
	if err != nil {
		return nil, err
	}
	if original.State != "completed" || original.Result == nil || conversationDigest(original.Call) != conversationDigest(record.Call) || conversationDigest(original.Definition) != conversationDigest(record.Definition) || conversationDigest(original.Result) != conversationDigest(record.Result) || original.IdempotencyKey != record.IdempotencyKey {
		return nil, fmt.Errorf("plan source record: %w", invalidPersonalReceipt())
	}
	var update sdk.ConversationPlanUpdate
	var saved struct {
		Plan sdk.ConversationPlan `json:"plan"`
	}
	schema, schemaErr := compileConversationSchema(definition.InputSchema)
	if schemaErr != nil || validateToolJSON(schema, []byte(record.Call.Arguments)) != nil || decodePersonalReceipt([]byte(record.Call.Arguments), &update) != nil || decodePersonalReceipt(record.Result.Content, &saved) != nil || record.Result.ResourceID != saved.Plan.TaskID || !planSourceMatchesUpdate(saved.Plan, update, owner, record.Step) {
		return nil, fmt.Errorf("plan source receipt: %w", invalidPersonalReceipt())
	}
	repo, ok := audit.s.repo.(persistence.ConversationTaskPlanRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "plans_unavailable")
	}
	stored, err := repo.ConversationTaskPlan(ctx, saved.Plan.TaskID, saved.Plan.Version, producer)
	if err != nil {
		return nil, err
	}
	if conversationDigest(stored) != conversationDigest(saved.Plan) {
		return nil, conversationFailure("conflict", "plan_reference_changed")
	}
	tasks, ok := audit.s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return nil, conversationFailure("unavailable", "tasks_unavailable")
	}
	task, err := tasks.ConversationTask(ctx, stored.TaskID, producer)
	if err != nil {
		return nil, err
	}
	executionConversationID := conversationTaskExecutionConversation(task)
	if !released {
		if err = audit.authorizeExecutionSource(ctx, executionConversationID); err != nil {
			return nil, err
		}
	}
	if _, err = audit.s.repo.Get(ctx, executionConversationID, producer); err != nil {
		return nil, err
	}
	roots := []sdk.ConversationRunReference{{ConversationID: owner.ConversationID, RunID: owner.RunID, BeforeStep: record.Step + 2}}
	for _, step := range stored.Steps {
		for _, ref := range step.Evidence {
			results, ok := audit.s.repo.(persistence.ConversationResultRepository)
			if !ok {
				return nil, conversationFailure("unavailable", "result_read_unavailable")
			}
			source, readErr := results.ConversationResult(ctx, ref, producer)
			if readErr != nil {
				return nil, readErr
			}
			if source.Result == nil || source.State != "completed" || source.Step != ref.Step || source.Call.ID != ref.CallID || conversationDigest(source.Result) != ref.SHA256 {
				return nil, conversationFailure("conflict", "result_reference_changed")
			}
			part, readErr := audit.record(ctx, sdk.ConversationRunReference{ConversationID: ref.ConversationID, RunID: ref.RunID}, source)
			if readErr != nil {
				return nil, readErr
			}
			roots = mergeConversationSources(roots, part)
		}
		for _, ref := range step.Artifacts {
			reader, readErr := audit.s.artifactAccess(ctx, audit.a, "artifact_read", map[string]any{"id": ref.ID, "version": ref.Version})
			if readErr != nil {
				return nil, readErr
			}
			artifact, readErr := reader.ArtifactRecord(ctx, ref.ID, ref.Version, audit.a)
			if readErr != nil {
				return nil, readErr
			}
			if artifact.Artifact.Version != ref.Version || artifact.Artifact.SHA256 != ref.SHA256 {
				return nil, conversationFailure("conflict", "plan_artifact_changed")
			}
			if readErr = audit.artifactSources(ctx, artifact); readErr != nil {
				return nil, readErr
			}
		}
	}
	return roots, ctx.Err()
}
