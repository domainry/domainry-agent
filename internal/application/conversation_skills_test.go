package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/persistence"
	agentdefinition "github.com/domainry/domainry-agent/definition"
)

type capabilityFeedbackRepository struct {
	persistence.ConversationRepository
	persistence.ConversationImprovementRepository
	persistence.ConversationTaskReadRepository
	persistence.ConversationCollaborationRepository
	task           sdk.ConversationTask
	saved          sdk.ConversationCapabilityFeedback
	feedbacks      []sdk.ConversationCapabilityFeedback
	savedCandidate sdk.ConversationImprovementCandidate
}

func (r *capabilityFeedbackRepository) ConversationTask(context.Context, string, sdk.ConversationAuthority) (sdk.ConversationTask, error) {
	return r.task, nil
}

func (r *capabilityFeedbackRepository) Run(context.Context, string, string, sdk.ConversationAuthority) (sdk.ConversationRun, error) {
	return sdk.ConversationRun{ID: r.task.ExecutionRunID, ConversationID: r.task.ExecutionConversationID, Status: "completed", BackgroundTask: &sdk.ConversationTaskExecution{TaskID: r.task.ID}}, nil
}

func (r *capabilityFeedbackRepository) Get(_ context.Context, id string, _ sdk.ConversationAuthority) (sdk.Conversation, error) {
	return sdk.Conversation{ID: id}, nil
}

func (r *capabilityFeedbackRepository) CreateConversationCapabilityFeedback(_ context.Context, _ string, prepared sdk.ConversationCapabilityFeedback, _ sdk.ConversationCapabilityFeedbackCreate, _ sdk.ConversationAuthority) (sdk.ConversationCapabilityFeedback, error) {
	r.saved = prepared
	return prepared, nil
}

func (r *capabilityFeedbackRepository) ConversationCapabilityFeedbacks(context.Context, []string, sdk.ConversationAuthority) ([]sdk.ConversationCapabilityFeedback, error) {
	return append([]sdk.ConversationCapabilityFeedback(nil), r.feedbacks...), nil
}

func (*capabilityFeedbackRepository) PublishedConversationSkills(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationSkillVersion, error) {
	return nil, nil
}

func (*capabilityFeedbackRepository) ConversationCapabilityConfiguration(context.Context, string, string, sdk.ConversationAuthority) (sdk.ConversationCapabilityConfiguration, bool, error) {
	return sdk.ConversationCapabilityConfiguration{}, false, nil
}

func (*capabilityFeedbackRepository) ConversationAgents(context.Context, sdk.ConversationAuthority) ([]sdk.ConversationAgent, error) {
	return nil, nil
}

func (r *capabilityFeedbackRepository) CreateConversationImprovementCandidate(_ context.Context, _ string, prepared sdk.ConversationImprovementCandidate, _ sdk.ConversationImprovementCandidateCreate, _ sdk.ConversationAuthority) (sdk.ConversationImprovementCandidate, error) {
	r.savedCandidate = prepared
	return prepared, nil
}

