package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

// The model sees the real, owner-filtered tool result. It never chooses an
// execution identity or fabricates a todo response.
type sharedAgentHTTPModel struct {
	peerWebModel
	entered chan struct{}
	release chan struct{}
}

func (m *sharedAgentHTTPModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, in)
	entered, release := m.entered, m.release
	m.entered = nil
	m.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return sdk.ConversationStepResult{}, ctx.Err()
		}
	}
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == "shared-todos" {
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: message.Content}}, nil
		}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "shared-todos", Name: "todo_list", Arguments: `{"scope":"all"}`}}}}, nil
}

func TestSharedAgentHTTPUsesActualCallerAndStopsAfterGrantRevocation(t *testing.T) {
	const initial, changed = "Shared-Agent-Initial!26", "Shared-Agent-Changed!26"
	const ownerSecret, callerContent = "CONFIG-OWNER-PRIVATE-TODO", "CALLER-OWN-TODO"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "shared-agent-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "shared-agent-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &sharedAgentHTTPModel{peerWebModel: peerWebModel{modelKey: "shared-review"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "sharing.db"), RuntimeID: "sharing-runtime", WorkspaceID: "sharing-workspace", ApplicationKey: "sharing-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"shared-review": model}, Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("shared agent")}}})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	admin := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	caller := &browser{t: t, handler: admin.handler, cookies: map[string]*http.Cookie{}}
	admin.login("admin@example.com", initial)
	admin.changePassword(initial, changed)
	caller.login("system_administrator@example.com", initial)
	caller.changePassword(initial, changed)
	ownerID := admin.readSession()["user_id"].(string)
	callerID := caller.readSession()["user_id"].(string)
	if ownerID == callerID || callerID == "" {
		t.Fatal("two independent Identity subjects required")
	}
	grant := func(user string) {
		mutateTestRolePermissions(t, host, admin, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix) {
					out = append(out, p)
				}
			}
			for _, op := range sdk.ConversationCollaborationOperations() {
				out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identity.DataScopeOwner})
			}
			for _, tool := range sdk.PersonalConversationTools() {
				out = append(out, identity.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identity.DataScopeOwner})
			}
			return out
		}, user)
	}
	grant(ownerID)
	grant(callerID)
	for i, b := range []*browser{admin, caller} {
		title := []string{ownerSecret, callerContent}[i]
		b.call("POST", "/agent/todos", accountJSON(sdk.ConversationTodoCreate{ClientID: "seed-todo", Items: []sdk.ConversationTodoInput{{Title: title, Timezone: "Asia/Shanghai"}}}), 200)
	}
	users := []string{callerID}
	config := sdk.ConversationAgentWrite{ClientID: "shared-profile", Name: "共享核对 Agent", Description: "根据使用者当前权限核对自己的待办", Instructions: "读取当前用户待办，保留实际结果", Tools: []string{"todo_list"}, SkillKeys: []string{}, ModelKey: "shared-review", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users}
	invalid := config
	unknown := []string{"no-such-workspace-member"}
	invalid.SharedWithUserIDs = &unknown
	admin.call("POST", "/agent/agents", accountJSON(invalid), 403)
	setTestCollaborationPermissions(t, host, admin, "discover", "configure")
	admin.call("POST", "/agent/agents", accountJSON(config), 403)
	grant(ownerID)
	agent := accountDecode[sdk.ConversationAgent](t, admin.call("POST", "/agent/agents", accountJSON(config), 200))
	assertDirectory := func(shared bool) {
		t.Helper()
		page := accountDecode[sdk.ConversationAgentPage](t, caller.call("GET", "/agent/agents", "", 200))
		found := false
		for _, item := range page.Items {
			if item.ID == agent.ID {
				found = true
				if !item.Shared || item.OwnerUserID != ownerID || len(item.SharedWithUserIDs) != 0 {
					t.Fatal("shared configuration identity or recipient privacy incorrect", item)
				}
			}
		}
		if found != shared {
			t.Fatal("shared directory visibility", found, shared)
		}
	}
	assertDirectory(true)
	config.ExpectedRevision = agent.Revision
	caller.call("PUT", "/agent/agents/"+agent.ID, accountJSON(config), 403)
	create := func(id string) sdk.ConversationDelegationDetail {
		t.Helper()
		c := accountDecode[sdk.Conversation](t, caller.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: "共享配置的独立执行"}), 200))
		input := sdk.ConversationDelegationCreate{ClientID: id, ConversationID: c.ID, AgentID: agent.ID, Purpose: "核对我自己的待办", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "列出当前使用者的待办", Deliverable: "实际待办结果", CompletionConditions: []string{"读取当前使用者待办"}}}
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(caller.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if d.ExecutionSubject == nil || d.ExecutionSubject.UserID != callerID || d.ExecutionSubject.WorkspaceID != options.WorkspaceID || d.ExecutionSubject.RuntimeID != options.RuntimeID {
			t.Fatal("delegation took configuration owner's identity", d.ExecutionSubject)
		}
		return d
	}
	wait := func(d sdk.ConversationDelegationDetail) sdk.ConversationRun {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			var current sdk.ConversationDelegationDetail
			if err := unmarshalPeerDetail(caller.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.Bytes(), &current); err != nil {
				t.Fatal(err)
			}
			if current.Task != nil && current.Task.ExecutionRunID != "" {
				run := accountDecode[sdk.ConversationRun](t, caller.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, "", 200))
				if run.Terminal() {
					return run
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("shared execution did not settle", d.ID)
		return sdk.ConversationRun{}
	}
	d := create("caller-first")
	run := wait(d)
	if run.Status != "completed" || run.Agent == nil || run.Agent.OwnerUserID != ownerID || run.Agent.ExecutionSubject == nil || run.Agent.ExecutionSubject.UserID != callerID {
		t.Fatal("shared configuration not executed as caller", run)
	}
	var ref *sdk.ConversationResultReference
	for _, step := range run.Steps {
		for _, call := range step.Calls {
			if call.ID == "shared-todos" && call.Status == "completed" {
				ref = call.ResultReference
			}
		}
	}
	if ref == nil {
		t.Fatal("missing actual todo execution receipt", run)
	}
	resultPath := "/agent/conversations/" + d.ConversationID + "/runs/" + run.ID + "/result"
	original := caller.call("POST", resultPath, accountJSON(sdk.ConversationResultRead{Reference: *ref, MaxBytes: 8192}), 200).Body.String()
	if !strings.Contains(original, callerContent) || strings.Contains(original, ownerSecret) {
		t.Fatal("tool did not use actual caller data scope", original)
	}
	admin.call("GET", "/agent/delegations/"+d.ID, "", 404)
	admin.call("GET", "/agent/conversations/"+d.ConversationID, "", 404)
	admin.call("POST", resultPath, accountJSON(sdk.ConversationResultRead{Reference: *ref, MaxBytes: 8192}), 404)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin.handler = open()
	caller.handler = admin.handler
	admin.login("admin@example.com", changed)
	caller.login("system_administrator@example.com", changed)
	assertDirectory(true)
	if got := caller.call("POST", resultPath, accountJSON(sdk.ConversationResultRead{Reference: *ref, MaxBytes: 8192}), 200).Body.String(); got != original {
		t.Fatal("restart changed original execution result")
	}
	for _, revoke := range []string{"tool-permission", "agent-sharing"} {
		entered, release := make(chan struct{}), make(chan struct{})
		model.mu.Lock()
		model.entered, model.release = entered, release
		model.mu.Unlock()
		pending := create("revoke-" + revoke)
		select {
		case <-entered:
		case <-time.After(90 * time.Second):
			t.Fatal("model did not reach pre-tool gate", revoke)
		}
		if revoke == "tool-permission" {
			mutateTestRolePermissions(t, host, admin, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
				out := []identity.ProjectRolePermission{}
				for _, p := range previous {
					if p.PermissionKey != sdk.ConversationToolActionPrefix+"todo_list" {
						out = append(out, p)
					}
				}
				return out
			}, callerID)
		} else {
			empty := []string{}
			config.SharedWithUserIDs = &empty
			config.ClientID = "revoke-shared-profile"
			agent = accountDecode[sdk.ConversationAgent](t, admin.call("PUT", "/agent/agents/"+agent.ID, accountJSON(config), 200))
			assertDirectory(false)
		}
		close(release)
		stopped := wait(pending)
		if stopped.Status != "failed" {
			t.Fatal("revoked work did not stop", revoke, stopped)
		}
		for _, step := range stopped.Steps {
			for _, call := range step.Calls {
				if call.Status == "completed" || call.ResultReference != nil {
					t.Fatal("tool executed after revocation", revoke, call)
				}
			}
		}
		if revoke == "tool-permission" {
			grant(callerID)
		}
	}
	model.mu.Lock()
	requests, err := json.Marshal(model.requests)
	model.mu.Unlock()
	if err != nil || strings.Contains(string(requests), ownerSecret) || !strings.Contains(string(requests), callerContent) {
		t.Fatal("model input crossed execution subjects", err)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	admin.handler = open()
	caller.handler = admin.handler
	admin.login("admin@example.com", changed)
	caller.login("system_administrator@example.com", changed)
	assertDirectory(false)
	if os.Getenv("AGENT_SHARING_BROWSER") == "1" {
		verifyAgentSharingBrowser(t, host, options, callerID, agent.ID)
		assertDirectory(false)
	}
}

func verifyAgentSharingBrowser(t *testing.T, host *Host, options Options, user, agent string) {
	t.Helper()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Model: "peer", Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/agent-sharing.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_RECIPIENT="+user, "AGENT_UI_SHARED_AGENT="+agent)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal(err)
	}
}
