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
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

// The model is a protocol fixture. Memory storage, permissions, confirmations,
// worker execution, events and the browser-facing API are the product code.
func personalMemoryModelFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Messages) == 0 {
			t.Error("invalid model request")
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		tool := func(name, id string, args any) {
			raw, _ := json.Marshal(args)
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "")
			write(map[string]any{}, "tool_calls")
		}
		last := payload.Messages[len(payload.Messages)-1]
		intent := ""
		for _, m := range payload.Messages {
			if m.Role == "user" && (strings.Contains(m.Content, "记住") || strings.Contains(m.Content, "修改") || strings.Contains(m.Content, "停用") || strings.Contains(m.Content, "忘记")) {
				intent = m.Content
			}
		}
		if last.Role != "tool" {
			tool("memory_search", "memory-find", map[string]any{"include_disabled": true})
		} else if last.ToolCallID == "memory-find" {
			var envelope struct {
				Content struct {
					Items []agentsdk.ConversationMemory `json:"items"`
				} `json:"content"`
			}
			if err := json.Unmarshal([]byte(last.Content), &envelope); err != nil {
				t.Error(err)
			}
			if strings.Contains(intent, "忘记") && len(envelope.Content.Items) > 0 {
				tool("memory_forget", "memory-change", map[string]any{"id": envelope.Content.Items[0].ID, "expected_revision": envelope.Content.Items[0].Revision})
			} else {
				args := map[string]any{"title": "周报格式", "content": "按项目组织，列出进展和风险", "enabled": !strings.Contains(intent, "停用"), "expected_revision": 0}
				if strings.Contains(intent, "修改") {
					args["content"] = "每个项目先写结论，再列进展和风险"
				}
				if len(envelope.Content.Items) > 0 {
					args["id"], args["expected_revision"] = envelope.Content.Items[0].ID, envelope.Content.Items[0].Revision
				}
				tool("memory_save", "memory-change", args)
			}
		} else {
			message := "个人记忆已保存，可以在个人记忆中查看。"
			if strings.Contains(intent, "忘记") {
				message = "指定个人记忆已删除，原始对话记录仍保留。"
			}
			if strings.Contains(last.Content, `"error"`) {
				message = "记忆未修改，请先核对当前内容。"
			}
			write(map[string]any{"content": message}, "")
			write(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestPersonalMemoryThroughIdentityHTTP(t *testing.T) {
	const initial, changed = "Initial-Agent-Tool-Test!2", "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	model := personalMemoryModelFixture(t)
	defer model.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "memory.db"), RuntimeID: "memory-runtime", WorkspaceID: "memory-workspace", ApplicationKey: "memory-app", Agent: agentmodule.Options{ConversationURL: model.URL, ConversationModel: "memory-fixture", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = host.Close(context.Background()) }()
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "memory-fixture", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	t.Log("grant memory tool permissions")
	grantPersonalTools(t, host, b, true)
	var c agentsdk.Conversation
	if err = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"memory-http"}`, 200).Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	base := "/agent/conversations/" + c.ID
	send := func(key, message string, scoped bool) agentsdk.ConversationRun {
		input := agentsdk.ConversationSend{ClientMessageID: key, Message: message}
		if scoped {
			input.WriteScope = &agentsdk.ConversationWriteScope{PersonalMemory: true}
		}
		raw, _ := json.Marshal(input)
		var out agentsdk.ConversationRun
		if err := json.Unmarshal(b.call("POST", base+"/messages", string(raw), 202).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	waitState := func(id, status string) agentsdk.ConversationRun {
		t.Helper()
		var out agentsdk.ConversationRun
		// Real Identity policy resolution is repeated for the full tool catalog
		// between model steps. Race instrumentation can exceed five seconds;
		// this is a test observation deadline, not the execution timeout.
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if err := json.Unmarshal(b.call("GET", base+"/runs/"+id, "", 200).Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Status == status {
				return out
			}
			if out.Terminal() {
				t.Fatalf("expected %s got %s: %s", status, out.Status, out.ErrorCode)
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("never reached %s: last status=%s error=%s steps=%d", status, out.Status, out.ErrorCode, len(out.Steps))
		return out
	}
	memories := func() []agentsdk.ConversationMemory {
		var out []agentsdk.ConversationMemory
		if err := json.Unmarshal(b.call("GET", "/agent/conversations/memories", "", 200).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	respond := func(run agentsdk.ConversationRun, decision string, status int) string {
		raw, _ := json.Marshal(agentsdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "response-" + run.ID, ExpectedRevision: run.Interaction.Revision, Decision: decision})
		b.call("POST", base+"/runs/"+run.ID+"/respond", string(raw), status)
		return string(raw)
	}
	created := send("create", "记住周报格式", true)
	created = waitState(created.ID, "completed")
	if created.Interaction != nil || created.WriteScope == nil || !created.WriteScope.PersonalMemory {
		t.Fatal("explicit request scope lost or confirmation repeated")
	}
	items := memories()
	if len(items) != 1 || items[0].Revision != 1 {
		t.Fatal("memory was not created once")
	}
	// Changing the scope on a retried message cannot silently expand it.
	b.call("POST", base+"/messages", `{"client_message_id":"create","message":"记住周报格式"}`, 409)
	update := send("update", "修改周报格式", false)
	update = waitState(update.ID, "waiting_confirmation")
	if update.WriteScope != nil || memories()[0].Revision != 1 {
		t.Fatal("scope leaked into next request or wrote before approval")
	}
	// Restart the complete host while the actual memory operation is waiting.
	if err = host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	restored := waitState(update.ID, "waiting_confirmation")
	if restored.Interaction.ID != update.Interaction.ID || restored.Interaction.Arguments != update.Interaction.Arguments {
		t.Fatal("confirmation changed on restart")
	}
	t.Log("revoke memory tool permissions after restart")
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, permission := range previous {
			if permission.PermissionKey != agentsdk.ConversationToolActionPrefix+"memory_save" {
				out = append(out, permission)
			}
		}
		return out
	})
	respond(restored, "approve", 403)
	if memories()[0].Revision != 1 {
		t.Fatal("revoked permission still wrote")
	}
	t.Log("grant memory tool permissions")
	grantPersonalTools(t, host, b, true)
	raw := respond(restored, "approve", 200)
	updated := waitState(update.ID, "completed")
	b.call("POST", base+"/runs/"+update.ID+"/respond", raw, 200)
	if updated.Interaction.Status != "approved" || memories()[0].Revision != 2 {
		t.Fatal("approval repeated or lost its write")
	}
	stream := b.call("GET", base+"/runs/"+update.ID+"/events?after_seq=0", "", 200).Body.String()
	if !strings.Contains(stream, "tool.completed") || !strings.Contains(stream, "interaction.responded") {
		t.Fatal("missing persisted execution events")
	}
	disabled := send("disable", "停用周报记忆", true)
	waitState(disabled.ID, "completed")
	if m := memories()[0]; m.Enabled || m.Revision != 3 {
		t.Fatal("disable failed")
	}
	rejected := send("reject", "忘记周报格式", false)
	rejected = waitState(rejected.ID, "waiting_confirmation")
	respond(rejected, "reject", 200)
	waitState(rejected.ID, "cancelled")
	if len(memories()) != 1 {
		t.Fatal("rejected deletion still happened")
	}
	deleted := send("delete", "忘记周报格式", false)
	deleted = waitState(deleted.ID, "waiting_confirmation")
	respond(deleted, "approve", 200)
	waitState(deleted.ID, "completed")
	if len(memories()) != 0 {
		t.Fatal("confirmed deletion failed")
	}
	grantPersonalTools(t, host, b, false)
	denied := send("denied", "记住周报格式", true)
	waitState(denied.ID, "failed")
	if len(memories()) != 0 {
		t.Fatal("user scope bypassed Identity permission")
	}
	grantPersonalTools(t, host, b, true)
	servePersonalToolAcceptance(t, host, options)
}
