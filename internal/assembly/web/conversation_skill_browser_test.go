package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type skillBrowserModel struct{ calls atomic.Int32 }

func (*skillBrowserModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "responses", Model: "skill-browser", Fingerprint: "skill-browser-v1"}
}

func (*skillBrowserModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{}, errors.New("skill browser fixture requires the step protocol")
}

func (m *skillBrowserModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.calls.Add(1)
	var prompt strings.Builder
	for _, message := range in.Messages {
		prompt.WriteString("\n")
		prompt.WriteString(message.Content)
	}
	text := prompt.String()
	if !strings.Contains(text, "report @ 1") || strings.Contains(text, "K01 FULL BODY ONE") || strings.Contains(text, "K01 RESOURCE ONE") {
		return sdk.ConversationStepResult{}, errors.New("background task did not receive a summary-only frozen Skill catalog")
	}
	return sdk.ConversationStepResult{FinishReason: "stop", Model: "skill-browser", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "K01 浏览器任务已完成，可记录采用或修订反馈。"}}, nil
}

func TestDynamicSkillFeedbackPublicationRollbackBuiltBrowser(t *testing.T) {
	if os.Getenv("AGENT_K01_BROWSER") != "1" || os.Getenv("AGENT_NODE_BINARY") == "" || os.Getenv("AGENT_UI_TEST_OUTPUT") == "" {
		t.Skip("opt-in K01 dynamic Skill browser acceptance")
	}
	const initial, changed = "Skill-Browser-Initial!22", "Skill-Browser-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "skill-browser-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "skill-browser-data-key-long-enough")
	t.Setenv("APP_ENV", "development")

	model := &skillBrowserModel{}
	skill := sdk.SkillSchema{
		Key: "report", Version: "1", Name: "Report Skill", Description: "Prepare a reviewed report", Instructions: "K01 FULL BODY ONE",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		Resources: []sdk.SkillResource{{Key: "template", Name: "Report Template", MediaType: "text/markdown", Content: "K01 RESOURCE ONE"}},
		Workflow:  []sdk.SkillWorkflowStep{{Key: "inspect", Name: "Inspect source", Instructions: "Inspect the exact source before drafting."}},
	}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "skill-browser.db"), RuntimeID: "skill-browser-runtime", WorkspaceID: "skill-browser-workspace", ApplicationKey: "skill-browser-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{
			Poll: 5 * time.Millisecond, Agent: &sdk.AgentSchema{Key: "default", Version: "1", Name: "Default", Instructions: "Complete the assigned work", SkillKeys: []string{"report"}}, Skills: []sdk.SkillSchema{skill},
		}},
	}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	seed, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "skill-browser", Files: os.DirFS("../../../frontend/dist")})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: seed, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantCollaborationPermissions(t, host, b)

	var conversation sdk.Conversation
	if err = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"skill-browser","title":"K01 Skill 验收"}`, 200).Body.Bytes(), &conversation); err != nil {
		t.Fatal(err)
	}
	service := host.Agent.(sdk.ConversationBinding).Conversations()
	authority := sdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	scheduled := sdk.ScheduledConversationTaskRequest{
		ContractVersion: sdk.ScheduledConversationTaskContractVersion, PlanID: "skill-browser-plan", SchedulerRunID: "skill-browser-schedule-run", IdempotencyKey: "skill-browser-window", ScheduledFor: time.Now().UTC(), Authority: authority, ConversationID: conversation.ID,
		Input: sdk.ConversationTaskStart{Goal: "K01 记录真实任务反馈", Input: "完成一份用于能力改进反馈的报告", AllowedTools: []string{}, Budget: sdk.ConversationTaskBudget{MaxSteps: 2, MaxToolCalls: 1, MaxOutputBytes: 4096, TimeoutSeconds: 30}},
	}
	ctx := sdk.WithAuthorizedServiceAction(t.Context(), sdk.ActionAgentScheduledConversationTaskStart, sdk.AgentRuntimeServiceAudience)
	receipt, err := service.(sdk.ScheduledConversationTaskService).StartScheduledConversationTask(ctx, scheduled)
	if err != nil {
		t.Fatal(err)
	}
	var task sdk.ConversationTaskDetail
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		task, err = service.(sdk.ConversationTaskService).ConversationTask(t.Context(), receipt.Task.ID, authority)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == sdk.ConversationTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task.Status != sdk.ConversationTaskStatusCompleted || task.AgentID != "default" || task.SkillVersions["report"] != "1" {
		t.Fatalf("seed task=%+v", task)
	}

	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "skill-browser", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()

	runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(runCtx, os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/dynamic-skill.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_TEST_OUTPUT="+os.Getenv("AGENT_UI_TEST_OUTPUT"), "AGENT_UI_PASSWORD="+changed)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal("dynamic Skill browser acceptance", err)
	}
	page, err := service.(sdk.ConversationSkillService).ConversationSkills(t.Context(), authority)
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != "1" {
		t.Fatalf("rolled back Skill catalog=%+v err=%v", page, err)
	}
	candidates, err := service.(sdk.ConversationSkillService).ConversationImprovementCandidates(t.Context(), authority)
	if err != nil || len(candidates.Items) != 2 || !candidates.Items[0].BaselineSnapshot && !candidates.Items[1].BaselineSnapshot {
		t.Fatalf("improvement versions=%+v err=%v", candidates, err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("configuration management unexpectedly invoked model: %d", model.calls.Load())
	}
	raw, _ := json.MarshalIndent(map[string]any{"complete": true, "compiled_frontend": true, "real_identity_http": true, "real_sqlite": true, "task_id": task.ID, "model_calls": model.calls.Load(), "active_skill_version": page.Items[0].Version, "candidate_versions": len(candidates.Items)}, "", "  ")
	if err = os.WriteFile(filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), "host-dynamic-skill.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
