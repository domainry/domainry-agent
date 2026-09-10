package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/internal/application"
	agentremote "github.com/domainry/domainry-agent/remote"
	agentserver "github.com/domainry/domainry-agent/server"
)

func waitConversationState(t *testing.T, s agentsdk.ConversationService, id, runID, status string) agentsdk.ConversationRun {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := s.Run(t.Context(), id, runID, conversationAuthority())
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == status {
			return run
		}
		if run.Terminal() {
			t.Fatalf("expected %s, got %s: %s", status, run.Status, run.ErrorCode)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run did not reach %s", status)
	return agentsdk.ConversationRun{}
}

type interactionHost struct {
	executionHost
	question       bool
	confirm        bool
	respondAllowed bool
	version        string
	denyApproved   bool
}

func (h *interactionHost) ConversationTools(ctx context.Context, a agentsdk.ConversationAuthority) ([]agentsdk.ConversationToolDefinition, error) {
	if h.question {
		for _, definition := range agentsdk.PersonalConversationTools() {
			if definition.Key == "ask_user" {
				return []agentsdk.ConversationToolDefinition{definition}, nil
			}
		}
	}
	definitions, err := h.executionHost.ConversationTools(ctx, a)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.version != "" {
		definitions[0].Version = h.version
	}
	return definitions, err
}
func (h *interactionHost) AuthorizeConversationTool(ctx context.Context, in agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	auth, err := h.executionHost.AuthorizeConversationTool(ctx, in)
	if h.confirm && in.Definition.Effect == "write" {
		auth.ConfirmationRequired = true
		if receipt := in.Confirmation; receipt != nil {
			raw, _ := json.Marshal(in.Call.Arguments)
			digest := sha256.Sum256(raw)
			if receipt.ID == in.ConfirmationID && receipt.UserID == in.Authority.UserID && receipt.ActionKey == in.Definition.ActionKey && receipt.ToolVersion == in.Definition.Version && receipt.ArgumentsHash == hex.EncodeToString(digest[:]) && !receipt.ApprovedAt.IsZero() {
				auth.ConfirmationRequired = false
			}
		}
		if h.denyApproved && in.Confirmation != nil {
			auth.Granted = false
		}
	}
	return auth, err
}
func (h *interactionHost) AuthorizeConversationInteraction(_ context.Context, a agentsdk.ConversationAuthority, _ agentsdk.ConversationInteraction) (agentsdk.ConversationToolAuthorization, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return agentsdk.ConversationToolAuthorization{Granted: h.respondAllowed && a.UserID == conversationAuthority().UserID}, nil
}

func TestConversationQuestionPersistsAcrossServiceRestartAndContinuesSameCall(t *testing.T) {
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true}, question: true, respondAllowed: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "question", Name: "ask_user", Arguments: `{"question":"选择周报格式","choices":["简洁版","详细版"]}`}}}, FinishReason: "tool_calls"}, nil
		}
		last := in.Messages[len(in.Messages)-1]
		tool := in.Messages[len(in.Messages)-2]
		if tool.Role != "tool" || tool.ToolCallID != "question" || last.Role != "user" || last.Content != "详细版，按项目组织" || !strings.Contains(tool.Content, last.Content) {
			t.Error("answer not paired with original call")
		}
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", Content: "已按项目组织。"}, FinishReason: "stop"}, nil
	}
	options := application.ConversationOptions{ToolHost: host, Workers: 1, Poll: 10 * time.Millisecond}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	a := conversationAuthority()
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "question"}, a)
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "帮我整理周报"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_user")
	if waiting.Interaction == nil || len(waiting.Interaction.Choices) != 2 {
		t.Fatal("missing question")
	}
	service.Close()
	service, err = application.NewConversationService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "answer", ExpectedRevision: 1, Decision: "answer", Answer: "详细版，按项目组织"}
	host.mu.Lock()
	host.respondAllowed = false
	host.mu.Unlock()
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err == nil {
		t.Fatal("missing response permission accepted")
	}
	host.mu.Lock()
	host.respondAllowed = true
	host.mu.Unlock()
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal(err)
	}
	final := waitConversationState(t, service, c.ID, run.ID, "completed")
	if final.Attempt != 2 || final.Interaction.Status != "answered" || len(final.Steps) != 2 {
		t.Fatal("did not continue original step")
	}
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal("duplicate answer", err)
	}
	messages, _ := service.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, a)
	if len(messages.Items) != 3 || messages.Items[1].Content != response.Answer {
		t.Fatal("original answer not preserved")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 0 {
		t.Fatal("question invoked external host")
	}
}

func TestConversationConfirmationDuplicatesAndUnknownResultKeepOriginalAuthorization(t *testing.T) {
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true, uncertain: true}, confirm: true, respondAllowed: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return model.callResult(`{"title":"one"}`), nil
		}
		return model.answerResult(), nil
	}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "confirmation"}, a)
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "Create one"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
	host.mu.Lock()
	if host.invokes != 0 {
		t.Error("effect before confirmation")
	}
	host.mu.Unlock()
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: 1, Decision: "approve"}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := service.Respond(t.Context(), c.ID, run.ID, response, a); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	unknown := waitConversationState(t, service, c.ID, run.ID, "needs_reconciliation")
	if unknown.Interaction.ID == waiting.Interaction.ID {
		t.Fatal("reconciliation overwrote confirmation")
	}
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal("late duplicate", err)
	}
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	final := waitConversationState(t, service, c.ID, run.ID, "completed")
	if final.Interaction.Status != "resolved" {
		t.Fatal("unresolved interaction")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 1 || host.reconciles != 1 || len(host.keys) != 2 || host.keys[0] != host.keys[1] {
		t.Fatal("duplicate external write or lost idempotency")
	}
}

