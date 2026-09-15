package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
)

type skillIntegrationAuthorizer struct{}

func (skillIntegrationAuthorizer) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	return agentsdk.ConversationToolAuthorization{Granted: true, Revision: "skill-integration-policy"}, nil
}

type skillIntegrationModel struct {
	mu       sync.Mutex
	calls    int
	changing chan struct{}
	resume   chan struct{}
	t        *testing.T
}

func (*skillIntegrationModel) GenerateConversation(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	return agentsdk.ConversationModelResult{}, fmt.Errorf("unexpected text-only model call")
}
func (*skillIntegrationModel) ConversationModelIdentity() agentsdk.ConversationModelIdentity {
	return agentsdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "skill", Fingerprint: "skill-integration-v1"}
}
func (m *skillIntegrationModel) StreamConversationStep(_ context.Context, in agentsdk.ConversationStepRequest, _ func(agentsdk.ConversationModelEvent) error) (agentsdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	joined := ""
	for _, message := range in.Messages {
		joined += "\n" + message.Role + ":" + message.Content
	}
	hasSkillLoad := false
	for _, definition := range in.Tools {
		hasSkillLoad = hasSkillLoad || definition.Key == "skill_load"
	}
	switch call {
	case 1:
		if !hasSkillLoad || !strings.Contains(joined, "report @ 1") || strings.Contains(joined, "BODY ONE") || strings.Contains(joined, "RESOURCE ONE") {
			m.t.Errorf("first request did not contain a summary-only Skill catalog: tools=%+v messages=%s", in.Tools, joined)
		}
		return skillLoadCall("body-one", "1", ""), nil
	case 2:
		if !strings.Contains(joined, "BODY ONE") || !strings.Contains(joined, "Inspect the source") || strings.Contains(joined, "RESOURCE ONE") {
			m.t.Errorf("Skill body load leaked or omitted a resource: %s", joined)
		}
		return skillLoadCall("resource-one", "1", "template"), nil
	case 3:
		if !strings.Contains(joined, "RESOURCE ONE") || strings.Count(joined, "BODY ONE") != 1 {
			m.t.Errorf("resource load did not remain separately addressable: %s", joined)
		}
		return agentsdk.ConversationStepResult{FinishReason: "stop", Model: "skill", Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "Skill v1 applied"}}, nil
	case 4:
		if !hasSkillLoad || !strings.Contains(joined, "report @ 2") || strings.Contains(joined, "BODY TWO") {
			m.t.Errorf("published Skill v2 was not selected by a new run: %s", joined)
		}
		close(m.changing)
		<-m.resume
		return skillLoadCall("body-two", "2", ""), nil
	default:
		return agentsdk.ConversationStepResult{}, fmt.Errorf("stale Agent configuration reached model call %d", call)
	}
}

func skillLoadCall(id, version, resource string) agentsdk.ConversationStepResult {
	args := agentsdk.ConversationSkillLoadRequest{Key: "report", Version: version, ResourceKey: resource}
	raw, _ := json.Marshal(args)
	return agentsdk.ConversationStepResult{FinishReason: "tool_calls", Model: "skill", Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: id, Name: "skill_load", Arguments: string(raw)}}}}
}

func integrationSkill(version, body, resource string) agentsdk.SkillSchema {
	return agentsdk.SkillSchema{Key: "report", Version: version, Name: "Report", Description: "Prepare a reviewed report", Instructions: body, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), Resources: []agentsdk.SkillResource{{Key: "template", Name: "Template", MediaType: "text/markdown", Content: resource}}, Workflow: []agentsdk.SkillWorkflowStep{{Key: "inspect", Name: "Inspect", Instructions: "Inspect the source"}}}
}

