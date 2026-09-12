package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	agentinfra "github.com/domainry/domainry-agent/internal/infrastructure/persistence"
	agentstore "github.com/domainry/domainry-agent/internal/infrastructure/persistence/database/agent"
	agentmodule "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-foundation/modulehttp"
)

type deferredModuleHost struct{ *sqliteModuleHost }

func (deferredModuleHost) DeferConversationHostBinding() bool { return true }

// No Interactive, Task, Proposal, Audit or Analysis ports are embedded here.
type thinConversationHost struct {
	authorizer agentsdk.ConversationToolAuthorizer
}

func (h thinConversationHost) ConversationAuthorizer() agentsdk.ConversationToolAuthorizer {
	return h.authorizer
}
func (thinConversationHost) ConversationBusinessSource() agentsdk.ConversationBusinessSource {
	return nil
}

type conversationApplicationHost struct {
	applicationHost
	source *businessSourceFixture
}

func (conversationApplicationHost) ConversationAuthorizer() agentsdk.ConversationToolAuthorizer {
	return personalReadAuthorizer{}
}
func (h conversationApplicationHost) ConversationBusinessSource() agentsdk.ConversationBusinessSource {
	return h.source
}

func TestDeferredConversationModuleRecoversOnlyAfterBusinessHostBinding(t *testing.T) {
	a := conversationAuthority()
	host := newSQLiteModuleHost(t, a.RuntimeID)
	initial, err := agentmodule.NewFactory(agentmodule.Options{}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, host)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	store, err := agentinfra.NewAgentStore(host.database, host.dialect, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	repo := agentstore.NewConversationStore(store)
	conversation, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "pending-business", Title: "恢复业务查询"}, a)
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.Enqueue(t.Context(), conversation.ID, agentsdk.ConversationSend{ClientMessageID: "pending-business-read", Message: "找客户乙的资料"}, a)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
		calls.Add(1)
		if n == 1 {
			found := false
			for _, tool := range in.Tools {
				if tool.Key == "calculate" {
					t.Error("deferred application binding ignored live tool switch")
				}
				if tool.Key == "business_catalog" {
					found = true
				}
			}
			if !found {
				t.Error("recovered execution started without its business source")
			}
			return resultToolCall("business_catalog", "catalog", map[string]any{}), nil
		}
		if n == 2 {
			return resultToolCall("get_record", "read", map[string]any{"object_key": "customer", "record_id": "customer-2", "fields": []string{"name"}}), nil
		}
		last := in.Messages[len(in.Messages)-1].Content
		if !strings.Contains(last, "客户乙") {
			t.Errorf("business read was not available: %s", last)
		}
		return (&executionModel{}).answerResult(), nil
	}}
	options := agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Workers: 1, Poll: 10 * time.Millisecond}}
	opened, err := agentmodule.NewFactory(options).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, deferredModuleHost{host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(t.Context()) })
	binding := opened.(agentsdk.ConversationBinding)
	if binding.Conversations() != nil {
		t.Fatal("unbound conversations exposed")
	}
	// More than several worker polling intervals: a persisted queued run must
	// remain untouched until the later application binding is complete.
	time.Sleep(70 * time.Millisecond)
	stored, err := repo.Run(t.Context(), conversation.ID, run.ID, a)
	if err != nil || stored.Status != "queued" || stored.Attempt != 0 || calls.Load() != 0 {
		t.Fatalf("unbound run processed: %+v, %v", stored, err)
	}
	ports := applicationHost{task: newTaskHost(opened.(agentpersistence.ExecutionStateBinding).AgentTaskState()), ports: &unusedApplicationPorts{}}
	binder := opened.(modulehost.ApplicationHostBinder)
	if err := binder.BindApplicationHost(ports); err == nil {
		t.Fatal("missing live conversation authorization accepted")
	}
	source := &businessSourceFixture{}
	if err := binder.BindApplicationHost(availabilityApplicationHost{conversationApplicationHost{applicationHost: ports, source: source}}); err != nil {
		t.Fatal(err)
	}
	service := binding.Conversations()
	if service == nil {
		t.Fatal("bound conversation service missing")
	}
	completed := waitConversation(t, service, conversation.ID, run.ID)
	if completed.Status != "completed" || calls.Load() != 3 {
		t.Fatalf("recovery: %+v model calls=%d", completed, calls.Load())
	}
	if err := binder.BindApplicationHost(conversationApplicationHost{applicationHost: ports, source: source}); err != nil {
		t.Fatal(err)
	}
	if binding.Conversations() != service {
		t.Fatal("repeat startup binding replaced a live conversation service")
	}
	source.revoked.Store(true)
	withheld, err := service.Run(t.Context(), conversation.ID, run.ID, a)
	if err != nil || withheld.AccessError == "" || withheld.DraftText != "" {
		t.Fatalf("source revocation was not rechecked: %+v %v", withheld, err)
	}
}

