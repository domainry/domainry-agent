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
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
)

func TestPersonalToolsThroughIdentityHTTPProviderPersistenceAndSSE(t *testing.T) {
	const initial = "Initial-Agent-Tool-Test!2"
	const changed = "Changed-Agent-Tool-Test!3"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "test-agent-identity-signing-key-32bytes")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "test-agent-identity-encryption-key-32bytes")
	t.Setenv("APP_ENV", "development")
	var requests atomic.Int32
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		available := false
		for _, tool := range payload.Tools {
			available = available || tool.Function.Name == "calculate"
		}
		if !available {
			t.Error("calculate missing from authorized catalog")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		write := func(delta any, finish string) {
			raw, _ := json.Marshal(map[string]any{"model": "test-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		n := requests.Add(1)
		if payload.Messages[len(payload.Messages)-1].Role != "tool" {
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "decimal-1", "type": "function", "function": map[string]any{"name": "calculate", "arguments": `{"operation":"expression",`}}}}, "")
			write(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": `"expression":"0.1+0.2","unit":"CNY"}`}}}}, "")
			write(map[string]any{}, "tool_calls")
		} else {
			last := payload.Messages[len(payload.Messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, `"value":"0.30"`) || !strings.Contains(last.Content, `"status":"completed"`) {
				t.Errorf("actual calculation result missing: %s", last.Content)
			}
			write(map[string]any{"content": "结果是 "}, "")
			if n == 2 {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Second):
					t.Error("step preview was not observed before model completion")
					return
				}
			} else {
				time.Sleep(350 * time.Millisecond)
			}
			write(map[string]any{"content": "0.30 元。"}, "")
			write(map[string]any{}, "stop")
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "tools.db"), RuntimeID: "tool-runtime", WorkspaceID: "tool-workspace", ApplicationKey: "tool-app", Agent: agentmodule.Options{ConversationURL: upstream.URL, ConversationModel: "test-model", ConversationTimezone: "Asia/Shanghai", ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close(context.Background())
	handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "test-model", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
	if err != nil {
		t.Fatal(err)
	}
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	var conversation agentsdk.Conversation
	_ = json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"decimal-test"}`, 200).Body.Bytes(), &conversation)
	base := "/agent/conversations/" + conversation.ID
	var run agentsdk.ConversationRun
	_ = json.Unmarshal(b.call("POST", base+"/messages", `{"client_message_id":"calculate-one","message":"计算 0.1 + 0.2 元"}`, 202).Body.Bytes(), &run)
	preview := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var events agentsdk.ConversationEventPage
		_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID+"/events", "", 200).Body.Bytes(), &events)
		for _, event := range events.Items {
			if event.Type == "step.text.delta" && event.Data["delta"] == "结果是 " {
				preview = true
				break
			}
		}
		if preview {
			break
		}
		_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
		if run.Terminal() {
			t.Fatalf("run finished before preview: %+v", run)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !preview {
		t.Fatal("no persisted live step preview")
	}
	release <- struct{}{}
	for !run.Terminal() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		_ = json.Unmarshal(b.call("GET", base+"/runs/"+run.ID, "", 200).Body.Bytes(), &run)
	}
	if run.Status != "completed" || requests.Load() != 2 {
		t.Fatalf("run=%+v requests=%d", run, requests.Load())
	}
	var messages agentsdk.ConversationMessagePage
	_ = json.Unmarshal(b.call("GET", base+"/messages", "", 200).Body.Bytes(), &messages)
	if len(messages.Items) != 2 || messages.Items[1].Content != "结果是 0.30 元。" {
		t.Fatalf("messages=%+v", messages)
	}
	sse := b.call("GET", base+"/runs/"+run.ID+"/events/stream?scope="+b.scope, "", 200).Body.String()
	for _, event := range []string{"step.tool.started", "step.tool.arguments.delta", "tool.completed", "step.text.delta", "run.completed"} {
		if !strings.Contains(sse, event) {
			t.Errorf("SSE missing %s", event)
		}
	}
	if strings.Contains(sse, "provider_state") || strings.Contains(sse, "authorization_revision") {
		t.Fatal("private execution state in browser stream")
	}
	servePersonalToolAcceptance(t, host, options)
	grantPersonalTools(t, host, b, false)
	a := agentsdk.ConversationAuthority{Known: true, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, UserID: "admin"}
	for _, definition := range agentsdk.PersonalConversationTools() {
		out, err := host.AuthorizeConversationTool(t.Context(), agentsdk.ConversationToolRequest{Authority: a, Definition: definition})
		if err != nil || out.Granted {
			t.Fatalf("revoked grant remained active: %+v %v", out, err)
		}
	}
}

// Grant through the actual Identity authoring API in this isolated test DB.
// Production hosts must obtain role grants from their Identity administrator.
func grantPersonalTools(t *testing.T, host *Host, b *browser, enabled bool, respond ...bool) {
	t.Helper()
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		permissions := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if !strings.HasPrefix(p.PermissionKey, agentsdk.ConversationToolActionPrefix) && p.PermissionKey != agentsdk.ConversationInteractionPermission().Key {
				permissions = append(permissions, p)
			}
		}
		if enabled {
			if len(respond) == 0 || respond[0] {
				permissions = append(permissions, identitysdk.ProjectRolePermission{PermissionKey: agentsdk.ConversationInteractionPermission().Key, DataScope: identitysdk.DataScopeOwner})
			}
			for _, definition := range agentsdk.PersonalConversationTools() {
				permissions = append(permissions, identitysdk.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identitysdk.DataScopeOwner})
			}
		}
		return permissions
	})
}

func mutateTestRolePermissions(t *testing.T, host *Host, b *browser, transform func([]identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission, users ...string) {
	t.Helper()
	user := "admin"
	if len(users) > 0 {
		user = users[0]
	}
	assignments, err := host.Identity.Projection().ListUserRoleAssignments(t.Context(), identitysdk.UserRoleAssignmentQuery{UserID: identitysdk.SubjectID(user)})
	if err != nil || len(assignments) == 0 {
		t.Fatalf("role assignments: %v %v", assignments, err)
	}
	mux := http.NewServeMux()
	for _, adapter := range host.Identity.(identityhttpapi.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	path := "/identity/roles/" + assignments[0].RoleID + "/permissions"
	call := func(method, body, hash string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+b.cookies["domainry_agent_access"].Value)
		r.Header.Set("X-Workspace-ID", string(host.application.WorkspaceID))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Expected-Schema-Hash", hash)
		r.Header.Set("Idempotency-Key", fmt.Sprintf("tools-grant-%d", time.Now().UnixNano()))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("Identity %s: %d %s", method, w.Code, w.Body.String())
		}
		return w
	}
	current := call("GET", "", "")
	var previous []identitysdk.ProjectRolePermission
	if err = json.Unmarshal(current.Body.Bytes(), &previous); err != nil {
		t.Fatal(err)
	}
	permissions := transform(previous)
	raw, _ := json.Marshal(map[string]any{"permissions": permissions, "business_reason": "Isolated Agent capability acceptance test"})
	call("PUT", string(raw), current.Header().Get("X-Resource-Hash"))
}