func publishIntegrationSkill(t *testing.T, repo *agentstore.ConversationStore, skill, baseline agentsdk.SkillSchema) agentsdk.ConversationImprovementCandidate {
	t.Helper()
	a := conversationAuthority()
	feedbackID := "feedback-" + skill.Version
	now := time.Now().UTC().Truncate(time.Millisecond)
	feedbackCreate := agentsdk.ConversationCapabilityFeedbackCreate{ClientID: feedbackID, TaskID: "task-" + skill.Version, Outcome: "revised", Reason: "Reviewed task outcome", SkillKeys: []string{"report"}}
	feedback := agentsdk.ConversationCapabilityFeedback{ID: feedbackID, TaskID: feedbackCreate.TaskID, RunID: "run-" + skill.Version, Outcome: feedbackCreate.Outcome, Reason: feedbackCreate.Reason, AgentID: "default", AgentRevision: 1, TargetSkillKeys: []string{"report"}, SkillVersions: map[string]string{"report": baseline.Version}, CreatedAt: now}
	if _, err := repo.CreateConversationCapabilityFeedback(t.Context(), feedbackID, feedback, feedbackCreate, a); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(skill)
	baselineRaw, _ := json.Marshal(baseline)
	id := "candidate-" + skill.Version
	create := agentsdk.ConversationImprovementCandidateCreate{ClientID: id, Kind: "skill", TargetKey: "report", Version: skill.Version, FeedbackIDs: []string{feedbackID}, Proposal: raw, Reason: "Apply reviewed task feedback"}
	candidate := agentsdk.ConversationImprovementCandidate{ID: id, Kind: create.Kind, TargetKey: create.TargetKey, Version: create.Version, BaselineVersion: baseline.Version, BaselineProposal: baselineRaw, FeedbackIDs: create.FeedbackIDs, Proposal: raw, Reason: create.Reason, Status: "candidate", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := repo.CreateConversationImprovementCandidate(t.Context(), id, candidate, create, a); err != nil {
		t.Fatal(err)
	}
	candidate, err := repo.EvaluateConversationImprovementCandidate(t.Context(), id, agentsdk.ConversationImprovementEvaluationWrite{ClientID: "evaluate-" + skill.Version, ExpectedRevision: 1, SuiteVersion: "v01-skill-" + skill.Version, ScenarioIDs: []string{"skill-on-demand"}, BaselineCompleted: 1, CandidateCompleted: 1, BaselineOmissions: 1, CandidateOmissions: 0, Passed: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err = repo.PublishConversationImprovementCandidate(t.Context(), id, agentsdk.ConversationImprovementPublish{ClientID: "publish-" + skill.Version, ExpectedRevision: candidate.Revision}, a)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestDynamicSkillLoadsOnDemandAndPublishedUpdateInvalidatesActiveRun(t *testing.T) {
	repo := conversationRepository(t)
	a := conversationAuthority()
	authorizer := skillIntegrationAuthorizer{}
	host, err := application.NewPersonalConversationHost(repo, authorizer, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	model := &skillIntegrationModel{changing: make(chan struct{}), resume: make(chan struct{}), t: t}
	options := conversationOptions()
	options.ContextBytes = 32 * 1024
	options.ToolHost, options.PersonalAuthorizer = host, authorizer
	options.Agent = &agentsdk.AgentSchema{Key: "default", Version: "1", Name: "Default", Instructions: "Complete the request", SkillKeys: []string{"report"}}
	skillV1 := integrationSkill("1", "BODY ONE", "RESOURCE ONE")
	options.Skills = []agentsdk.SkillSchema{skillV1}
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "skill-v1"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "skill-v1", Message: "Prepare the report"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, conversation.ID, run.ID); final.Status != "completed" || len(final.Steps) != 3 {
		t.Fatalf("Skill v1 run=%+v", final)
	}
	skillV2 := integrationSkill("2", "BODY TWO", "RESOURCE TWO")
	publishIntegrationSkill(t, repo, skillV2, skillV1)
	second, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "skill-v2", Message: "Prepare another report"}, a)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.changing:
	case <-time.After(5 * time.Second):
		t.Fatal("Skill v2 run did not reach the model")
	}
	publishIntegrationSkill(t, repo, integrationSkill("3", "BODY THREE", "RESOURCE THREE"), skillV2)
	close(model.resume)
	final := waitConversation(t, service, conversation.ID, second.ID)
	if final.Status != "failed" || final.ErrorCode != "agent_changed" {
		t.Fatalf("active run retained superseded Skill configuration: %+v", final)
	}
}
