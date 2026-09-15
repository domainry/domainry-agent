package application

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

func (s *ConversationService) prepareConversationTaskCompletion(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationTaskCompletionRecord, error) {
	var submit agentsdk.ConversationTaskCompletionSubmit
	if in.Call.Name != "completion_submit" || json.Unmarshal([]byte(in.Call.Arguments), &submit) != nil || !conversationKey(submit.ClientID) || submit.ExpectedRevision < 0 || submit.AgreementRevision < 1 || !conversationText(submit.Summary, 8192, true) || len(submit.Data) > 65536 || len(submit.Data) > 0 && !json.Valid(submit.Data) || len(submit.Artifacts) > 32 {
		return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("bad_request", "task_completion_invalid")
	}
	if _, ok := s.repo.(persistence.ConversationTaskCompletionRepository); !ok {
		return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("unavailable", "task_completion_unavailable")
	}
	run, err := s.repo.Run(ctx, in.ConversationID, in.RunID, in.Authority)
	if err != nil || run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" {
		return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("conflict", "task_completion_run_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, run.BackgroundTask.TaskID, in.Authority)
	if err != nil {
		return agentsdk.ConversationTaskCompletionRecord{}, err
	}
	currentRevision := int64(0)
	if task.Completion != nil {
		currentRevision = task.Completion.Revision
	}
	executionConversationID := conversationTaskExecutionConversation(task)
	if task.CompletionMode != agentsdk.ConversationTaskCompletionModeAssessed || run.BackgroundTask.CompletionMode != agentsdk.ConversationTaskCompletionModeAssessed || task.DelegationID != "" || task.FollowUp != nil || task.Status != agentsdk.ConversationTaskStatusRunning || task.ExecutionRunID != in.RunID || executionConversationID != in.ConversationID || submit.ExpectedRevision != currentRevision || submit.AgreementRevision != max(1, task.AgreementRevision) || max(1, run.BackgroundTask.AgreementRevision) != submit.AgreementRevision || task.Brief == nil || len(submit.Conditions) != len(task.Brief.CompletionConditions) {
		return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("conflict", "task_completion_changed")
	}
	if err = s.checkCompletionAssessments(ctx, *task.Brief, submit.Conditions, in.Authority, in.ConversationID); err != nil {
		return agentsdk.ConversationTaskCompletionRecord{}, err
	}
	for _, ref := range submit.Conditions {
		for _, receipt := range ref.Receipts {
			if receipt.ConversationID == in.ConversationID && receipt.RunID == in.RunID && receipt.Step >= in.Step {
				return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("bad_request", "task_completion_evidence_invalid")
			}
		}
	}
	seenArtifacts := map[string]bool{}
	for _, ref := range submit.Artifacts {
		key := ref.ID + ":" + strconv.FormatInt(ref.Version, 10)
		if !conversationKey(ref.ID) || ref.Version < 1 || len(ref.SHA256) != 64 || seenArtifacts[key] {
			return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("bad_request", "task_completion_artifact_invalid")
		}
		seenArtifacts[key] = true
		artifact, readErr := s.Artifact(ctx, ref.ID, ref.Version, in.Authority)
		if readErr != nil {
			return agentsdk.ConversationTaskCompletionRecord{}, readErr
		}
		if artifact.Artifact.Version != ref.Version || artifact.Artifact.SHA256 != ref.SHA256 {
			return agentsdk.ConversationTaskCompletionRecord{}, conversationFailure("conflict", "task_completion_artifact_changed")
		}
	}
	source := agentsdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID, BeforeStep: in.Step + 1}
	return agentsdk.ConversationTaskCompletionRecord{
		Revision: currentRevision + 1,
		Kind:     "agent_assessment",
		Submission: agentsdk.ConversationTaskCompletionSubmission{
			AgreementRevision: submit.AgreementRevision,
			Summary:           strings.TrimSpace(submit.Summary), Data: append(json.RawMessage(nil), submit.Data...),
			Conditions: append([]agentsdk.ConversationConditionAssessment{}, submit.Conditions...), Artifacts: append([]agentsdk.ConversationArtifactReference{}, submit.Artifacts...), Source: &source,
		},
	}, nil
}

