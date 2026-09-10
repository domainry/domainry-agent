package integration_test

import (
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
)

type deferredModuleHost struct{ *sqliteModuleHost }

func (deferredModuleHost) DeferConversationHostBinding() bool { return true }

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
	ports := applicationHost{task: newTaskHost(opened.(agentpersistence.ExecutionStateBinding).AgentTaskState()), ports: &unusedApplicationPorts{}}
	if err := opened.(modulehost.ApplicationHostBinder).BindApplicationHost(conversationApplicationHost{applicationHost: ports, source: &businessSourceFixture{}}); err != nil {
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
