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

// Real Identity, memory effects, result storage and compaction are reached
// through product HTTP routes. Only the three model decisions are fixtures.
func TestCompactedResultReadThroughIdentityHTTP(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload.Messages) == 0 {
			t.Error("invalid model request")
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
		case last.Role != "tool":
			tool("memory_search", "memory-large", map[string]any{"include_disabled": true})
		case last.ToolCallID == "memory-large":
			var preview struct {
				Representation  string                               `json:"representation"`
				Reference       agentsdk.ConversationResultReference `json:"reference"`
				TotalBytes      int                                  `json:"total_bytes"`
				ContentComplete bool                                 `json:"content_complete"`
			}
			if json.Unmarshal([]byte(last.Content), &preview) != nil || preview.Representation != "stored_result_preview" || preview.ContentComplete || preview.TotalBytes < 8192 {
				t.Error("large memory result was not compacted")
				return
			}
			tool("tool_result_read", "read-tail", agentsdk.ConversationResultRead{Reference: preview.Reference, Offset: preview.TotalBytes - 2048, MaxBytes: 2048})
		default:
			var result agentsdk.ConversationToolResult
			var slice agentsdk.ConversationResultSlice
			if json.Unmarshal([]byte(last.Content), &result) != nil || json.Unmarshal(result.Content, &slice) != nil || result.Status != "completed" || !slice.Complete || !strings.Contains(slice.JSONText, "memory-tail-") {
				t.Errorf("actual result slice missing: %s", last.Content)
			}
			write(map[string]any{"content": "已核对本次检索结果尾部。未读取的其他分页仍需继续查询。"}, "")
			write(map[string]any{}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "results.db"), RuntimeID: "result-runtime", WorkspaceID: "result-workspace", ApplicationKey: "result-app", Agent: agentmodule.Options{ConversationURL: upstream.URL, ConversationModel: "result-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "result-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	for i := 0; i < 16; i++ {
		raw, _ := json.Marshal(agentsdk.ConversationMemoryWrite{Title: fmt.Sprintf("Preference %02d", i), Content: strings.Repeat("\n", 480) + fmt.Sprintf("memory-tail-%02d", i), Enabled: true})
		b.call("PUT", fmt.Sprintf("/agent/conversations/memories/result-memory-%02d", i), string(raw), 200)
	}
	var c agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"results"}`, 200).Body.Bytes(), &c)
	base := "/agent/conversations/" + c.ID
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"read","message":"查看个人记忆检索结果，按需核对原始内容。"}`, 202).Body.Bytes(), &run)
	deadline := time.Now().Add(10 * time.Second)
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
	}
	if run.Status != "completed" || len(run.Steps) != 3 || run.Steps[1].Calls[0].Name != "tool_result_read" || run.Steps[1].Calls[0].Status != "completed" {
		t.Fatalf("result flow failed: %+v", run)
	}
	sse := b.call("GET", base+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	for _, kind := range []string{"context.tools_compacted", "tool.completed", "run.completed"} {
		if !strings.Contains(sse, kind) {
			t.Errorf("SSE missing %s", kind)
		}
	}
	if strings.Contains(sse, "provider_state") || strings.Contains(sse, "authorization_revision") {
		t.Fatal("private execution state reached SSE")
	}
}