func TestSkillToolLoadsFrozenBodyWorkflowAndResourcesOnlyOnDemand(t *testing.T) {
	read := sdk.PersonalConversationTools()[1]
	base := &conversationTaskCatalogHost{definitions: []sdk.ConversationToolDefinition{read}, denied: map[string]bool{}}
	skill := sdk.SkillSchema{
		Key: "report", Version: "7", Name: "Report", Description: "Prepare a reviewed report", Instructions: "FULL PRIVATE SKILL BODY", AllowedTools: []string{read.Key},
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		Resources: []sdk.SkillResource{{Key: "template", Name: "Template", Description: "Report headings", MediaType: "text/markdown", Content: "FULL RESOURCE BODY"}},
		Workflow:  []sdk.SkillWorkflowStep{{Key: "inspect", Name: "Inspect", Instructions: "Read the source", AllowedTools: []string{read.Key}}, {Key: "draft", Name: "Draft", Instructions: "Draft the report", DependsOn: []string{"inspect"}}},
	}
	snapshot := &sdk.ConversationAgentSnapshot{Profile: sdk.AgentSchema{Tools: []string{read.Key}}, Skills: []sdk.SkillSchema{skill}}
	ctx := context.WithValue(t.Context(), conversationAgentContextKey{}, snapshot)
	host := &skillToolHost{ConversationToolHost: base, service: &ConversationService{}}
	definitions, err := host.ConversationTools(ctx, sdk.ConversationAuthority{})
	if err != nil || len(definitions) != 2 || definitions[1].Key != "skill_load" {
		t.Fatalf("definitions=%+v err=%v", definitions, err)
	}
	definition := definitions[1]
	request := sdk.ConversationToolRequest{Definition: definition, Call: sdk.ConversationToolCall{ID: "load", Name: "skill_load", Arguments: `{"key":"report","version":"7"}`}}
	auth, err := host.AuthorizeConversationTool(ctx, request)
	if err != nil || !auth.Granted || auth.Revision != conversationDigest(skill) {
		t.Fatalf("authorization=%+v err=%v", auth, err)
	}
	result, err := host.InvokeConversationTool(ctx, request)
	if err != nil || result.Status != "completed" || !strings.Contains(string(result.Content), "FULL PRIVATE SKILL BODY") || !strings.Contains(string(result.Content), "Read the source") || strings.Contains(string(result.Content), "FULL RESOURCE BODY") {
		t.Fatalf("body result=%s err=%v", result.Content, err)
	}
	request.Call.Arguments = `{"key":"report","version":"7","resource_key":"template"}`
	result, err = host.InvokeConversationTool(ctx, request)
	if err != nil || !strings.Contains(string(result.Content), "FULL RESOURCE BODY") || strings.Contains(string(result.Content), "FULL PRIVATE SKILL BODY") {
		t.Fatalf("resource result=%s err=%v", result.Content, err)
	}
	request.Call.Arguments = `{"key":"report","version":"8"}`
	if auth, err = host.AuthorizeConversationTool(ctx, request); err != nil || auth.Granted {
		t.Fatalf("unfrozen version authorization=%+v err=%v", auth, err)
	}
}

func TestSkillLoadIsInternalAndDoesNotBypassAgentTools(t *testing.T) {
	read := sdk.PersonalConversationTools()[1]
	base := &conversationTaskCatalogHost{definitions: []sdk.ConversationToolDefinition{read}, denied: map[string]bool{}}
	skill := sdk.SkillSchema{Key: "report", Version: "1", Name: "Report", Description: "Report", Instructions: "Use the frozen read tool", AllowedTools: []string{read.Key}}
	snapshot := &sdk.ConversationAgentSnapshot{Profile: sdk.AgentSchema{Tools: []string{read.Key}}, Skills: []sdk.SkillSchema{skill}}
	ctx := context.WithValue(t.Context(), conversationAgentContextKey{}, snapshot)
	s := &ConversationService{options: ConversationOptions{ToolHost: &skillToolHost{ConversationToolHost: base}, ToolAvailability: catalogAvailabilityFunc(func(context.Context, sdk.ConversationAuthority, string) (bool, error) { return false, nil })}}
	definitions, _, err := s.executionCatalog(ctx, sdk.ConversationAuthority{})
	if err != nil || len(definitions) != 1 || definitions[0].Key != "skill_load" {
		t.Fatalf("internal catalog=%+v err=%v", definitions, err)
	}
	profile := sdk.AgentSchema{Key: "agent", Version: "1", Name: "Agent", Instructions: "Work", Tools: []string{read.Key}, SkillKeys: []string{skill.Key}}
	skill.AllowedTools = []string{"unselected_write"}
	if _, err := agentdefinition.CompileProfile(profile, []sdk.SkillSchema{skill}, []string{read.Key, "unselected_write"}); err == nil {
		t.Fatal("Skill expanded the Agent tool scope")
	}
}

