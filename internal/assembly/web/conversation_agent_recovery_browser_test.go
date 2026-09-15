package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/infrastructure/provider"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type agentRecoveryBrowserModel struct {
	mu    sync.Mutex
	calls int
}

func (*agentRecoveryBrowserModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "recovery", Fingerprint: "recovery-v1"}
}

func (*agentRecoveryBrowserModel) ConversationStepInputBytes(in sdk.ConversationStepRequest) (int, error) {
	return provider.ConversationStepInputBytes(provider.ConversationModelConfig{Provider: in.ModelIdentity.Provider, Protocol: in.ModelIdentity.Protocol, Model: in.ModelIdentity.Model, MaxOutputTokens: 1024}, in)
}

func (*agentRecoveryBrowserModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{Content: "ready"}, nil
}

func (m *agentRecoveryBrowserModel) StreamConversationStep(context.Context, sdk.ConversationStepRequest, func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.mu.Unlock()
	if call == 1 {
		return sdk.ConversationStepResult{}, &sdk.Error{Class: "conflict", Code: "agent.conversation.agent_changed"}
	}
	return sdk.ConversationStepResult{Usage: map[string]any{"input_tokens": 100, "output_tokens": 20}, FinishReason: "stop", Model: "recovery", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Recovered with the current immutable Agent configuration."}}, nil
}

func TestPeerSameAgentRecoveryHTTPAndBrowser(t *testing.T) {
	if os.Getenv("AGENT_C07_BROWSER") != "1" || os.Getenv("AGENT_NODE_BINARY") == "" || os.Getenv("AGENT_UI_TEST_OUTPUT") == "" {
		t.Skip("opt-in C07 built-client browser acceptance")
	}
	const initial, changed = "Agent-Recovery-Initial!22", "Agent-Recovery-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "agent-recovery-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "agent-recovery-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &agentRecoveryBrowserModel{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "agent-recovery.db"), RuntimeID: "agent-recovery-runtime", WorkspaceID: "agent-recovery-workspace", ApplicationKey: "agent-recovery-app", Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"recovery": model}, Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()

	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "recovery", Files: fstest.MapFS{"index.html": {Data: []byte("fixture")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: seed, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantCollaborationPermissions(t, host, b)

	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "recovery", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()

	agent := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "recovery-agent", Name: "Recovery Agent", Description: "Recover changed configuration", Instructions: "Complete the delegated review", ModelKey: "recovery", Enabled: true, MaxConcurrent: 1}), 200))
	source := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "recovery-source", Title: "Recovery work"}), 200))
	budget := sdk.ConversationWorkBudget{MaxInputTokens: 5_000_000, MaxOutputTokens: 1_000_000, MaxDurationSeconds: 600}
	created := sdk.ConversationDelegationCreate{ClientID: "recovery-work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "Prove same-Agent recovery", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "Recover this changed Agent assignment", Deliverable: "Recovered result", CompletionConditions: []string{"Use the current Agent configuration"}}, WorkBudget: &budget}
	d := accountDecode[sdk.ConversationDelegationDetail](t, b.call("POST", "/agent/delegations", accountJSON(created), 200))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		d = accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", "/agent/delegations/"+d.ID, "", 200))
		if d.Task != nil && d.Task.Status == sdk.ConversationTaskStatusFailed && d.Task.ErrorCode == "agent_changed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.Task == nil || d.Task.ErrorCode != "agent_changed" {
		t.Fatalf("recoverable terminal task missing: %+v", d.Task)
	}
	oldConversation, oldTask := d.ConversationID, d.Task.ID
	update := sdk.ConversationAgentWrite{ClientID: "reconfigure-agent", ExpectedRevision: agent.Revision, Name: agent.Name, Description: agent.Description, Instructions: agent.Instructions + " using the repaired configuration", Tools: agent.Tools, SkillKeys: agent.SkillKeys, ModelKey: agent.ModelKey, Enabled: true, MaxConcurrent: agent.MaxConcurrent}
	agent = accountDecode[sdk.ConversationAgent](t, b.call("PUT", "/agent/agents/"+agent.ID, accountJSON(update), 200))

	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/agent-recovery.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+source.ID, "AGENT_UI_DELEGATION="+d.ID, "AGENT_UI_AGENT="+agent.ID, "AGENT_UI_PASSWORD="+changed, "AGENT_UI_OLD_CONVERSATION="+oldConversation, "AGENT_UI_OLD_TASK="+oldTask)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal(err)
	}

	d = accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", "/agent/delegations/"+d.ID, "", 200))
	if d.AssignmentNumber != 2 || d.ToAgentID != agent.ID || d.ConversationID == oldConversation || d.Task == nil || d.Task.ID == oldTask || d.WorkBudget == nil || d.WorkUsage == nil || d.WorkUsage.InputTokens != 100 || d.WorkUsage.OutputTokens != 20 {
		raw, _ := json.Marshal(d)
		t.Fatalf("same-Agent recovery lost history or whole-work usage: %s", raw)
	}
}
