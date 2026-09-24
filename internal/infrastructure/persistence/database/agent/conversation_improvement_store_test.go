package agent

import (
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

func improvementSkill(version, body string) sdk.SkillSchema {
	return sdk.SkillSchema{Key: "report", Version: version, Name: "Report", Description: "Prepare reports", Instructions: body, AllowedTools: []string{"record_read"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}
}

func improvementCandidate(id, version, baseline string, skill, baselineSkill sdk.SkillSchema, feedback string) sdk.ConversationImprovementCandidate {
	now := time.Now().UTC().Truncate(time.Millisecond)
	raw, _ := json.Marshal(skill)
	baselineRaw, _ := json.Marshal(baselineSkill)
	return sdk.ConversationImprovementCandidate{ID: id, Kind: "skill", TargetKey: skill.Key, Version: version, BaselineVersion: baseline, BaselineProposal: baselineRaw, FeedbackIDs: []string{feedback}, Proposal: raw, Reason: "Observed revision", Status: "candidate", Revision: 1, CreatedAt: now, UpdatedAt: now}
}

func evaluateImprovement(t *testing.T, repo *ConversationStore, id, client string, a sdk.ConversationAuthority) sdk.ConversationImprovementCandidate {
	t.Helper()
	out, err := repo.EvaluateConversationImprovementCandidate(t.Context(), id, sdk.ConversationImprovementEvaluationWrite{ClientID: client, ExpectedRevision: 1, SuiteVersion: "v01-skill-1", ScenarioIDs: []string{"skill-flow"}, BaselineCompleted: 1, CandidateCompleted: 1, BaselineOmissions: 1, CandidateOmissions: 0, Passed: true}, a)
	if err != nil || out.Status != "evaluated" || out.Revision != 2 || out.Evaluation == nil || !out.Evaluation.Passed {
		t.Fatalf("evaluation=%+v err=%v", out, err)
	}
	return out
}

func TestCapabilityImprovementPublicationRollbackAndOwnerIsolation(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	now := time.Now().UTC().Truncate(time.Millisecond)
	request := sdk.ConversationCapabilityFeedbackCreate{ClientID: "feedback-client", TaskID: "task-one", Outcome: "revised", Reason: "Changed report ordering", SkillKeys: []string{"report"}}
	prepared := sdk.ConversationCapabilityFeedback{ID: "feedback-one", TaskID: request.TaskID, RunID: "run-one", Outcome: request.Outcome, Reason: request.Reason, AgentID: "analyst", AgentRevision: 3, SkillVersions: map[string]string{"report": "1"}, CreatedAt: now}
	feedback, err := repo.CreateConversationCapabilityFeedback(t.Context(), request.ClientID, prepared, request, a)
	if err != nil || feedback.ID != prepared.ID {
		t.Fatalf("feedback=%+v err=%v", feedback, err)
	}
	replay, err := repo.CreateConversationCapabilityFeedback(t.Context(), request.ClientID, prepared, request, a)
	if err != nil || replay.ID != prepared.ID {
		t.Fatalf("feedback replay=%+v err=%v", replay, err)
	}
	changed := request
	changed.Reason = "different"
	_, err = repo.CreateConversationCapabilityFeedback(t.Context(), request.ClientID, prepared, changed, a)
	requireConversationCode(t, err, "idempotency_conflict")

	v1skill := improvementSkill("1", "Original column order")
	v2skill := improvementSkill("2", "Use the accepted column order")
	v2create := sdk.ConversationImprovementCandidateCreate{ClientID: "candidate-v2", Kind: "skill", TargetKey: "report", Version: "2", FeedbackIDs: []string{feedback.ID}, Proposal: json.RawMessage(conversationJSON(v2skill)), Reason: "Apply reviewed feedback"}
	v2 := improvementCandidate("candidate-two", "2", "1", v2skill, v1skill, feedback.ID)
	if _, err = repo.CreateConversationImprovementCandidate(t.Context(), v2create.ClientID, v2, v2create, a); err != nil {
		t.Fatal(err)
	}
	v2 = evaluateImprovement(t, repo, v2.ID, "evaluate-v2", a)
	v2, err = repo.PublishConversationImprovementCandidate(t.Context(), v2.ID, sdk.ConversationImprovementPublish{ClientID: "publish-v2", ExpectedRevision: v2.Revision}, a)
	if err != nil || v2.Status != "published" || v2.Revision != 3 {
		t.Fatalf("publish v2=%+v err=%v", v2, err)
	}
	published, err := repo.PublishedConversationSkills(t.Context(), a)
	if err != nil || len(published) != 1 || published[0].Definition.Version != "2" || published[0].Definition.Instructions != v2skill.Instructions {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	baseline, err := repo.RollbackConversationImprovement(t.Context(), v2.ID, sdk.ConversationImprovementRollback{ClientID: "rollback-v2", ExpectedRevision: v2.Revision, TargetVersion: "1", Reason: "Restore original configuration"}, a)
	if err != nil || !baseline.BaselineSnapshot || baseline.Version != "1" || baseline.Status != "published" {
		t.Fatalf("initial baseline rollback=%+v err=%v", baseline, err)
	}
	v2, err = repo.RollbackConversationImprovement(t.Context(), baseline.ID, sdk.ConversationImprovementRollback{ClientID: "restore-v2", ExpectedRevision: baseline.Revision, TargetVersion: "2", Reason: "Restore evaluated candidate"}, a)
	if err != nil || v2.Status != "published" || v2.Version != "2" {
		t.Fatalf("restore v2=%+v err=%v", v2, err)
	}

	v3skill := improvementSkill("3", "A worse experimental ordering")
	v3create := sdk.ConversationImprovementCandidateCreate{ClientID: "candidate-v3", Kind: "skill", TargetKey: "report", Version: "3", FeedbackIDs: []string{feedback.ID}, Proposal: json.RawMessage(conversationJSON(v3skill)), Reason: "Experiment"}
	v3 := improvementCandidate("candidate-three", "3", "2", v3skill, v2skill, feedback.ID)
	if _, err = repo.CreateConversationImprovementCandidate(t.Context(), v3create.ClientID, v3, v3create, a); err != nil {
		t.Fatal(err)
	}
	v3 = evaluateImprovement(t, repo, v3.ID, "evaluate-v3", a)
	v3, err = repo.PublishConversationImprovementCandidate(t.Context(), v3.ID, sdk.ConversationImprovementPublish{ClientID: "publish-v3", ExpectedRevision: v3.Revision}, a)
	if err != nil || v3.Status != "published" {
		t.Fatalf("publish v3=%+v err=%v", v3, err)
	}
	historical, err := repo.ConversationImprovementCandidate(t.Context(), v2.ID, a)
	if err != nil || historical.Status != "retired" {
		t.Fatalf("superseded candidate=%+v err=%v", historical, err)
	}
	restored, err := repo.RollbackConversationImprovement(t.Context(), v3.ID, sdk.ConversationImprovementRollback{ClientID: "rollback-v3", ExpectedRevision: v3.Revision, TargetVersion: "2", Reason: "Regression"}, a)
	if err != nil || restored.ID != v2.ID || restored.Status != "published" {
		t.Fatalf("rollback=%+v err=%v", restored, err)
	}
	published, _ = repo.PublishedConversationSkills(t.Context(), a)
	if len(published) != 1 || published[0].Definition.Version != "2" {
		t.Fatalf("rollback current=%+v", published)
	}

	other := a
	other.UserID = "other-user"
	if isolated, err := repo.PublishedConversationSkills(t.Context(), other); err != nil || len(isolated) != 0 {
		t.Fatalf("foreign published=%+v err=%v", isolated, err)
	}
	if candidates, err := repo.ConversationImprovementCandidates(t.Context(), a); err != nil || len(candidates) != 3 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
}

func TestCapabilityImprovementRejectsPublicationWithoutPassingEvaluation(t *testing.T) {
	store, _ := openAgentStore(t)
	repo := newTestConversationStore(t, store)
	a := conversationTestAuthority()
	skill := improvementSkill("2", "Candidate")
	create := sdk.ConversationImprovementCandidateCreate{ClientID: "candidate-failed", Kind: "skill", TargetKey: "report", Version: "2", FeedbackIDs: []string{"feedback"}, Proposal: json.RawMessage(conversationJSON(skill)), Reason: "Candidate"}
	candidate := improvementCandidate("candidate-failed", "2", "1", skill, improvementSkill("1", "Baseline"), "feedback")
	if _, err := repo.CreateConversationImprovementCandidate(t.Context(), create.ClientID, candidate, create, a); err != nil {
		t.Fatal(err)
	}
	_, err := repo.PublishConversationImprovementCandidate(t.Context(), candidate.ID, sdk.ConversationImprovementPublish{ClientID: "premature", ExpectedRevision: 1}, a)
	requireConversationCode(t, err, "improvement_evaluation_required")
}
