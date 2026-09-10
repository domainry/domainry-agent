package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
)

func TestHistoricalExecutionThroughIdentityHTTPAndSSE(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				CallID  string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) == 0 {
			t.Error("invalid model input")
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		tool := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		last := payload.Messages[len(payload.Messages)-1]
		switch {
		case last.Role == "user" && last.Content == "计算费用":
			tool("calculate", "decimal", map[string]any{"operation": "expression", "expression": "0.1+0.2", "unit": "CNY"})
		case last.Role == "user":
			found := false
			for _, message := range payload.Messages {
				if !strings.HasPrefix(message.Content, "Recent execution links") {
					continue
				}
				var links []agentsdk.ConversationExecutionRead
				if json.Unmarshal([]byte(strings.Split(message.Content, "\n")[1]), &links) != nil || len(links) == 0 {
					t.Error("missing automatic link")
					return
				}
				tool("execution_read", "old-execution", links[0])
				found = true
				break
			}
			if !found {
				t.Error("execution locator absent")
			}
		case last.CallID == "old-execution":
			var output agentsdk.ConversationToolResult
			var page agentsdk.ConversationExecutionReadResult
			if json.Unmarshal([]byte(last.Content), &output) != nil || json.Unmarshal(output.Content, &page) != nil || !page.Complete || len(page.Items) != 1 || page.Items[0].Reference == nil {
				t.Error("actual historical result missing")
				return
			}
			tool("tool_result_read", "original-result", agentsdk.ConversationResultRead{Reference: *page.Items[0].Reference})
		case last.CallID == "original-result":
			var output agentsdk.ConversationToolResult
			var page agentsdk.ConversationResultSlice
			if json.Unmarshal([]byte(last.Content), &output) != nil || json.Unmarshal(output.Content, &page) != nil || !page.Complete || !strings.Contains(page.JSONText, `"value":"0.30"`) {
				t.Error("full calculation evidence missing")
				return
			}
			write(map[string]any{"content": "原始计算结果为 0.30 元。"}, "")
			write(map[string]any{}, "stop")
		default:
			write(map[string]any{"content": "费用计算已保存。"}, "")
			write(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "history-results.db"), RuntimeID: "history-runtime", WorkspaceID: "history-workspace", ApplicationKey: "history-app", Agent: agentmodule.Options{ConversationURL: upstream.URL, ConversationModel: "history-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "history-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"history"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	send := func(id, message string) agentsdk.ConversationRun {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"client_message_id": id, "message": message})
		var run agentsdk.ConversationRun
		_ = json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &run)
		deadline := time.Now().Add(10 * time.Second)
		for !run.Terminal() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
			_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
		}
		if run.Status != "completed" {
			t.Fatalf("%s failed: %s/%s", id, run.Status, run.ErrorCode)
		}
		return run
	}
	first := send("first", "计算费用")
	second := send("second", "读取上次实际计算结果")
	if len(second.Steps) != 3 || second.Steps[0].Calls[0].Name != "execution_read" {
		t.Fatal("historical lookup skipped real executor")
	}
	var old agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("GET", base+"/runs/"+first.ID, "", 200).Body.Bytes(), &old)
	if old.ID != first.ID || old.Steps[0].Calls[0].Name != "calculate" || old.Steps[0].Calls[0].ResultReference == nil {
		t.Fatal("old-run route lost original execution")
	}
	sse := b.call("GET", base+"/runs/"+first.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	if !strings.Contains(sse, `"result_reference"`) || !strings.Contains(sse, old.Steps[0].Calls[0].ResultReference.SHA256) {
		t.Fatal("SSE did not preserve result reference")
	}
	for _, forbidden := range []string{"provider_state", "authorization_revision", "record_hash", "fingerprint"} {
		if strings.Contains(sse, forbidden) {
			t.Fatalf("private state exposed: %s", forbidden)
		}
	}
}