func TestConversationSkillDetailDoesNotEraseOnDemandResource(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	service := &ConversationService{runtimeID: a.RuntimeID, options: ConversationOptions{
		CollaborationAuthorizer: fixedCollaborationTestPolicy{"discover"},
		Skills: []sdk.SkillSchema{{
			Key: "report", Version: "1", Name: "Report", Description: "Report", Instructions: "body",
			Resources: []sdk.SkillResource{{Key: "template", Name: "Template", MediaType: "text/markdown", Content: "exact resource"}},
		}},
	}}
	detail, err := service.ConversationSkill(t.Context(), "report", "1", a)
	if err != nil || len(detail.Definition.Resources) != 1 || detail.Definition.Resources[0].Content != "" {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	resource, err := service.ConversationSkillResource(t.Context(), "report", "1", "template", a)
	if err != nil || resource.Content != "exact resource" {
		t.Fatalf("resource=%+v err=%v", resource, err)
	}
}

func TestCapabilityFeedbackBindsActualTaskAgentAndAllSkillVersions(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	repo := &capabilityFeedbackRepository{task: sdk.ConversationTask{
		ID: "task-one", Status: sdk.ConversationTaskStatusCompleted, SourceConversationID: "source", ExecutionConversationID: "execution", ExecutionRunID: "run-one",
		Agent: &sdk.ConversationAgentSnapshot{ID: "agent-one", Revision: 7, Profile: sdk.AgentSchema{Version: "7+prompt:prompt-4"}, Skills: []sdk.SkillSchema{{Key: "report", Version: "3"}, {Key: "review", Version: "2"}}}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}
	service := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{CollaborationAuthorizer: fixedCollaborationTestPolicy{"configure"}}}
	out, err := service.CreateConversationCapabilityFeedback(t.Context(), sdk.ConversationCapabilityFeedbackCreate{ClientID: "feedback-one", TaskID: repo.task.ID, Outcome: "revised", Reason: "Changed the conclusion after review", SkillKeys: []string{"report"}}, a)
	if err != nil || out.AgentID != "agent-one" || out.AgentRevision != 7 || out.AgentPromptVersion != "prompt-4" || len(out.TargetSkillKeys) != 1 || out.TargetSkillKeys[0] != "report" || len(out.SkillVersions) != 2 || out.SkillVersions["report"] != "3" || out.SkillVersions["review"] != "2" || repo.saved.ID != out.ID {
		t.Fatalf("feedback=%+v saved=%+v err=%v", out, repo.saved, err)
	}
	detail, err := service.ConversationTask(t.Context(), repo.task.ID, a)
	if err != nil || detail.AgentRevision != 7 || detail.AgentPromptVersion != "prompt-4" || detail.SkillVersions["report"] != "3" || detail.SkillVersions["review"] != "2" {
		t.Fatalf("task configuration projection=%+v err=%v", detail.ConversationTaskSummary, err)
	}
	repo.task.Status = sdk.ConversationTaskStatusRunning
	_, err = service.CreateConversationCapabilityFeedback(t.Context(), sdk.ConversationCapabilityFeedbackCreate{ClientID: "feedback-active", TaskID: repo.task.ID, Outcome: "failed", Reason: "Too early"}, a)
	var coded *sdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.feedback_task_active" {
		t.Fatalf("active feedback err=%v", err)
	}
}

