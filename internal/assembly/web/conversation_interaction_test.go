package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

func TestConversationQuestionThroughIdentityHTTPProviderAndSSE(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		available := false
		for _, tool := range payload.Tools {
			available = available || tool.Function.Name == "ask_user"
		}
		if !available {
			t.Error("question tool missing from granted catalog")
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"model": "question-fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		last := payload.Messages[len(payload.Messages)-1]
		if last.Role == "user" && len(payload.Messages) > 1 && payload.Messages[len(payload.Messages)-2].Role == "tool" {
			last = payload.Messages[len(payload.Messages)-2]
		}
		if last.Role != "tool" {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "question-1", "type": "function", "function": map[string]any{"name": "ask_user", "arguments": `{"question":"周报采用哪种格式？","choices":["简洁版","详细版"]}`}}}}, "")
			write(map[string]any{}, "tool_calls")
		} else {
			if last.ToolCallID != "question-1" {
				t.Error("answer not paired with original question")
			}
			var result struct {
				Content struct {
					Answer string `json:"answer"`
				} `json:"content"`
			}
			if err := json.Unmarshal([]byte(last.Content), &result); err != nil || result.Content.Answer == "" {
				t.Error("missing actual answer")
				return
			}
			write(map[string]any{"content": "已收到你的选择：" + result.Content.Answer + "。我会按此格式继续。"}, "")
			write(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "questions.db"), RuntimeID: "question-runtime", WorkspaceID: "question-workspace", ApplicationKey: "question-app", Agent: agentmodule.Options{ConversationURL: upstream.URL, ConversationModel: "question-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "question-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true, false)
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"question-http"}`, 200).Body.Bytes(), &conversation)
	base := "/agent/conversations/" + conversation.ID
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"one","message":"整理周报，缺少信息时请向我提问"}`, 202).Body.Bytes(), &run)
	path := base + "/runs/" + run.ID
	waitState := func(status string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			_ = json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &run)
			if run.Status == status {
				return
			}
			if run.Terminal() {
				t.Fatalf("expected %s: %+v", status, run)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("never reached %s", status)
	}
	waitState("waiting_user")
	if run.Interaction == nil || run.Interaction.Question != "周报采用哪种格式？" || requests.Load() != 1 {
		t.Fatal("question was not durably paused")
	}
	response := agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "answer", ExpectedRevision: run.Interaction.Revision, Decision: "answer", Answer: "详细版，按项目组织"}
	raw, _ := json.Marshal(response)
	denied := b.call("POST", path+"/respond", string(raw), 403).Body.String()
	if !strings.Contains(denied, "interaction_access_denied") {
		t.Fatal("missing independent response permission check", denied)
	}
	grantPersonalTools(t, host, b, true, true)
	b.call("POST", path+"/respond", string(raw), 200)
	waitState("completed")
	b.call("POST", path+"/respond", string(raw), 200)
	var messages agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &messages)
	if len(messages.Items) != 3 || messages.Items[1].Content != response.Answer || !strings.Contains(messages.Items[2].Content, response.Answer) || requests.Load() != 2 {
		t.Fatal("lost or duplicate answer")
	}
	sse := b.call("GET", path+"/events/stream?scope="+b.scope, "", 200).Body.String()
	for _, event := range []string{"run.waiting_user", "tool.waiting", "interaction.responded", "tool.completed", "run.completed"} {
		if !strings.Contains(sse, event) {
			t.Errorf("missing durable event %s", event)
		}
	}
	servePersonalToolAcceptance(t, host, options)
}
