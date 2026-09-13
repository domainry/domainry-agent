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

func TestDelegationParticipantsHTTPKeepSenderAndExecutorIndependent(t *testing.T) {
	const initial, changed = "Participant-Initial!26", "Participant-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "participant-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "participant-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &sharedAgentHTTPModel{peerWebModel: peerWebModel{modelKey: "participant-review"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "participants.db"), RuntimeID: "participant-runtime", WorkspaceID: "participant-workspace", ApplicationKey: "participant-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"participant-review": model}, Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("participants")}}})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	owner := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	participant := &browser{t: t, handler: owner.handler, cookies: map[string]*http.Cookie{}}
	owner.login("admin@example.com", initial)
	owner.changePassword(initial, changed)
	participant.login("system_administrator@example.com", initial)
	participant.changePassword(initial, changed)
	user := participant.readSession()["user_id"].(string)
	grantPersonalTools(t, host, owner, true)
	grantCollaborationPermissions(t, host, owner)
	setParticipantRole := func(crossOwner, communicate bool) {
		t.Helper()
		mutateTestRolePermissions(t, host, owner, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) {
					out = append(out, p)
				}
			}
			scope := identity.DataScopeOwner
			if crossOwner {
				scope = identity.DataScopeAll
			}
			for _, op := range sdk.ConversationCollaborationOperations() {
				if op != "communicate" || communicate {
					out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: scope})
				}
			}
			return out
		}, user)
	}
	setParticipantRole(false, true)
	owner.call("POST", "/agent/todos", accountJSON(sdk.ConversationTodoCreate{ClientID: "owner-private-todo", Items: []sdk.ConversationTodoInput{{Title: "EXECUTION-OWNER-PRIVATE-TODO", Timezone: "Asia/Shanghai"}}}), 200)
	agent := accountDecode[sdk.ConversationAgent](t, owner.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "participant-review", Name: "协作核对", Instructions: "读取当前用户待办", Tools: []string{"todo_list"}, SkillKeys: []string{}, ModelKey: "participant-review", Enabled: true, MaxConcurrent: 1}), 200))
	conversation := accountDecode[sdk.Conversation](t, owner.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "source", Title: "明确共享约定"}), 200))
	input := sdk.ConversationDelegationCreate{ClientID: "work", ConversationID: conversation.ID, AgentID: agent.ID, Purpose: "核对个人待办", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对待办", Deliverable: "核对结果", CompletionConditions: []string{"读取实际待办"}}}
	var d sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(owner.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + d.ID
	read := func(b *browser) sdk.ConversationDelegationDetail {
		t.Helper()
		var current sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		return current
	}
	wait := func(condition func(sdk.ConversationDelegationDetail) bool) sdk.ConversationDelegationDetail {
		t.Helper()
		var current sdk.ConversationDelegationDetail
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); {
			current = read(owner)
			if condition(current) {
				return current
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("participant scenario timeout", current)
		return current
	}
	d = wait(func(current sdk.ConversationDelegationDetail) bool {
		return current.Task != nil && current.Task.Status == "completed"
	})
	participant.call("GET", path, "", 404)
	members := []sdk.ConversationDelegationParticipantInput{{UserID: user, Operations: []string{"view", "communicate"}}}
	setMembers := func(client string, members []sdk.ConversationDelegationParticipantInput) {
		t.Helper()
		d = read(owner)
		owner.call("POST", path+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: client, ExpectedRevision: d.Revision, Action: "set_participants", Reason: "明确这项委派的参与范围", Participants: &members}), 200)
	}
	setMembers("invite", members)
	// Both the explicit delegation grant and current Identity data policy are
	// required. A role limited to the user's own records still denies this one.
	participant.call("GET", path, "", 403)
	setParticipantRole(true, true)
	shared := read(participant)
	if shared.OwnerUserID != "admin" || shared.Access == nil || !shared.Access.View || !shared.Access.Communicate || shared.Access.Manage || shared.Access.Receive || shared.Access.ExecutionRead || shared.Access.DeliveryRead || shared.Access.Share || shared.Task != nil || shared.Delivery != nil || shared.SourceAgent != nil || shared.ConversationID != "" || shared.SourceConversationID != "" || shared.TaskID != "" || len(shared.Participants) != 1 || shared.Participants[0].UserID != user {
		t.Fatal("participant scope exposed private work or management", shared)
	}
	list := participant.call("GET", "/agent/delegations", "", 200).Body.String()
	if !strings.Contains(list, d.ID) || strings.Contains(list, "EXECUTION-OWNER-PRIVATE-TODO") {
		t.Fatal("participant list scope", list)
	}
	participant.call("GET", "/agent/conversations/"+d.ConversationID, "", 404)
	participant.call("POST", path+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "take-control", ExpectedRevision: shared.Revision, Action: "cancel", Reason: "not granted"}), 403)
	participant.call("POST", path+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "take-members", ExpectedRevision: shared.Revision, Action: "set_participants", Reason: "not owner", Participants: &members}), 403)
	messageInput := sdk.ConversationAgentMessageSend{ClientID: "participant-input", ToAgentID: d.ToAgentID, Content: "PARTICIPANT-EXPLICIT-MESSAGE", BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision}
	message := accountDecode[sdk.ConversationAgentMessage](t, participant.call("POST", path+"/messages", accountJSON(messageInput), 200))
	if message.FromUserID != user || message.ParticipantUserID != user || message.ParticipantRevision == 0 || message.ConversationID != "" {
		t.Fatal("HTTP sender identity changed", message)
	}
	consumed := wait(func(current sdk.ConversationDelegationDetail) bool {
		for _, m := range current.Messages {
			if m.ID == message.ID && m.ConsumedByRunID != "" && current.Task != nil && current.Task.Status == "completed" {
				return true
			}
		}
		return false
	})
	model.mu.Lock()
	seen := false
	for _, request := range model.requests {
		for _, m := range request.Messages {
			if strings.Contains(m.Content, messageInput.Content) && strings.HasPrefix(m.Content, "Delegation participant message (a separate user;") {
				seen = true
			}
		}
	}
	model.mu.Unlock()
	if !seen || consumed.ExecutionSubject == nil || consumed.ExecutionSubject.UserID != "admin" {
		t.Fatal("actual participant input or original executor missing")
	}
	runPath := "/agent/conversations/" + consumed.ConversationID + "/runs/" + consumed.Task.ExecutionRunID
	run := accountDecode[sdk.ConversationRun](t, owner.call("GET", runPath, "", 200))
	if run.Agent == nil || run.Agent.ExecutionSubject == nil || run.Agent.ExecutionSubject.UserID != "admin" {
		t.Fatal("participant replaced worker identity", run)
	}
	participant.call("GET", runPath, "", 404)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.handler = open()
	participant.handler = owner.handler
	owner.login("admin@example.com", changed)
	participant.login("system_administrator@example.com", changed)
	if after := read(participant); after.Participants[0].Revision != shared.Participants[0].Revision || len(after.Messages) == 0 {
		t.Fatal("restart lost participant grant or communication", after)
	}
	// Keep a current owner-authorized model request open, then withdraw the
	// sender's role before a next-run participant message can enter context.
	entered, release := make(chan struct{}), make(chan struct{})
	model.mu.Lock()
	model.entered, model.release = entered, release
	model.mu.Unlock()
	gate := sdk.ConversationAgentMessageSend{ClientID: "owner-gate", ToAgentID: d.ToAgentID, Content: "Owner follow-up", BriefVersion: 1, AgreementRevision: 1}
	owner.call("POST", path+"/messages", accountJSON(gate), 200)
	select {
	case <-entered:
	case <-time.After(90 * time.Second):
		t.Fatal("owner model did not reach pending-message gate")
	}
	blockedInput := messageInput
	blockedInput.ClientID, blockedInput.Content, blockedInput.DeliveryMode = "pending-withdrawal", "REVOKED-BEFORE-MODEL-INTAKE", "next_run"
	blocked := accountDecode[sdk.ConversationAgentMessage](t, participant.call("POST", path+"/messages", accountJSON(blockedInput), 200))
	setParticipantRole(true, false)
	close(release)
	wait(func(current sdk.ConversationDelegationDetail) bool {
		for _, m := range current.Messages {
			if m.ID == blocked.ID && m.Superseded && current.Task != nil && current.Task.Status == "completed" {
				return true
			}
		}
		return false
	})
	model.mu.Lock()
	requests, err := json.Marshal(model.requests)
	model.mu.Unlock()
	if err != nil || strings.Contains(string(requests), blockedInput.Content) {
		t.Fatal("revoked pending participant input reached model", err)
	}
	if after := read(participant); after.Access.Communicate || len(after.Messages) > 0 {
		t.Fatal("current role withdrawal retained communication", after)
	}
	messageInput.ClientID = "denied-role"
	participant.call("POST", path+"/messages", accountJSON(messageInput), 403)
	setParticipantRole(true, true)
	setMembers("remove", []sdk.ConversationDelegationParticipantInput{})
	participant.call("GET", path, "", 404)
	participant.call("POST", path+"/messages", accountJSON(messageInput), 404)
	if got := participant.call("GET", "/agent/delegations", "", 200).Body.String(); strings.Contains(got, d.ID) {
		t.Fatal("revoked grant survived directory", got)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.handler = open()
	participant.handler = owner.handler
	participant.login("system_administrator@example.com", changed)
	participant.call("GET", path, "", 404)
	if os.Getenv("AGENT_PARTICIPANTS_BROWSER") == "1" {
		verifyDelegationParticipantsBrowser(t, host, options, user, conversation.ID)
		participant.call("GET", path, "", 404)
	}
}

func verifyDelegationParticipantsBrowser(t *testing.T, host *Host, options Options, user, source string) {
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
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests/delegation-participants.browser.mjs"))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_RECIPIENT="+user, "AGENT_UI_CONVERSATION="+source)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}
