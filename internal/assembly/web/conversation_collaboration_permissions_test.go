package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

type peerPermissionGateModel struct {
	peerWebModel
	next    chan struct{}
	release chan struct{}
}

func (m *peerPermissionGateModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	started, release := m.next, m.release
	m.next = nil
	m.mu.Unlock()
	if started != nil {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return sdk.ConversationStepResult{}, ctx.Err()
		}
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

func setTestCollaborationPermissions(t *testing.T, host *Host, b *browser, operations ...string) {
	t.Helper()
	mutateTestRolePermissions(t, host, b, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		result := []identity.ProjectRolePermission{}
		for _, p := range previous {
			if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) {
				result = append(result, p)
			}
		}
		for _, op := range operations {
			result = append(result, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identity.DataScopeOwner})
		}
		return result
	})
}

// Real Identity grants, public HTTP adapters and durable workers. A role edit
// must affect every access path without recreating the caller's login session.
func TestPeerCollaborationPermissionsRecheckCurrentRoleAcrossEntrypoints(t *testing.T) {
	const initial, changed = "Peer-Initial!22", "Peer-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "peer-policy-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "peer-policy-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "peer-policy.db"), RuntimeID: "peer-policy-runtime", WorkspaceID: "peer-policy-workspace", ApplicationKey: "peer-policy-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"review": &peerPermissionGateModel{peerWebModel: peerWebModel{collaborate: true, modelKey: "review"}}}, Poll: 5 * time.Millisecond}}}
	host, err := Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	}()
	newBrowser := func() *browser {
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}, ApplicationRoutes: host.CollaborationSetupRoutes()})
		if err != nil {
			t.Fatal(err)
		}
		return &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	}
	b := newBrowser()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	readAccess := func() sdk.ConversationCollaborationAuthorization {
		var out sdk.ConversationCollaborationAuthorization
		if err := json.Unmarshal(b.call("GET", "/agent/collaboration-access", "", 200).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if len(readAccess().Allowed) != 0 {
		t.Fatal("startup implicitly granted collaboration")
	}
	b.call("GET", "/agent/agents", "", 403)
	b.call("POST", "/app/product/collaboration-setup", "{}", 200)
	firstAccess := readAccess()
	if len(firstAccess.Allowed) != len(sdk.ConversationCollaborationOperations()) {
		t.Fatalf("explicit setup omitted grants: %+v", firstAccess)
	}
	var agent sdk.ConversationAgent
	json.Unmarshal(b.call("POST", "/agent/agents", `{"client_id":"policy-agent","name":"权限核查员","description":"独立核查","instructions":"核查并交付","tools":["time_now","delegation_get","agent_message","delegation_update"],"skill_keys":[],"model_key":"review","enabled":true,"max_concurrent":1}`, 200).Body.Bytes(), &agent)
	var source sdk.Conversation
	json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"policy-source","title":"权限核查任务"}`, 200).Body.Bytes(), &source)
	input := sdk.ConversationDelegationCreate{ClientID: "policy-work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "验证当前授权", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核查当前权限", Deliverable: "核查结论", CompletionConditions: []string{"说明依据"}}}
	raw, _ := json.Marshal(input)
	var d sdk.ConversationDelegationDetail
	unmarshalPeerDetail(b.call("POST", "/agent/delegations", string(raw), 200).Body.Bytes(), &d)
	path := "/agent/delegations/" + d.ID
	deadline := time.Now().Add(90 * time.Second)
	for {
		unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &d)
		if d.Status == "delivered" && d.Task != nil && d.Task.Status == "completed" {
			break
		}
		if d.Status == "failed" && d.Task != nil {
			var failedRun sdk.ConversationRun
			if d.Task.ExecutionRunID != "" {
				_ = json.Unmarshal(b.call("GET", "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "", 200).Body.Bytes(), &failedRun)
			}
			t.Fatalf("fixture failed: task=%+v run=%+v", *d.Task, failedRun)
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not deliver: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Wait for source notification consumption before revoking its current data.
	deadline = time.Now().Add(90 * time.Second)
	for {
		var c sdk.Conversation
		json.Unmarshal(b.call("GET", "/agent/conversations/"+source.ID, "", 200).Body.Bytes(), &c)
		if c.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("source remained active")
		}
		time.Sleep(20 * time.Millisecond)
	}
	exercisePeerPermissionBrowser(t, host, b, options, source.ID, d.ID)
	full := d
	runPath := "/agent/conversations/" + d.ConversationID + "/runs/" + d.Task.ExecutionRunID
	var saved *sdk.ConversationResultReference
	for _, step := range d.Task.Steps {
		for _, call := range step.Calls {
			if call.ID == "peer-deliver" {
				saved = call.ResultReference
			}
		}
	}
	if saved == nil {
		t.Fatal("missing immutable delivery result")
	}
	getDetail := func() sdk.ConversationDelegationDetail {
		var out sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	setTestCollaborationPermissions(t, host, b, "view")
	if access := readAccess(); len(access.Allowed) != 1 || access.Allowed[0] != "view" || access.Revision == firstAccess.Revision {
		t.Fatalf("stale principal: %+v", access)
	}
	d = getDetail()
	if d.Access == nil || !d.Access.View || d.Task != nil || d.Delivery != nil || d.Verification != nil || len(d.Messages) != 0 {
		t.Fatalf("metadata disclosed protected contents: %+v", d)
	}
	for _, endpoint := range []string{runPath, runPath + "/events", runPath + "/events/stream?scope=" + url.QueryEscape(b.scope), "/agent/conversations/" + d.ConversationID + "/messages", "/agent/conversation-tasks/" + d.TaskID, path + "/deliveries"} {
		b.call("GET", endpoint, "", 403)
	}
	for _, endpoint := range []string{runPath + "/cancel", runPath + "/resume", "/agent/conversation-tasks/" + d.TaskID + "/cancel", "/agent/conversation-tasks/" + d.TaskID + "/resume"} {
		b.call("POST", endpoint, "{}", 403)
	}
	raw, _ = json.Marshal(sdk.ConversationResultRead{Reference: *saved, MaxBytes: 4096})
	b.call("POST", runPath+"/result", string(raw), 403)
	var tasks sdk.ConversationTaskPage
	json.Unmarshal(b.call("GET", "/agent/conversation-tasks", "", 200).Body.Bytes(), &tasks)
	for _, task := range tasks.Items {
		if task.ID == d.TaskID {
			t.Fatal("task list bypassed execution-read restriction")
		}
	}
	decision := func(action string, want int) {
		d = getDetail()
		raw, _ := json.Marshal(sdk.ConversationDelegationUpdate{ClientID: "policy-" + action, ExpectedRevision: d.Revision, Action: action, Reason: "核对权限边界"})
		b.call("POST", path+"/decisions", string(raw), want)
	}
	decision("pause", 403)
	message, _ := json.Marshal(sdk.ConversationAgentMessageSend{ClientID: "policy-message", ToAgentID: d.ToAgentID, BriefVersion: d.Brief.Version, AgreementRevision: d.AgreementRevision, Content: "权限范围内的沟通"})
	b.call("POST", path+"/messages", string(message), 403)
	setTestCollaborationPermissions(t, host, b, "view", "communicate")
	b.call("POST", path+"/messages", string(message), 200)
	decision("pause", 403)
	if len(getDetail().Messages) == 0 {
		t.Fatal("communication grant did not expose authorized inbox")
	}
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	d = getDetail()
	if d.Delivery == nil || d.Task != nil || len(d.Messages) != 0 {
		t.Fatalf("delivery and execution grants conflated: %+v", d.Access)
	}
	b.call("GET", path+"/deliveries", "", 200)
	b.call("GET", runPath, "", 403)
	decision("pause", 403)
	setTestCollaborationPermissions(t, host, b, "view", "execution_read")
	b.call("GET", runPath, "", 200)
	d = getDetail()
	if d.Task == nil || d.Delivery != nil {
		t.Fatalf("execution permission granted delivery access: %+v", d.Access)
	}
	// Reading a previously saved delegation_update payload rechecks delivery_read.
	b.call("POST", runPath+"/result", string(raw), 403)
	setTestCollaborationPermissions(t, host, b, "view", "manage")
	decision("pause", 200)
	if getDetail().Status != "paused" {
		t.Fatal("manager without raw execution access could not pause delegation")
	}
	decision("review_delivery", 403)
	setTestCollaborationPermissions(t, host, b, "view", "receive")
	decision("pause", 403)
	// A valid recipient action is distinct from management; no original tool grants changed.
	decision("reject", 200)
	if getDetail().Status != "rejected" {
		t.Fatal("receive permission did not permit rejection")
	}

	// Admission is allowed, then the role is revoked while the model is in
	// flight. Its returned tool request must fail before invoking the tool.
	setTestCollaborationPermissions(t, host, b, sdk.ConversationCollaborationOperations()...)
	receiver := options.Agent.ConversationOptions.AgentModels["review"].(*peerPermissionGateModel)
	started, release := make(chan struct{}), make(chan struct{})
	receiver.mu.Lock()
	receiver.next, receiver.release = started, release
	receiver.mu.Unlock()
	input.ClientID = "policy-no-receive"
	raw, _ = json.Marshal(input)
	var denied sdk.ConversationDelegationDetail
	unmarshalPeerDetail(b.call("POST", "/agent/delegations", string(raw), 200).Body.Bytes(), &denied)
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("model did not reach the revocation boundary")
	}
	operations := []string{}
	for _, op := range sdk.ConversationCollaborationOperations() {
		if op != "receive" {
			operations = append(operations, op)
		}
	}
	setTestCollaborationPermissions(t, host, b, operations...)
	close(release)
	deadline = time.Now().Add(45 * time.Second)
	for {
		unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+denied.ID, "", 200).Body.Bytes(), &denied)
		if denied.Task != nil && denied.Task.Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revoked receiver continued execution: %+v", denied)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if denied.Task.ErrorCode != "collaboration_access_denied" {
		t.Fatalf("revocation lost its actionable error: %+v", denied.Task)
	}
	for _, step := range denied.Task.Steps {
		for _, call := range step.Calls {
			if call.Status != "not_started" || call.StartedAt != nil || call.ResultReference != nil {
				t.Fatalf("revoked receive invoked a tool: %+v", call)
			}
		}
	}
	// Discovery and direct admission report the same current receive restriction.
	var directory sdk.ConversationAgentPage
	json.Unmarshal(b.call("GET", "/agent/agents", "", 200).Body.Bytes(), &directory)
	for _, candidate := range directory.Availability {
		if candidate.CanAccept || !slices.Contains(candidate.Reasons, "receiver_access_denied") {
			t.Fatalf("directory ignored receiver access: %+v", candidate)
		}
	}
	input.ClientID = "policy-no-admission"
	raw, _ = json.Marshal(input)
	b.call("POST", "/agent/delegations", string(raw), 403)
	// Revoke all: delegated metadata disappears from generic conversation listing.
	setTestCollaborationPermissions(t, host, b)
	b.call("GET", path, "", 403)
	b.call("GET", "/agent/conversations/"+full.ConversationID, "", 403)
	var conversations sdk.ConversationPage
	json.Unmarshal(b.call("GET", "/agent/conversations", "", 200).Body.Bytes(), &conversations)
	for _, c := range conversations.Items {
		if c.ID == full.ConversationID {
			t.Fatal("conversation list bypassed view permission")
		}
	}
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	host, err = Open(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	b = newBrowser()
	b.login("admin@example.com", changed)
	if access := readAccess(); slices.Contains(access.Allowed, "view") || len(access.Allowed) != 0 {
		t.Fatalf("restart restored revoked permissions: %+v", access)
	}
	b.call("GET", path, "", 403)
}
