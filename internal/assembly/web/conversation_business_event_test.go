package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type businessEventWebModel struct {
	mu      sync.Mutex
	prompts []string
}

func (*businessEventWebModel) GenerateConversation(context.Context, sdk.ConversationModelRequest) (sdk.ConversationModelResult, error) {
	return sdk.ConversationModelResult{Content: "event handled", Model: "event-model"}, nil
}

func (*businessEventWebModel) ConversationModelIdentity() sdk.ConversationModelIdentity {
	return sdk.ConversationModelIdentity{Provider: "fixture", Protocol: "chat_completions", Model: "event-model", Fingerprint: "event-model-v1"}
}

func (m *businessEventWebModel) StreamConversationStep(_ context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	if len(in.Messages) != 0 {
		m.prompts = append(m.prompts, in.Messages[len(in.Messages)-1].Content)
	}
	m.mu.Unlock()
	return sdk.ConversationStepResult{FinishReason: "stop", Model: "event-model", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "业务事件已处理。"}}, nil
}

func (m *businessEventWebModel) lastPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.prompts) == 0 {
		return ""
	}
	return m.prompts[len(m.prompts)-1]
}

func TestBusinessEventCreatesAndWakesAgentTaskThroughCurrentIdentity(t *testing.T) {
	const initial, changed = "Initial-Business-Event!2", "Changed-Business-Event!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "business-event-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "business-event-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &businessEventWebModel{}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "business-event.db"), RuntimeID: "event-runtime", WorkspaceID: "event-workspace", ApplicationKey: "event-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"event-model": model}, Poll: 5 * time.Millisecond, Lease: 300 * time.Millisecond}},
	}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "event-model", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	browser := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	browser.login("admin@example.com", initial)
	browser.changePassword(initial, changed)
	grantPersonalTools(t, host, browser, true)
	setTestCollaborationPermissions(t, host, browser, "discover", "configure")
	agent := accountDecode[sdk.ConversationAgent](t, browser.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "event-agent", Name: "Event Agent", Instructions: "Handle verified events", Tools: []string{}, SkillKeys: []string{}, ModelKey: "event-model", Enabled: true, MaxConcurrent: 1}), 200))
	conversation := accountDecode[sdk.Conversation](t, browser.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{AgentID: agent.ID, ClientID: "event-conversation", Title: "Event work"}), 200))
	service := host.Agent.(sdk.ConversationBinding).Conversations()
	events := service.(sdk.BusinessEventConversationTaskService)
	authority := sdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	request := sdk.BusinessEventConversationTaskRequest{
		ContractVersion: sdk.BusinessEventConversationTaskContractVersion, Authority: authority, ConversationID: conversation.ID, AgentID: agent.ID, Mode: "start", IdempotencyKey: "event-1:ticket-opened",
		Source: sdk.ConversationBusinessEventSource{EventID: "event-1", Provider: "support", EventType: "ticket.opened", ExternalID: "ticket-42", ReceivedAt: time.Date(2026, 9, 16, 9, 30, 0, 0, time.UTC)},
		Rule:   sdk.ConversationBusinessEventRule{Key: "ticket-opened", Revision: strings.Repeat("a", 64)},
		Input:  sdk.ConversationTaskStart{Goal: "Review ticket 42", Input: `{"goal":"Review ticket 42","ticket_id":"42"}`, AllowedTools: []string{}},
	}
	if _, err = events.AcceptBusinessEventConversationTask(t.Context(), request); err == nil {
		t.Fatal("business event reached Agent without the Runtime service action")
	}
	ctx := sdk.WithAuthorizedServiceAction(t.Context(), sdk.ActionAgentBusinessEventConversationTaskAccept, sdk.AgentRuntimeServiceAudience)
	receipt, err := events.AcceptBusinessEventConversationTask(ctx, request)
	if err != nil || receipt.Replay || receipt.Task.ID == "" {
		t.Fatalf("business-event receipt=%+v err=%v", receipt, err)
	}
	replay, err := events.AcceptBusinessEventConversationTask(ctx, request)
	if err != nil || !replay.Replay || replay.Task.ID != receipt.Task.ID {
		t.Fatalf("business-event replay=%+v err=%v", replay, err)
	}
	tasks := service.(sdk.ConversationTaskService)
	var first sdk.ConversationTaskDetail
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		first, err = tasks.ConversationTask(t.Context(), receipt.Task.ID, authority)
		if err != nil {
			t.Fatal(err)
		}
		if first.Status == sdk.ConversationTaskStatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first.Status != sdk.ConversationTaskStatusCompleted || first.BusinessEvent == nil || first.BusinessEvent.Source.EventID != "event-1" || first.BusinessEvent.Execution.UserID != "admin" || first.BusinessEvent.TargetAgentID != agent.ID {
		t.Fatalf("completed business-event task=%+v", first)
	}
	var pageDetail sdk.ConversationTaskDetail
	if err = json.Unmarshal(browser.call("GET", "/agent/conversation-tasks/"+receipt.Task.ID, "", 200).Body.Bytes(), &pageDetail); err != nil || pageDetail.BusinessEvent == nil || pageDetail.BusinessEvent.Rule.Key != "ticket-opened" {
		t.Fatalf("HTTP business-event detail=%+v err=%v", pageDetail, err)
	}
	if prompt := model.lastPrompt(); !strings.Contains(prompt, `"event_id":"event-1"`) || !strings.Contains(prompt, `"target_agent_id":"`+agent.ID+`"`) {
		t.Fatalf("model prompt omitted verified event reference: %q", prompt)
	}
	wake := request
	wake.Mode, wake.RelatedTaskID, wake.IdempotencyKey = "wake", receipt.Task.ID, "event-2:ticket-escalated"
	wake.Source = sdk.ConversationBusinessEventSource{EventID: "event-2", Provider: "support", EventType: "ticket.escalated", ExternalID: "ticket-42:escalated", ReceivedAt: request.Source.ReceivedAt.Add(time.Minute)}
	wake.Rule = sdk.ConversationBusinessEventRule{Key: "ticket-escalated", Revision: strings.Repeat("b", 64)}
	wake.Input.Goal, wake.Input.Input = "Handle ticket escalation", `{"goal":"Handle ticket escalation","ticket_id":"42"}`
	woken, err := events.AcceptBusinessEventConversationTask(ctx, wake)
	if err != nil || woken.Replay || woken.Task.ID == receipt.Task.ID || woken.Task.BusinessEvent == nil || woken.Task.BusinessEvent.RelatedTaskID != receipt.Task.ID {
		t.Fatalf("business-event wake successor=%+v err=%v", woken, err)
	}
	wrong := request
	wrong.IdempotencyKey, wrong.Source.EventID, wrong.AgentID = "event-3:wrong-agent", "event-3", "agent-missing"
	if _, err = events.AcceptBusinessEventConversationTask(ctx, wrong); err == nil {
		t.Fatal("event mapping targeted an Agent other than the conversation binding")
	}
}
