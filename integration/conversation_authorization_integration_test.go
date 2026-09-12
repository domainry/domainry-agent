package integration_test

import (
	"context"
	"errors"
	"fmt"
	conversationassembly "github.com/domainry/domainry-agent/internal/assembly/conversation"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/application"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type conversationExecutionPolicyFunc func(context.Context, agentsdk.ConversationExecutionAuthorizationRequest) (bool, error)

func (f conversationExecutionPolicyFunc) AuthorizeConversationExecution(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	return f(ctx, in)
}

// Keep only the old tool interface to exercise an incomplete external host.
type toolOnlyAdmissionPolicy struct {
	agentsdk.ConversationToolAuthorizer
}

func TestConversationApplicationBindingRequiresExecutionPolicy(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			a := conversationAuthority()
			options := agentmodule.Options{ConversationProvider: conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				t.Error("denied host invoked model")
				return agentsdk.ConversationModelResult{}, nil
			})}
			if explicit {
				options.ConversationOptions.ExecutionAuthorizer = conversationExecutionPolicyFunc(func(context.Context, agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
					return false, nil
				})
			}
			opened, err := agentmodule.NewFactory(options).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, deferredModuleHost{newSQLiteModuleHost(t, a.RuntimeID)})
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close(t.Context())
			bind := opened.(modulehost.ConversationApplicationHostBinder)
			err = bind.BindConversationHost(thinConversationHost{toolOnlyAdmissionPolicy{personalReadAuthorizer{}}})
			if !explicit {
				if err == nil {
					t.Fatal("application host started workers without current execution policy")
				}
				if err = bind.BindConversationHost(thinConversationHost{personalReadAuthorizer{}}); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			service := opened.(agentsdk.ConversationBinding).Conversations()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "explicit-policy"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "blocked"}, a); err == nil {
				t.Fatal("explicit execution authorizer was replaced")
			}
		})
	}
}

func TestConversationImmediateModuleBindingRequiresExecutionPolicy(t *testing.T) {
	a := conversationAuthority()
	// Hiding optional methods emulates an external host with only persistence.
	host := struct{ modulehost.Host }{newSQLiteModuleHost(t, a.RuntimeID)}
	options := agentmodule.Options{ConversationProvider: conversationModelFunc(func(context.Context, agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		t.Error("model invoked without current execution policy")
		return agentsdk.ConversationModelResult{}, nil
	})}
	if binding, err := agentmodule.NewFactory(options).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, host); err == nil {
		_ = binding.Close(t.Context())
		t.Fatal("immediate model host started without current execution policy")
	}
}

func TestConversationExecutionAuthorizationAtEveryModelBoundary(t *testing.T) {
	for _, stage := range []string{"send", "execute", "model", "summary", "commit", "unavailable"} {
		t.Run(stage, func(t *testing.T) {
			repo := conversationRepository(t)
			var models, summaries, checked atomic.Int32
			options := conversationOptions()
			options.ExecutionAuthorizer = conversationExecutionPolicyFunc(func(ctx context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
				if in.Authority != conversationAuthority() || in.ConversationID == "" || in.Stage != "send" && in.RunID == "" {
					t.Error("execution policy received untrusted or incomplete identity")
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
					t.Error("authorization is not bounded")
				}
				if stage == "unavailable" && in.Stage == "execute" {
					return true, errors.New("private-identity-secret must not be persisted")
				}
				if stage == in.Stage {
					checked.Add(1)
					return false, nil
				}
				return true, nil
			})
			model := conversationModelFunc(func(_ context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
				models.Add(1)
				if in.Purpose == "summary" {
					summaries.Add(1)
					t.Error("revoked summary reached the model")
				}
				return agentsdk.ConversationModelResult{Content: strings.Repeat("r", 330), Model: "authorization-fixture"}, nil
			})
			service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: stage}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for i := 0; i < 14; i++ {
				run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: fmt.Sprint("send", i), Message: strings.Repeat("u", 330)}, conversationAuthority())
				if stage == "send" {
					if err == nil {
						t.Fatal("denied send was enqueued")
					}
					page, readErr := repo.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, conversationAuthority())
					if readErr != nil || len(page.Items) != 0 {
						t.Fatal("denied send stored messages", readErr)
					}
					failed = true
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				final := waitConversation(t, service, c.ID, run.ID)
				if final.Status == "failed" {
					code := "execution_access_denied"
					if stage == "unavailable" {
						code = "execution_authorization_unavailable"
					}
					if final.ErrorCode != code || strings.Contains(string(mustJSON(final)), "private-identity-secret") {
						t.Fatalf("incorrect denial: %+v", final)
					}
					page, readErr := repo.Messages(t.Context(), c.ID, agentsdk.ConversationMessageQuery{}, conversationAuthority())
					if readErr != nil || len(page.Items) != i*2+1 {
						t.Fatal("denied execution committed an assistant message", readErr)
					}
					failed = true
					break
				}
				if stage != "summary" {
					t.Fatal("revoked boundary did not stop execution")
				}
			}
			if !failed || summaries.Load() != 0 || stage != "unavailable" && checked.Load() == 0 {
				t.Fatal("boundary not exercised")
			}
			if stage != "summary" && stage != "commit" && models.Load() != 0 {
				t.Fatal("unauthorized model call")
			}
		})
	}
}

