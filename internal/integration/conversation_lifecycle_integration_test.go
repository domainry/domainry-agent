package integration_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	application "github.com/domainry/domainry-agent/internal/application"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
)

type lifecycleTestExtension struct {
	definition agentsdk.ConversationLifecycleExtensionDefinition
	handle     func(agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error)
}

func (e *lifecycleTestExtension) ConversationLifecycleDefinition() agentsdk.ConversationLifecycleExtensionDefinition {
	return e.definition
}

func (e *lifecycleTestExtension) HandleConversationLifecycle(_ context.Context, event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
	if e.handle == nil {
		return agentsdk.ConversationLifecycleDecision{}, nil
	}
	return e.handle(event)
}

func lifecycleTestDefinition(key string, order int, kind, failure string, stages ...agentsdk.ConversationLifecycleStage) agentsdk.ConversationLifecycleExtensionDefinition {
	return agentsdk.ConversationLifecycleExtensionDefinition{Key: key, Version: "1", ConfigurationVersion: "test-1", Order: order, Kind: kind, FailureMode: failure, Stages: stages}
}

func TestConversationLifecycleComposesPlanningCompactionToolsAndObservation(t *testing.T) {
	repo := conversationRepository(t)
	host := &executionHost{allowed: true}
	var mu sync.Mutex
	observed := []string{}
	stepInputs := []agentsdk.ConversationStepRequest{}
	record := func(key string, stage agentsdk.ConversationLifecycleStage) {
		mu.Lock()
		observed = append(observed, key+":"+string(stage))
		mu.Unlock()
	}
	planner := &lifecycleTestExtension{
		definition: lifecycleTestDefinition("planner", 10, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleContextAssembling),
		handle: func(event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
			record("planner", event.Stage)
			return agentsdk.ConversationLifecycleDecision{PlanningInstructions: []string{"PLAN_MARKER: verify the saved receipt before answering."}}, nil
		},
	}
	compactor := &lifecycleTestExtension{
		definition: lifecycleTestDefinition("compactor", 20, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleContextCompacting),
		handle: func(event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
			record("compactor", event.Stage)
			return agentsdk.ConversationLifecycleDecision{ContextLimitBytes: 20_000}, nil
		},
	}
	allStages := []agentsdk.ConversationLifecycleStage{
		agentsdk.ConversationLifecycleInputReceived, agentsdk.ConversationLifecycleContextAssembling,
		agentsdk.ConversationLifecycleContextCompacting, agentsdk.ConversationLifecycleContextAssembled,
		agentsdk.ConversationLifecycleModelRequest, agentsdk.ConversationLifecycleModelCompleted,
		agentsdk.ConversationLifecycleModelFailed, agentsdk.ConversationLifecycleModelRetry,
		agentsdk.ConversationLifecycleToolBefore, agentsdk.ConversationLifecycleToolCompleted,
		agentsdk.ConversationLifecycleToolFailed, agentsdk.ConversationLifecycleRunFinished,
	}
	observer := &lifecycleTestExtension{
		definition: lifecycleTestDefinition("observer", 30, agentsdk.ConversationLifecycleKindObserver, agentsdk.ConversationLifecycleFailureContinue, allStages...),
		handle: func(event agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
			record("observer", event.Stage)
			if event.EventID == "" || event.ContractVersion != agentsdk.ConversationLifecycleContractVersion || event.Manifest == nil {
				t.Error("lifecycle event identity or frozen manifest missing")
			}
			if event.ModelRequest != nil && len(event.ModelRequest.Messages) > 0 {
				event.ModelRequest.Messages[0].Content = "MUTATED"
			}
			if event.StepRequest != nil && len(event.StepRequest.Messages) > 0 {
				event.StepRequest.Messages[0].Content = "MUTATED"
			}
			if event.ToolCall != nil {
				event.ToolCall.Arguments = `{"title":"MUTATED"}`
			}
			if event.Stage == agentsdk.ConversationLifecycleToolBefore {
				host.mu.Lock()
				authorized, invoked := host.authorizes, host.invokes
				host.mu.Unlock()
				if authorized == 0 || invoked != 0 {
					t.Errorf("tool.before must follow authorization and precede invocation: authorization=%d invocation=%d", authorized, invoked)
				}
			}
			if event.Stage == agentsdk.ConversationLifecycleContextAssembled {
				return agentsdk.ConversationLifecycleDecision{}, errors.New("ignored observer outage")
			}
			return agentsdk.ConversationLifecycleDecision{}, nil
		},
	}
	model := &executionModel{}
	model.step = func(number int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		mu.Lock()
		stepInputs = append(stepInputs, in)
		mu.Unlock()
		if number == 1 {
			return model.callResult(`{"title":"original"}`), nil
		}
		return model.answerResult(), nil
	}
	service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{
		ToolHost: host, LifecycleExtensions: []agentsdk.ConversationLifecycleExtension{observer, compactor, planner},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	authority := conversationAuthority()
	conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "lifecycle-composition"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "lifecycle-message", Message: "create the item"}, authority)
	if err != nil {
		t.Fatal(err)
	}
	if run.Lifecycle == nil || len(run.Lifecycle.Extensions) != 3 || run.Lifecycle.Extensions[0].Key != "planner" || run.Lifecycle.Extensions[1].Key != "compactor" || run.Lifecycle.Extensions[2].Key != "observer" {
		t.Fatalf("ordered lifecycle manifest was not frozen: %+v", run.Lifecycle)
	}
	final := waitConversation(t, service, conversation.ID, run.ID)
	if final.Status != "completed" {
		t.Fatalf("run failed: %+v", final)
	}
	if len(final.ModelAttempts) != 2 || final.ModelAttempts[0].Status != "completed" || final.ModelAttempts[1].Status != "completed" {
		t.Fatalf("model attempts were not durably completed before lifecycle observation: %+v", final.ModelAttempts)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		hasFinished := false
		for _, item := range observed {
			hasFinished = hasFinished || item == "observer:"+string(agentsdk.ConversationLifecycleRunFinished)
		}
		mu.Unlock()
		if hasFinished || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	inputs := append([]agentsdk.ConversationStepRequest(nil), stepInputs...)
	events := append([]string(nil), observed...)
	mu.Unlock()
	if len(inputs) != 2 || inputs[0].ContextWindow == nil || inputs[0].ContextWindow.LimitBytes != 20_000 {
		t.Fatalf("compaction policy was not frozen into step input: %+v", inputs)
	}
	joined := ""
	for _, message := range inputs[0].Messages {
		joined += message.Content
	}
	if !strings.Contains(joined, "PLAN_MARKER") || strings.Contains(joined, "MUTATED") {
		t.Fatalf("planning decision or event isolation failed: %s", joined)
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 {
		t.Fatalf("tool was invoked %d times", invokes)
	}
	for _, required := range []string{
		"observer:" + string(agentsdk.ConversationLifecycleInputReceived),
		"planner:" + string(agentsdk.ConversationLifecycleContextAssembling),
		"observer:" + string(agentsdk.ConversationLifecycleContextAssembled),
		"compactor:" + string(agentsdk.ConversationLifecycleContextCompacting),
		"observer:" + string(agentsdk.ConversationLifecycleModelRequest),
		"observer:" + string(agentsdk.ConversationLifecycleModelCompleted),
		"observer:" + string(agentsdk.ConversationLifecycleToolBefore),
		"observer:" + string(agentsdk.ConversationLifecycleToolCompleted),
		"observer:" + string(agentsdk.ConversationLifecycleRunFinished),
	} {
		if !containsString(events, required) {
			t.Errorf("missing lifecycle event %s in %v", required, events)
		}
	}
	if indexString(events, "planner:"+string(agentsdk.ConversationLifecycleContextAssembling)) > indexString(events, "observer:"+string(agentsdk.ConversationLifecycleContextAssembling)) {
		t.Fatalf("lifecycle order was not applied: %v", events)
	}
}

func TestConversationLifecycleRetryPolicyNarrowsEngineAndInputPolicyFailsClosed(t *testing.T) {
	t.Run("text context policy lowers frozen ceiling", func(t *testing.T) {
		repo := conversationRepository(t)
		var requestMu sync.Mutex
		var request agentsdk.ConversationModelRequest
		model := conversationModelFunc(func(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
			requestMu.Lock()
			request = in
			requestMu.Unlock()
			return agentsdk.ConversationModelResult{Content: "bounded", Model: "text-model"}, nil
		})
		policy := &lifecycleTestExtension{
			definition: lifecycleTestDefinition("text-compactor", 1, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleContextCompacting),
			handle: func(agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
				return agentsdk.ConversationLifecycleDecision{ContextLimitBytes: 5_000}, nil
			},
		}
		options := conversationOptions()
		options.ContextBytes = 8_000
		options.LifecycleExtensions = []agentsdk.ConversationLifecycleExtension{policy}
		service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authority := conversationAuthority()
		conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "text-lifecycle-limit"}, authority)
		if err != nil {
			t.Fatal(err)
		}
		run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "bounded", Message: "answer"}, authority)
		if err != nil {
			t.Fatal(err)
		}
		if final := waitConversation(t, service, conversation.ID, run.ID); final.Status != "completed" {
			t.Fatalf("text lifecycle run failed: %+v", final)
		}
		requestMu.Lock()
		window := request.ContextWindow
		requestMu.Unlock()
		if window == nil || window.LimitBytes != 5_000 {
			t.Fatalf("text context ceiling was not frozen: %+v", window)
		}
	})

	t.Run("retry policy disables retry", func(t *testing.T) {
		repo := conversationRepository(t)
		host := &executionHost{allowed: true}
		model := &retryingExecutionModel{}
		policy := &lifecycleTestExtension{
			definition: lifecycleTestDefinition("retry-policy", 1, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleModelFailed),
			handle: func(agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
				return agentsdk.ConversationLifecycleDecision{Retry: &agentsdk.ConversationLifecycleRetryDecision{Retry: false}}, nil
			},
		}
		service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, application.ConversationOptions{ToolHost: host, MaxModelAttempts: 3, ModelRetryBaseDelay: time.Millisecond, ModelRetryMaxDelay: 200 * time.Millisecond, LifecycleExtensions: []agentsdk.ConversationLifecycleExtension{policy}})
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authority := conversationAuthority()
		conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "lifecycle-retry"}, authority)
		if err != nil {
			t.Fatal(err)
		}
		run, err := service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "retry-once", Message: "create once"}, authority)
		if err != nil {
			t.Fatal(err)
		}
		final := waitConversation(t, service, conversation.ID, run.ID)
		model.mu.Lock()
		calls := len(model.calls)
		model.mu.Unlock()
		if final.ErrorCode != "provider_network" || calls != 1 || len(final.ModelAttempts) != 1 || final.ModelAttempts[0].Status != "failed" {
			t.Fatalf("retry policy did not narrow the engine policy: run=%+v calls=%d", final, calls)
		}
	})

	t.Run("input policy denies before enqueue", func(t *testing.T) {
		repo := conversationRepository(t)
		calls := 0
		model := conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
			calls++
			return agentsdk.ConversationModelResult{Content: "unexpected"}, nil
		})
		policy := &lifecycleTestExtension{
			definition: lifecycleTestDefinition("input-policy", 1, agentsdk.ConversationLifecycleKindPolicy, agentsdk.ConversationLifecycleFailureFailClosed, agentsdk.ConversationLifecycleInputAdmitting),
			handle: func(agentsdk.ConversationLifecycleEvent) (agentsdk.ConversationLifecycleDecision, error) {
				return agentsdk.ConversationLifecycleDecision{}, errors.New("private policy reason")
			},
		}
		options := conversationOptions()
		options.LifecycleExtensions = []agentsdk.ConversationLifecycleExtension{policy}
		service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
		if err != nil {
			t.Fatal(err)
		}
		defer service.Close()
		authority := conversationAuthority()
		conversation, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "lifecycle-deny"}, authority)
		if err != nil {
			t.Fatal(err)
		}
		_, err = service.Send(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "denied", Message: "blocked input"}, authority)
		if err == nil || !strings.Contains(err.Error(), "lifecycle_extension_failed") {
			t.Fatalf("expected stable lifecycle failure, got %v", err)
		}
		messages, readErr := service.Messages(t.Context(), conversation.ID, agentsdk.ConversationMessageQuery{}, authority)
		if readErr != nil || len(messages.Items) != 0 || calls != 0 {
			t.Fatalf("denied input was persisted or executed: messages=%+v calls=%d err=%v", messages, calls, readErr)
		}
	})
}

func containsString(items []string, expected string) bool { return indexString(items, expected) >= 0 }

func indexString(items []string, expected string) int {
	for index, item := range items {
		if item == expected {
			return index
		}
	}
	return -1
}