func TestConversationConfirmationRejectsChangedToolsAndRevokedAccess(t *testing.T) {
	for _, change := range []string{"tool", "permission", "after_approval", "reject", "expired"} {
		t.Run(change, func(t *testing.T) {
			repo := conversationRepository(t)
			host := &interactionHost{executionHost: executionHost{allowed: true}, confirm: true, respondAllowed: true}
			host.denyApproved = change == "after_approval"
			model := &executionModel{}
			model.step = func(int, agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				return model.callResult(`{"title":"one"}`), nil
			}
			options := application.ConversationOptions{ToolHost: host, Poll: 10 * time.Millisecond}
			if change == "expired" {
				options.InteractionTTL = 50 * time.Millisecond
			}
			service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			a := conversationAuthority()
			c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: change}, a)
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "Create one"}, a)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
			response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: 1, Decision: "approve"}
			host.mu.Lock()
			if change == "tool" {
				host.version = "2"
			}
			if change == "permission" {
				host.allowed = false
			}
			host.mu.Unlock()
			if change == "expired" {
				final := waitConversationState(t, service, c.ID, run.ID, "failed")
				if final.ErrorCode != "interaction_expired" {
					t.Fatal(final.ErrorCode)
				}
			} else {
				if change == "reject" {
					response.Decision = "reject"
				}
				_, err = service.Respond(t.Context(), c.ID, run.ID, response, a)
				if change == "reject" {
					if err != nil {
						t.Fatal(err)
					}
					waitConversationState(t, service, c.ID, run.ID, "cancelled")
				} else if change == "after_approval" {
					if err != nil {
						t.Fatal(err)
					}
					final := waitConversationState(t, service, c.ID, run.ID, "failed")
					if final.ErrorCode != "tool_access_denied" {
						t.Fatal(final.ErrorCode)
					}
				} else if err == nil {
					t.Fatalf("accepted %s", change)
				}
			}
			host.mu.Lock()
			defer host.mu.Unlock()
			if host.invokes != 0 {
				t.Fatal("unauthorized effect")
			}
		})
	}
}

func TestConversationQuestionCannotShareAStepWithOtherCalls(t *testing.T) {
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true}, question: true, respondAllowed: true}
	model := &executionModel{}
	model.step = func(int, agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "one", Name: "ask_user", Arguments: `{"question":"第一项？"}`}, {ID: "two", Name: "ask_user", Arguments: `{"question":"第二项？"}`}}}, FinishReason: "tool_calls"}, nil
	}
	service, err := application.NewConversationService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	a := conversationAuthority()
	c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "mixed-question"}, a)
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "整理事项"}, a)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, c.ID, run.ID)
	if final.ErrorCode != "question_must_be_separate" || final.Interaction != nil {
		t.Fatalf("mixed question accepted: %+v", final)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.invokes != 0 {
		t.Fatal("effect from an invalid mixed step")
	}
}

func TestConversationInteractionRemoteRoundTrip(t *testing.T) {
	a := conversationAuthority()
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true}, question: true, respondAllowed: true}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if number == 1 {
			return agentsdk.ConversationStepResult{Message: agentsdk.ConversationStepMessage{Role: "assistant", ToolCalls: []agentsdk.ConversationToolCall{{ID: "ask", Name: "ask_user", Arguments: `{"question":"请选择格式"}`}}}, FinishReason: "tool_calls"}, nil
		}
		if in.Messages[len(in.Messages)-1].Content != "按项目分组" {
			t.Error("remote answer lost")
		}
		return model.answerResult(), nil
	}
	service, err := application.NewConversationService(repo, model, a.RuntimeID, application.ConversationOptions{ToolHost: host})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	server, err := agentserver.New(agentserver.Config{APIKey: "interaction-test-secret", Conversations: service, ConversationRuntimeID: a.RuntimeID})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(server.Handler())
	defer upstream.Close()
	binding, err := agentremote.NewFactory(agentremote.Options{BaseURL: upstream.URL, APIKey: "interaction-test-secret", Client: upstream.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, saasRuntimeHost(a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	client := binding.(agentsdk.ConversationBinding).Conversations()
	interactions, ok := client.(agentsdk.ConversationInteractionService)
	if !ok {
		t.Fatal("remote interaction extension missing")
	}
	c, err := client.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "remote-question"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "整理周报"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, client, c.ID, run.ID, "waiting_user")
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "answer", ExpectedRevision: 1, Decision: "answer", Answer: "按项目分组"}
	if _, err = interactions.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal(err)
	}
	waitConversationState(t, client, c.ID, run.ID, "completed")
	if _, err = interactions.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal("remote duplicate response", err)
	}
}
