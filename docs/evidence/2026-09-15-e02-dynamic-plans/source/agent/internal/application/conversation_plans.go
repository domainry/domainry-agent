package application

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
)

var conversationPlanAgreementFields = []string{"assumptions", "audience", "completion_conditions", "constraints", "deliverable", "due_at", "goal"}

func validConversationPlanStatus(status string) bool {
	switch status {
	case agentsdk.ConversationPlanStepPending, agentsdk.ConversationPlanStepInProgress, agentsdk.ConversationPlanStepCompleted, agentsdk.ConversationPlanStepBlocked, agentsdk.ConversationPlanStepSkipped, agentsdk.ConversationPlanStepNeedsReview:
		return true
	}
	return false
}

func validConversationPlanRequirement(field string) bool {
	for _, allowed := range append(append([]string(nil), conversationPlanAgreementFields...), "input", "dependencies") {
		if field == allowed {
			return true
		}
	}
	return false
}

func (s *ConversationService) prepareConversationTaskPlan(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationPlan, error) {
	var update agentsdk.ConversationPlanUpdate
	if in.Call.Name != "plan_update" || json.Unmarshal([]byte(in.Call.Arguments), &update) != nil || !conversationKey(update.ClientID) || update.ExpectedVersion < 0 || update.AgreementRevision < 1 || !conversationText(update.Reason, 2048, true) || len(update.Steps) < 1 || len(update.Steps) > 32 {
		return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_invalid")
	}
	if _, ok := s.repo.(persistence.ConversationTaskPlanRepository); !ok {
		return agentsdk.ConversationPlan{}, conversationFailure("unavailable", "plans_unavailable")
	}
	taskRepo, ok := s.repo.(persistence.ConversationTaskReadRepository)
	if !ok {
		return agentsdk.ConversationPlan{}, conversationFailure("unavailable", "tasks_unavailable")
	}
	run, err := s.repo.Run(ctx, in.ConversationID, in.RunID, in.Authority)
	if err != nil || run.BackgroundTask == nil || run.BackgroundTask.TaskID == "" {
		return agentsdk.ConversationPlan{}, conversationFailure("conflict", "plan_task_invalid")
	}
	task, err := taskRepo.ConversationTask(ctx, run.BackgroundTask.TaskID, in.Authority)
	if err != nil {
		return agentsdk.ConversationPlan{}, err
	}
	currentVersion := int64(0)
	if task.Plan != nil {
		currentVersion = task.Plan.Version
	}
	executionConversationID := task.ExecutionConversationID
	if executionConversationID == "" {
		executionConversationID = task.SourceConversationID
	}
	if task.Status != agentsdk.ConversationTaskStatusRunning || task.ExecutionRunID != in.RunID || executionConversationID != in.ConversationID || update.ExpectedVersion != currentVersion || update.AgreementRevision != max(1, task.AgreementRevision) || max(1, run.BackgroundTask.AgreementRevision) != update.AgreementRevision {
		return agentsdk.ConversationPlan{}, conversationFailure("conflict", "plan_changed")
	}
	agentID := "default"
	if run.Agent != nil && run.Agent.ID != "" {
		agentID = run.Agent.ID
	}
	source := agentsdk.ConversationRunReference{ConversationID: in.ConversationID, RunID: in.RunID, BeforeStep: in.Step + 1}
	plan := agentsdk.ConversationPlan{TaskID: task.ID, Version: currentVersion + 1, AgreementRevision: update.AgreementRevision, Reason: strings.TrimSpace(update.Reason), Steps: make([]agentsdk.ConversationPlanStep, 0, len(update.Steps)), Source: &source}
	seen, active := map[string]bool{}, 0
	for _, input := range update.Steps {
		if !conversationKey(input.ID) || !conversationText(input.Title, 512, true) || !validConversationPlanStatus(input.Status) || !conversationText(input.Input, 2048, false) || !conversationText(input.ExpectedOutput, 2048, true) || len(input.DependsOn) > 32 || len(input.RequirementFields) > 9 || len(input.Evidence) > 32 || len(input.Artifacts) > 32 || !conversationText(input.Outcome, 2048, false) || !conversationText(input.Blocker, 2048, false) || seen[input.ID] {
			return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_invalid")
		}
		seen[input.ID] = true
		if input.Status == agentsdk.ConversationPlanStepInProgress {
			active++
		}
		if input.Status == agentsdk.ConversationPlanStepCompleted && strings.TrimSpace(input.Outcome) == "" || input.Status == agentsdk.ConversationPlanStepBlocked && strings.TrimSpace(input.Blocker) == "" {
			return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_invalid")
		}
		fields := append([]string(nil), input.RequirementFields...)
		if len(fields) == 0 {
			fields = append([]string(nil), conversationPlanAgreementFields...)
		}
		sort.Strings(fields)
		for index, field := range fields {
			if !validConversationPlanRequirement(field) || index > 0 && fields[index-1] == field {
				return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_invalid")
			}
		}
		step := agentsdk.ConversationPlanStep{ID: input.ID, Title: strings.TrimSpace(input.Title), Status: input.Status, DependsOn: append([]string{}, input.DependsOn...), Input: strings.TrimSpace(input.Input), ExpectedOutput: strings.TrimSpace(input.ExpectedOutput), RequirementFields: fields, Executor: agentsdk.ConversationPlanExecutor{AgentID: agentID, RunID: in.RunID}, Evidence: append([]agentsdk.ConversationResultReference{}, input.Evidence...), Artifacts: append([]agentsdk.ConversationArtifactReference{}, input.Artifacts...), Outcome: strings.TrimSpace(input.Outcome), Blocker: strings.TrimSpace(input.Blocker)}
		if task.Plan != nil {
			for _, old := range task.Plan.Steps {
				if old.ID == step.ID && old.Status == agentsdk.ConversationPlanStepCompleted {
					step.Executor = old.Executor
				}
			}
		}
		plan.Steps = append(plan.Steps, step)
	}
	if active > 1 || !validConversationPlanDAG(plan.Steps) {
		return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_invalid")
	}
	if task.Plan != nil {
		for _, old := range task.Plan.Steps {
			if old.Status != agentsdk.ConversationPlanStepCompleted {
				continue
			}
			found := false
			for _, current := range plan.Steps {
				if current.ID == old.ID {
					found = true
					if conversationDigest(current) != conversationDigest(old) {
						return agentsdk.ConversationPlan{}, conversationFailure("conflict", "plan_completed_step_changed")
					}
				}
			}
			if !found {
				return agentsdk.ConversationPlan{}, conversationFailure("conflict", "plan_completed_step_changed")
			}
		}
	}
	_, catalog, err := s.executionCatalogForRun(ctx, persistence.ConversationClaim{Authority: in.Authority, Run: run})
	if err != nil {
		return agentsdk.ConversationPlan{}, err
	}
	for _, step := range plan.Steps {
		for _, ref := range step.Evidence {
			if ref.ConversationID == in.ConversationID && ref.RunID == in.RunID && ref.Step >= in.Step {
				return agentsdk.ConversationPlan{}, conversationFailure("bad_request", "plan_evidence_invalid")
			}
			if _, err = s.authorizedConversationResult(ctx, ref, in.Authority, catalog, map[string]bool{}, in.ConversationID); err != nil {
				return agentsdk.ConversationPlan{}, err
			}
		}
		for _, ref := range step.Artifacts {
			artifact, readErr := s.Artifact(ctx, ref.ID, ref.Version, in.Authority)
			if readErr != nil {
				return agentsdk.ConversationPlan{}, readErr
			}
			if artifact.Artifact.Version != ref.Version || artifact.Artifact.SHA256 != ref.SHA256 {
				return agentsdk.ConversationPlan{}, conversationFailure("conflict", "plan_artifact_changed")
			}
		}
	}
	return plan, nil
}