func TestConversationExecutionAuthorizationRechecksRecoveryAndResume(t *testing.T) {
	repo := conversationRepository(t)
	var denied atomic.Bool
	var currentRoleRequired atomic.Bool
	var calls atomic.Int32
	started := make(chan struct{})
	model := conversationModelFunc(func(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return agentsdk.ConversationModelResult{}, ctx.Err()
		}
		return agentsdk.ConversationModelResult{Content: "explicitly resumed", Model: "authorization-fixture"}, nil
	})
	options := conversationOptions()
	options.ExecutionAuthorizer = conversationExecutionPolicyFunc(func(_ context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
		return !denied.Load() && (!currentRoleRequired.Load() || in.Authority.RoleKey == "current-role"), nil
	})
	a := conversationAuthority()
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { service.Close() }()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "recovery-policy"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "first", Message: "persisted input"}, a)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model not started")
	}
	service.Close()
	denied.Store(true)
	service, err = conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	final := waitConversation(t, service, c.ID, run.ID)
	if final.Status != "failed" || final.ErrorCode != "execution_access_denied" || calls.Load() != 1 {
		t.Fatalf("recovery reused old grant: %+v", final)
	}
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err == nil {
		t.Fatal("resume admitted revoked owner")
	}
	after, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || after.Attempt != final.Attempt || after.LastEventSeq != final.LastEventSeq {
		t.Fatal("denied resume mutated run", err)
	}
	denied.Store(false)
	currentRoleRequired.Store(true)
	a.RoleKey = "current-role"
	if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
		t.Fatal(err)
	}
	final = waitConversation(t, service, c.ID, run.ID)
	if final.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("authorized recovery failed: %+v", final)
	}
}

func TestConversationConfirmationPersistsCurrentRoleForWorker(t *testing.T) {
	repo := conversationRepository(t)
	host := &interactionHost{executionHost: executionHost{allowed: true}, confirm: true, respondAllowed: true}
	model := &executionModel{}
	model.step = func(n int, _ agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		if n == 1 {
			return model.callResult(`{"title":"role changed"}`), nil
		}
		return model.answerResult(), nil
	}
	var currentRoleRequired atomic.Bool
	options := application.ConversationOptions{ToolHost: host, Workers: 1, Poll: 5 * time.Millisecond}
	options.ExecutionAuthorizer = conversationExecutionPolicyFunc(func(_ context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
		return !currentRoleRequired.Load() || in.Authority.RoleKey == "current-role", nil
	})
	a := conversationAuthority()
	service, err := conversationassembly.NewService(repo, model, a.RuntimeID, options)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "role-switch"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "create"}, a)
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
	currentRoleRequired.Store(true)
	a.RoleKey = "current-role"
	response := agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: 1, Decision: "approve"}
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal(err)
	}
	if final := waitConversation(t, service, c.ID, run.ID); final.Status != "completed" {
		t.Fatalf("confirmation kept stale role: %+v", final)
	}
	before, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Respond(t.Context(), c.ID, run.ID, response, a); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Run(t.Context(), c.ID, run.ID, a)
	if err != nil || before.LastEventSeq != after.LastEventSeq {
		t.Fatal("duplicate confirmation changed current run", err)
	}
	host.mu.Lock()
	invokes := host.invokes
	host.mu.Unlock()
	if invokes != 1 {
		t.Fatal("confirmed effect repeated", invokes)
	}
}

func TestConversationExecutionAuthorizationRechecksAfterConfirmation(t *testing.T) {
	for _, stage := range []string{"respond", "execute", "tool", "model"} {
		t.Run(stage, func(t *testing.T) {
			repo := conversationRepository(t)
			host := &interactionHost{executionHost: executionHost{allowed: true}, confirm: true, respondAllowed: true}
			model := &executionModel{}
			model.step = func(n int, _ agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				if n == 1 {
					return model.callResult(`{"title":"authorized effect"}`), nil
				}
				return model.answerResult(), nil
			}
			var revoke atomic.Bool
			options := application.ConversationOptions{ToolHost: host, Workers: 1, Poll: 5 * time.Millisecond}
			options.ExecutionAuthorizer = conversationExecutionPolicyFunc(func(_ context.Context, in agentsdk.ConversationExecutionAuthorizationRequest) (bool, error) {
				return !(revoke.Load() && in.Stage == stage), nil
			})
			service, err := conversationassembly.NewService(repo, model, conversationAuthority().RuntimeID, options)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			c, _ := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: stage}, conversationAuthority())
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "send", Message: "create"}, conversationAuthority())
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitConversationState(t, service, c.ID, run.ID, "waiting_confirmation")
			revoke.Store(true)
			_, err = service.Respond(t.Context(), c.ID, run.ID, agentsdk.ConversationInteractionResponse{InteractionID: waiting.Interaction.ID, ClientID: "approve", ExpectedRevision: 1, Decision: "approve"}, conversationAuthority())
			if stage == "respond" {
				if err == nil {
					t.Fatal("revoked response accepted")
				}
				after, readErr := repo.Run(t.Context(), c.ID, run.ID, conversationAuthority())
				if readErr != nil || after.LastEventSeq != waiting.LastEventSeq || after.Interaction.Status != "pending" {
					t.Fatal("denied response mutated confirmation", readErr)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				final := waitConversation(t, service, c.ID, run.ID)
				if final.Status != "failed" || final.ErrorCode != "execution_access_denied" {
					t.Fatalf("old approval reused: %+v", final)
				}
			}
			host.mu.Lock()
			invokes := host.invokes
			host.mu.Unlock()
			wantEffects := 0
			if stage == "model" {
				wantEffects = 1
			}
			model.mu.Lock()
			calls := model.calls
			model.mu.Unlock()
			if invokes != wantEffects || calls != 1 {
				t.Fatalf("effects=%d want=%d models=%d", invokes, wantEffects, calls)
			}
		})
	}
}