func (s *ConversationService) ReviewConversationTaskCompletion(ctx context.Context, id string, in agentsdk.ConversationTaskCompletionReviewRequest, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskDetail, error) {
	return s.reviewConversationTaskCompletion(ctx, id, in, a, true)
}

func (s *ConversationService) checkConversationTaskCompletionSources(ctx context.Context, completion agentsdk.ConversationTaskCompletionRecord, a agentsdk.ConversationAuthority) error {
	if completion.Submission.Source != nil {
		if err := s.checkRunSources(ctx, *completion.Submission.Source, a); err != nil {
			return err
		}
	}
	if completion.Verification.Source != nil {
		if err := s.checkRunSources(ctx, *completion.Verification.Source, a); err != nil {
			return err
		}
	}
	if err := s.checkCompletionReceipts(ctx, completionCheckReceipts(completion.Verification.Checks), a, ""); err != nil {
		return err
	}
	if err := s.checkCompletionReceipts(ctx, conditionAssessmentReceipts(completion.Submission.Conditions), a, ""); err != nil {
		return err
	}
	for _, ref := range completion.Submission.Artifacts {
		artifact, err := s.Artifact(ctx, ref.ID, ref.Version, a)
		if err != nil {
			return err
		}
		if artifact.Artifact.Version != ref.Version || artifact.Artifact.SHA256 != ref.SHA256 {
			return conversationFailure("conflict", "task_completion_artifact_changed")
		}
	}
	return nil
}

func (s *ConversationService) reviewConversationTaskCompletion(ctx context.Context, id string, in agentsdk.ConversationTaskCompletionReviewRequest, a agentsdk.ConversationAuthority, authorize bool) (agentsdk.ConversationTaskDetail, error) {
	if authorize {
		if err := s.authorizeConversationTaskControl(ctx, id, "task_review", a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	if !conversationKey(id) || !conversationKey(in.ClientID) || in.ExpectedRevision < 1 || !conversationText(in.Reason, 4096, true) || len(in.Review.DeliveryDigest) != 64 {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("bad_request", "task_completion_review_invalid")
	}
	task, err := s.conversationTaskRecord(ctx, id, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	for _, operation := range []string{"manage", "execution_read"} {
		if err = s.authorizeCollaborationTask(ctx, task, operation, a); err != nil {
			return agentsdk.ConversationTaskDetail{}, err
		}
	}
	if task.Brief == nil || task.Completion == nil || len(in.Review.Conditions) != len(task.Brief.CompletionConditions) {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("conflict", "task_completion_missing")
	}
	if err = s.checkConversationTaskCompletionSources(ctx, *task.Completion, a); err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	if err = s.checkCompletionAssessments(ctx, *task.Brief, in.Review.Conditions, a, ""); err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	repo, ok := s.repo.(persistence.ConversationTaskCompletionRepository)
	if !ok {
		return agentsdk.ConversationTaskDetail{}, conversationFailure("unavailable", "task_completion_unavailable")
	}
	task, _, err = repo.ReviewConversationTaskCompletion(ctx, id, in, a)
	if err != nil {
		return agentsdk.ConversationTaskDetail{}, err
	}
	return s.projectConversationTask(ctx, task, a, true)
}

func (s *ConversationService) ConversationTaskCompletionHistory(ctx context.Context, id string, before int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationTaskCompletionHistory, error) {
	if before < 0 {
		return agentsdk.ConversationTaskCompletionHistory{}, conversationFailure("bad_request", "task_completion_query_invalid")
	}
	if _, err := s.ConversationTask(ctx, id, a); err != nil {
		return agentsdk.ConversationTaskCompletionHistory{}, err
	}
	repo, ok := s.repo.(persistence.ConversationTaskCompletionRepository)
	if !ok {
		return agentsdk.ConversationTaskCompletionHistory{}, conversationFailure("unavailable", "task_completion_unavailable")
	}
	history, err := repo.ConversationTaskCompletionHistory(ctx, id, before, a)
	if err != nil {
		return agentsdk.ConversationTaskCompletionHistory{}, err
	}
	for _, item := range history.Items {
		if err = s.checkConversationTaskCompletionSources(ctx, item, a); err != nil {
			return agentsdk.ConversationTaskCompletionHistory{}, err
		}
	}
	return history, nil
}

var _ agentsdk.ConversationTaskCompletionService = (*ConversationService)(nil)