func validConversationPlanDAG(steps []agentsdk.ConversationPlanStep) bool {
	byID := make(map[string]agentsdk.ConversationPlanStep, len(steps))
	for _, step := range steps {
		byID[step.ID] = step
	}
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		step := byID[id]
		seen := map[string]bool{}
		for _, dependency := range step.DependsOn {
			if dependency == id || seen[dependency] {
				return false
			}
			seen[dependency] = true
			if _, exists := byID[dependency]; !exists || !visit(dependency) {
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
	for _, step := range steps {
		if step.Status != agentsdk.ConversationPlanStepInProgress && step.Status != agentsdk.ConversationPlanStepCompleted {
			continue
		}
		for _, dependency := range step.DependsOn {
			status := byID[dependency].Status
			if status != agentsdk.ConversationPlanStepCompleted && status != agentsdk.ConversationPlanStepSkipped {
				return false
			}
		}
	}
	return true
}

func (s *ConversationService) ConversationTaskPlans(ctx context.Context, id string, before int64, a agentsdk.ConversationAuthority) (agentsdk.ConversationPlanHistory, error) {
	if err := s.authorize(a); err != nil {
		return agentsdk.ConversationPlanHistory{}, err
	}
	if !conversationKey(id) || before < 0 {
		return agentsdk.ConversationPlanHistory{}, conversationFailure("bad_request", "plan_query_invalid")
	}
	if _, err := s.ConversationTask(ctx, id, a); err != nil {
		return agentsdk.ConversationPlanHistory{}, err
	}
	repo, ok := s.repo.(persistence.ConversationTaskPlanRepository)
	if !ok {
		return agentsdk.ConversationPlanHistory{}, conversationFailure("unavailable", "plans_unavailable")
	}
	return repo.ConversationTaskPlans(ctx, id, before, a)
}

var _ agentsdk.ConversationTaskPlanService = (*ConversationService)(nil)