func TestDeferredConversationModuleWithoutModelKeepsPersonalHTTPServices(t *testing.T) {
	a := conversationAuthority()
	host := deferredModuleHost{newSQLiteModuleHost(t, a.RuntimeID)}
	opened, err := agentmodule.NewFactory(agentmodule.Options{}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(t.Context()) })
	if err := opened.(modulehost.ConversationApplicationHostBinder).BindConversationHost(thinConversationHost{personalReadAuthorizer{}}); err != nil {
		t.Fatal(err)
	}
	service := opened.(agentsdk.ConversationBinding).Conversations()
	if service == nil {
		t.Fatal("model-less service missing")
	}
	if _, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "without-model"}, a); err != nil {
		t.Fatal(err)
	}
	if err := service.(agentsdk.ConversationStatusProvider).ConversationReady(t.Context()); err == nil {
		t.Fatal("missing model declared ready")
	}
}

func TestConversationOnlyBindingRecoversWithAuthorizationAndSharedBudget(t *testing.T) {
	for _, exceed := range []bool{false, true} {
		t.Run(fmt.Sprintf("exceed=%v", exceed), func(t *testing.T) {
			a := conversationAuthority()
			database := newSQLiteModuleHost(t, a.RuntimeID)
			ports := thinConversationHost{personalReadAuthorizer{}}
			if _, legacy := any(ports).(modulehost.ApplicationHost); legacy {
				t.Fatal("fixture unexpectedly requires legacy application ports")
			}
			toolHost := &executionHost{allowed: true}
			var calls atomic.Int32
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				calls.Add(1)
				if n == 2 {
					return agentsdk.ConversationStepResult{}, fmt.Errorf("interrupted after first effect")
				}
				if n == 4 && !exceed {
					return (&executionModel{}).answerResult(), nil
				}
				return resultToolCall("create_item", fmt.Sprintf("call-%d", n), map[string]any{"title": fmt.Sprintf("item-%d", n)}), nil
			}}
			options := agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{ToolHost: toolHost, MaxToolCalls: 2, Poll: 5 * time.Millisecond}}
			open := func() agentsdk.Binding {
				b, err := agentmodule.NewFactory(options).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, deferredModuleHost{database})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			opened := open()
			defer func() { _ = opened.Close(t.Context()) }()
			store, err := agentinfra.NewAgentStore(database.database, database.dialect, "sqlite")
			if err != nil {
				t.Fatal(err)
			}
			repo := agentstore.NewConversationStore(store)
			c, err := repo.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "thin-host"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := repo.Enqueue(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "one", Message: "创建事项"}, a)
			if err != nil {
				t.Fatal(err)
			}
			if opened.(agentsdk.ConversationBinding).Conversations() != nil {
				t.Fatal("unbound service published")
			}
			beforeAdapters := len(opened.(modulehttp.Provider).HTTPAdapters())
			binder := opened.(modulehost.ConversationApplicationHostBinder)
			for _, invalid := range []modulehost.ConversationApplicationHost{nil, thinConversationHost{}} {
				if err = binder.BindConversationHost(invalid); err == nil {
					t.Fatal("missing authorizer accepted")
				}
			}
			time.Sleep(70 * time.Millisecond)
			pending, err := repo.Run(t.Context(), c.ID, run.ID, a)
			if err != nil || pending.Status != "queued" || pending.Attempt != 0 || calls.Load() != 0 {
				t.Fatalf("unbound recovery: %+v %v", pending, err)
			}
			if err = binder.BindConversationHost(ports); err != nil {
				t.Fatal(err)
			}
			service := opened.(agentsdk.ConversationBinding).Conversations()
			if got := len(opened.(modulehttp.Provider).HTTPAdapters()); got != beforeAdapters+1 {
				t.Fatalf("adapters: %d", got)
			}
			if err = binder.BindConversationHost(ports); err == nil {
				t.Fatal("live conversation host replaced")
			}
			if service != opened.(agentsdk.ConversationBinding).Conversations() {
				t.Fatal("service changed on rejected bind")
			}
			first := waitConversation(t, service, c.ID, run.ID)
			if first.Status != "failed" || first.ErrorCode != "provider_failed" || calls.Load() != 2 {
				t.Fatalf("first run: %+v model=%d", first, calls.Load())
			}
			if err = opened.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err = binder.BindConversationHost(ports); err == nil {
				t.Fatal("closed binding reopened")
			}
			toolHost.mu.Lock()
			toolHost.allowed = false
			toolHost.mu.Unlock()
			opened = open()
			if err = opened.(modulehost.ConversationApplicationHostBinder).BindConversationHost(ports); err != nil {
				t.Fatal(err)
			}
			service = opened.(agentsdk.ConversationBinding).Conversations()
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			denied := waitConversation(t, service, c.ID, run.ID)
			if denied.ErrorCode != "tool_access_denied" || calls.Load() != 2 {
				t.Fatalf("revoked replay reached model: %+v model=%d", denied, calls.Load())
			}
			toolHost.mu.Lock()
			toolHost.allowed = true
			toolHost.mu.Unlock()
			if _, err = service.Resume(t.Context(), c.ID, run.ID, a); err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			if exceed {
				if final.ErrorCode != "execution_limit" || final.Status != "failed" {
					t.Fatalf("budget reset on recovery: %+v", final)
				}
			} else if final.Status != "completed" {
				t.Fatalf("completed result charged twice: %+v", final)
			}
			toolHost.mu.Lock()
			invokes, reconciles := toolHost.invokes, toolHost.reconciles
			toolHost.mu.Unlock()
			if invokes != 2 || reconciles != 0 || calls.Load() != 4 {
				t.Fatalf("effects=%d reconciles=%d model=%d", invokes, reconciles, calls.Load())
			}
			for _, table := range []string{"_agent_task_runs", "_agent_task_definitions", "_agent_interactive_runs"} {
				var count int
				if err = database.database.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("legacy table %s count=%d err=%v", table, count, err)
				}
			}
			t.Logf("thin host: run=%s attempt=%d status=%s error=%s effects=%d model=%d legacy rows=0", final.ID, final.Attempt, final.Status, final.ErrorCode, invokes, calls.Load())
		})
	}
}