func TestImprovementFeedbackMustTargetExactBaselineConfiguration(t *testing.T) {
	feedback := sdk.ConversationCapabilityFeedback{
		TaskID: "task-one", RunID: "run-one", AgentID: "agent-one", AgentRevision: 7,
		TargetSkillKeys: []string{"report"}, SkillVersions: map[string]string{"report": "3", "review": "2"},
	}
	if !conversationImprovementFeedbackMatches("skill", "report", "3", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("exact targeted Skill baseline was rejected")
	}
	if conversationImprovementFeedbackMatches("skill", "review", "2", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("an untargeted Skill was accepted merely because it was present in the task snapshot")
	}
	if conversationImprovementFeedbackMatches("skill", "report", "4", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("feedback from a stale Skill baseline was accepted")
	}
	if !conversationImprovementFeedbackMatches("agent_prompt", "agent-one", "agent-revision-7", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("exact Agent prompt baseline was rejected")
	}
	feedback.AgentPromptVersion = "prompt-4"
	if !conversationImprovementFeedbackMatches("agent_prompt", "agent-one", "prompt-4", []sdk.ConversationCapabilityFeedback{feedback}) || conversationImprovementFeedbackMatches("agent_prompt", "agent-one", "prompt-3", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("published prompt version was not matched exactly")
	}
	if conversationImprovementFeedbackMatches("agent_prompt", "agent-two", "agent-revision-7", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("feedback from another Agent was accepted")
	}
	if !conversationImprovementFeedbackMatches("delegation_strategy", "default", "builtin-1", []sdk.ConversationCapabilityFeedback{feedback}) {
		t.Fatal("reviewed task feedback was not accepted for the default delegation strategy")
	}
}

func TestCreateSkillImprovementRequiresFeedbackForTargetAndCurrentBaseline(t *testing.T) {
	a := sdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}
	repo := &capabilityFeedbackRepository{feedbacks: []sdk.ConversationCapabilityFeedback{{ID: "feedback-one", TaskID: "task-one", RunID: "run-one", TargetSkillKeys: []string{"report"}, SkillVersions: map[string]string{"report": "3", "review": "2"}}}}
	service := &ConversationService{runtimeID: a.RuntimeID, repo: repo, options: ConversationOptions{
		CollaborationAuthorizer: fixedCollaborationTestPolicy{"configure"},
		Skills: []sdk.SkillSchema{
			{Key: "report", Version: "3", Name: "Report", Description: "Report", Instructions: "Current report"},
			{Key: "review", Version: "2", Name: "Review", Description: "Review", Instructions: "Current review"},
		},
	}}
	proposal := sdk.SkillSchema{Key: "report", Version: "4", Name: "Report", Description: "Report", Instructions: "Apply the reviewed correction"}
	raw, _ := json.Marshal(proposal)
	out, err := service.CreateConversationImprovementCandidate(t.Context(), sdk.ConversationImprovementCandidateCreate{ClientID: "candidate-one", Kind: "skill", TargetKey: "report", Version: "4", FeedbackIDs: []string{"feedback-one"}, Proposal: raw, Reason: "Apply reviewed correction"}, a)
	var baseline sdk.SkillSchema
	baselineErr := json.Unmarshal(out.BaselineProposal, &baseline)
	if err != nil || baselineErr != nil || out.BaselineVersion != "3" || baseline.Key != "report" || baseline.Version != "3" || baseline.Instructions != "Current report" || repo.savedCandidate.ID != out.ID {
		t.Fatalf("candidate=%+v saved=%+v err=%v", out, repo.savedCandidate, err)
	}
	proposal.Key, proposal.Version = "review", "3"
	raw, _ = json.Marshal(proposal)
	_, err = service.CreateConversationImprovementCandidate(t.Context(), sdk.ConversationImprovementCandidateCreate{ClientID: "candidate-unrelated", Kind: "skill", TargetKey: "review", Version: "3", FeedbackIDs: []string{"feedback-one"}, Proposal: raw, Reason: "Unrelated feedback"}, a)
	var coded *sdk.Error
	if !errors.As(err, &coded) || coded.Code != "agent.conversation.improvement_feedback_mismatch" {
		t.Fatalf("unrelated feedback err=%v", err)
	}
}
