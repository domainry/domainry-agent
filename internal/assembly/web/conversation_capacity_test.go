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

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

type capacityWebModel struct {
	once    sync.Once
	entered chan struct{}
}

func (m *capacityWebModel) GenerateConversation(ctx context.Context, in agentsdk.ConversationModelRequest) (agentsdk.ConversationModelResult, error) {
	last := in.Messages[len(in.Messages)-1].Content
	if strings.Contains(last, "slow") {
		m.once.Do(func() { close(m.entered) })
		<-ctx.Done()
		return agentsdk.ConversationModelResult{}, ctx.Err()
	}
	return agentsdk.ConversationModelResult{Content: "capacity accepted", Model: "capacity-web-fixture"}, nil
}

func TestConversationCapacityThroughIdentityHTTPAndSQLiteRestart(t *testing.T) {
	const initial, changed = "Initial-Capacity-Test!2", "Changed-Capacity-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "capacity-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "capacity-test-encryption-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &capacityWebModel{entered: make(chan struct{})}
	options := Options{
		DatabasePath: filepath.Join(t.TempDir(), "capacity.db"), RuntimeID: "capacity-runtime", WorkspaceID: "capacity-workspace", ApplicationKey: "capacity-app",
		Agent: agentmodule.Options{ConversationProvider: model, ConversationOptions: agentmodule.ConversationOptions{
			Workers: 1, Poll: 5 * time.Millisecond, RunTimeout: 3 * time.Second, ExternalCallTimeout: 500 * time.Millisecond,
			MaxQueuedPerUser: 1, MaxQueuedPerWorkspace: 1, MaxRunningPerUser: 1, MaxRunningPerWorkspace: 1,
		}},
	}
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "capacity-web-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { _ = host.Close(context.Background()) }()
	browser := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	browser.login("admin@example.com", initial)
	browser.changePassword(initial, changed)
	create := func(id string) agentsdk.Conversation {
		var conversation agentsdk.Conversation
		if err := json.Unmarshal(browser.call("POST", "/agent/conversations", `{"client_id":"`+id+`"}`, 200).Body.Bytes(), &conversation); err != nil {
			t.Fatal(err)
		}
		return conversation
	}
	firstConversation, secondConversation, rejectedConversation := create("capacity-first"), create("capacity-second"), create("capacity-rejected")
	send := func(conversation agentsdk.Conversation, id, message string, status int) agentsdk.ConversationRun {
		var run agentsdk.ConversationRun
		response := browser.call("POST", "/agent/conversations/"+conversation.ID+"/messages", `{"client_message_id":"`+id+`","message":"`+message+`"}`, status)
		if status == 202 && json.Unmarshal(response.Body.Bytes(), &run) != nil {
			t.Fatal("invalid run response")
		}
		return run
	}
	first := send(firstConversation, "slow", "slow provider", 202)
	select {
	case <-model.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow provider did not start")
	}
	second := send(secondConversation, "queued", "fast provider", 202)
	rejected := browser.call("POST", "/agent/conversations/"+rejectedConversation.ID+"/messages", `{"client_message_id":"rejected","message":"queue overflow"}`, 429)
	if rejected.Body.String() != "{\"code\":\"agent.conversation.user_queue_full\"}\n" {
		t.Fatalf("quota response=%s", rejected.Body.String())
	}
	wait := func(conversation agentsdk.Conversation, run agentsdk.ConversationRun) agentsdk.ConversationRun {
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if err := json.Unmarshal(browser.call("GET", "/agent/conversations/"+conversation.ID+"/runs/"+run.ID, "", 200).Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if run.Terminal() {
				return run
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run did not finish")
		return run
	}
	if first = wait(firstConversation, first); first.Status != "failed" || first.ErrorCode != "provider_timeout" {
		t.Fatalf("first=%+v", first)
	}
	if second = wait(secondConversation, second); second.Status != "completed" {
		t.Fatalf("second=%+v", second)
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	open()
	browser.handler = handler
	browser.login("admin@example.com", changed)
	afterRestart := send(rejectedConversation, "after-restart", "fast after restart", 202)
	if afterRestart = wait(rejectedConversation, afterRestart); afterRestart.Status != "completed" {
		t.Fatalf("after restart=%+v", afterRestart)
	}
}