func TestConversationOnlyBindingRequiresDeferredStartup(t *testing.T) {
	a := conversationAuthority()
	opened, err := agentmodule.NewFactory(agentmodule.Options{}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, newSQLiteModuleHost(t, a.RuntimeID))
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close(t.Context())
	if err := opened.(modulehost.ConversationApplicationHostBinder).BindConversationHost(thinConversationHost{personalReadAuthorizer{}}); err == nil {
		t.Fatal("non-deferred service was replaced")
	}
}

// Early persistence ports are deliberately unusable. Deferred assembly must
// resolve automatic defaults from the later application host instead.
type earlyConversationHost struct {
	deferredModuleHost
	checks atomic.Int32
}

func (h *earlyConversationHost) AuthorizeConversationTool(context.Context, agentsdk.ConversationToolRequest) (agentsdk.ConversationToolAuthorization, error) {
	h.checks.Add(1)
	return agentsdk.ConversationToolAuthorization{}, nil
}
func (h *earlyConversationHost) ConversationToolAvailable(context.Context, agentsdk.ConversationAuthority, string) (bool, error) {
	h.checks.Add(1)
	return false, nil
}

func TestConversationOnlyBindingResolvesLiveDefaultsAndPreservesExplicitOptions(t *testing.T) {
	for _, explicit := range []string{"none", "authorizer", "availability"} {
		t.Run(explicit, func(t *testing.T) {
			a := conversationAuthority()
			early := &earlyConversationHost{deferredModuleHost: deferredModuleHost{newSQLiteModuleHost(t, a.RuntimeID)}}
			model := &executionModel{step: func(n int, in agentsdk.ConversationStepRequest) (agentsdk.ConversationStepResult, error) {
				found := false
				for _, tool := range in.Tools {
					if tool.Key == "calculate" {
						found = true
					}
				}
				if found != (explicit == "none") {
					t.Errorf("live/explicit policy ignored: explicit=%s calculate=%v", explicit, found)
				}
				if n == 1 && found {
					return resultToolCall("calculate", "sum", map[string]any{"operation": "expression", "expression": "0.1+0.2", "unit": "CNY"}), nil
				}
				if found && !strings.Contains(in.Messages[len(in.Messages)-1].Content, "0.30") {
					t.Error("calculation result absent")
				}
				return (&executionModel{}).answerResult(), nil
			}}
			options := agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}
			switch explicit {
			case "authorizer":
				options.ConversationOptions.PersonalAuthorizer = early
			case "availability":
				options.ConversationOptions.ToolAvailability = early
			}
			opened, err := agentmodule.NewFactory(options).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: a.RuntimeID}, early)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close(t.Context())
			if err = opened.(modulehost.ConversationApplicationHostBinder).BindConversationHost(thinConversationHost{personalReadAuthorizer{}}); err != nil {
				t.Fatal(err)
			}
			service := opened.(agentsdk.ConversationBinding).Conversations()
			c, err := service.Create(t.Context(), agentsdk.ConversationCreate{ClientID: "defaults"}, a)
			if err != nil {
				t.Fatal(err)
			}
			run, err := service.Send(t.Context(), c.ID, agentsdk.ConversationSend{ClientMessageID: "sum", Message: "计算"}, a)
			if err != nil {
				t.Fatal(err)
			}
			final := waitConversation(t, service, c.ID, run.ID)
			if final.Status != "completed" {
				t.Fatalf("run=%+v", final)
			}
			if (early.checks.Load() == 0) != (explicit == "none") {
				t.Fatalf("early host policy checks=%d explicit=%s", early.checks.Load(), explicit)
			}
		})
	}
}
