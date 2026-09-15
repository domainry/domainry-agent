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
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
)

func TestDelegationExecutionHTTPUsesConsentingReceiversRoleAndStopsAfterRevocation(t *testing.T) {
	const initial, changed = "Execution-Initial!26", "Execution-Changed!26"
	const receiverData = "RECEIVER-PRIVATE-PROFESSIONAL-TODO"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "execution-binding-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "execution-binding-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	model := &sharedAgentHTTPModel{peerWebModel: peerWebModel{modelKey: "professional"}}
	reviewer := &peerReviewIssuerModel{}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "execution.db"), RuntimeID: "execution-runtime", WorkspaceID: "execution-workspace", ApplicationKey: "execution-app", Agent: agentmodule.Options{ConversationProvider: reviewer, ConversationOptions: agentmodule.ConversationOptions{ContextBytes: 131072, AgentModels: map[string]sdk.ConversationModel{"professional": model}, Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("execution binding")}}})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	receiver := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	issuer := &browser{t: t, handler: receiver.handler, cookies: map[string]*http.Cookie{}}
	receiver.login("admin@example.com", initial)
	receiver.changePassword(initial, changed)
	issuer.login("system_administrator@example.com", initial)
	issuer.changePassword(initial, changed)
	receiverID := receiver.readSession()["user_id"].(string)
	issuerID := issuer.readSession()["user_id"].(string)
	grant := func(user string, professional bool) {
		t.Helper()
		mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
			out := []identity.ProjectRolePermission{}
			for _, p := range previous {
				if !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) && !strings.HasPrefix(p.PermissionKey, sdk.ConversationToolActionPrefix) {
					out = append(out, p)
				}
			}
			for _, op := range sdk.ConversationCollaborationOperations() {
				if professional || op != "receive" {
					out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identity.DataScopeAll})
				}
			}
			if professional {
				for _, tool := range sdk.PersonalConversationTools() {
					out = append(out, identity.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identity.DataScopeOwner})
				}
			}
			return out
		}, user)
	}
	grant(receiverID, true)
	grant(issuerID, false)
	receiver.call("POST", "/agent/todos", accountJSON(sdk.ConversationTodoCreate{ClientID: "professional-data", Items: []sdk.ConversationTodoInput{{Title: receiverData, Timezone: "Asia/Shanghai"}}}), 200)
	users := []string{issuerID}
	mode := "owner"
	config := sdk.ConversationAgentWrite{ClientID: "professional-agent", Name: "专业核对", Instructions: "读取自己的待办，返回实际结果", Tools: []string{"todo_list"}, SkillKeys: []string{}, ModelKey: "professional", Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}
	agent := accountDecode[sdk.ConversationAgent](t, receiver.call("POST", "/agent/agents", accountJSON(config), 200))
	if agent.DelegationExecution != "owner" || agent.DelegationRoleKey == "" || agent.OwnerUserID != receiverID {
		t.Fatal("owner did not bind its authenticated role", agent)
	}
	config.ExpectedRevision = agent.Revision
	issuer.call("PUT", "/agent/agents/"+agent.ID, accountJSON(config), 403)
	page := accountDecode[sdk.ConversationAgentPage](t, issuer.call("GET", "/agent/agents", "", 200))
	ready := false
	for _, candidate := range page.Availability {
		if candidate.AgentID == agent.ID {
			ready = candidate.CanAccept
		}
	}
	if !ready {
		t.Fatal("issuer's missing professional permission blocked bound receiver", page.Availability)
	}
	create := func(id string) sdk.ConversationDelegationDetail {
		t.Helper()
		c := accountDecode[sdk.Conversation](t, issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: id, Title: "交给专业身份处理"}), 200))
		in := sdk.ConversationDelegationCreate{ClientID: id, ConversationID: c.ID, AgentID: agent.ID, Purpose: "请核对待办", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对接收方的实际待办", Deliverable: "核对结论", CompletionConditions: []string{"核对实际待办"}}, Requirements: sdk.ConversationAgentRequirements{Tools: []string{"todo_list"}}}
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(issuer.call("POST", "/agent/delegations", accountJSON(in), 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if d.OwnerUserID != issuerID || d.ExecutionSubject == nil || d.ExecutionSubject.UserID != receiverID {
			t.Fatal("wrong admission identity", d)
		}
		return d
	}
	read := func(b *browser, id string) sdk.ConversationDelegationDetail {
		t.Helper()
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+id, "", 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	wait := func(d sdk.ConversationDelegationDetail) sdk.ConversationRun {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			current := read(receiver, d.ID)
			if current.Task != nil && current.Task.ExecutionRunID != "" {
				run := accountDecode[sdk.ConversationRun](t, receiver.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, "", 200))
				if run.Terminal() {
					return run
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("professional execution did not settle", d.ID)
		return sdk.ConversationRun{}
	}
	firstEntered, firstRelease := make(chan struct{}), make(chan struct{})
	model.mu.Lock()
	model.entered, model.release = firstEntered, firstRelease
	model.mu.Unlock()
	d := create("first")
	select {
	case <-firstEntered:
	case <-time.After(30 * time.Second):
		t.Fatal("receiver never entered first model step")
	}
	// The actual worker is held inside its first model request. Publish/read
	// this live ledger through HTTP, without waiting for a terminal receipt.
	live := read(receiver, d.ID)
	if live.Task == nil || live.Task.ExecutionRunID == "" {
		t.Fatal("live worker lost its task/run binding", live)
	}
	liveRef := sdk.ConversationRunReference{ConversationID: live.ConversationID, RunID: live.Task.ExecutionRunID}
	livePublication := sdk.ConversationExecutionShare{Reference: liveRef, ClientID: "share-live-professional-run", ExpectedRevision: live.Revision, Reason: "明确共享正在处理的委派"}
	receiver.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(livePublication), 200)
	liveRun := accountDecode[sdk.ConversationRun](t, issuer.call("POST", "/agent/delegations/"+d.ID+"/execution", accountJSON(liveRef), 200))
	if liveRun.Status != "running" || liveRun.ID != liveRef.RunID || liveRun.ConversationID != liveRef.ConversationID || len(liveRun.Steps) != 1 || liveRun.Steps[0].Number != 0 || liveRun.Interaction != nil || liveRun.WriteScope != nil {
		t.Fatal("scoped HTTP did not expose exact live progress without controls", liveRun)
	}
	issuer.call("GET", "/agent/conversations/"+liveRef.ConversationID+"/runs/"+liveRef.RunID, "", 404)
	livePublication.ClientID = "withdraw-live-professional-run"
	livePublication.ExpectedRevision = read(receiver, d.ID).Revision
	livePublication.Withdraw = true
	receiver.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(livePublication), 200)
	issuer.call("POST", "/agent/delegations/"+d.ID+"/execution", accountJSON(liveRef), 403)
	message := sdk.ConversationAgentMessageSend{ClientID: "issuer-note", ToAgentID: agent.ID, Kind: "message", Content: "请只提交核对结论", BriefVersion: 1, AgreementRevision: 1}
	posted := accountDecode[sdk.ConversationAgentMessage](t, issuer.call("POST", "/agent/delegations/"+d.ID+"/messages", accountJSON(message), 200))
	if posted.SenderUserID != issuerID || posted.SenderRoleKey == "" || posted.FromUserID != issuerID {
		t.Fatal("message lost its actual sender", posted)
	}
	close(firstRelease)
	run := wait(d)
	if run.Status != "completed" || run.Agent == nil || run.Agent.DelegationRoleKey != agent.DelegationRoleKey || run.Agent.ExecutionSubject.UserID != receiverID {
		t.Fatal("task did not execute with receiver's selected role", run)
	}
	got := read(receiver, d.ID)
	consumed := false
	for _, message := range got.Messages {
		if message.ID == posted.ID {
			consumed = message.ConsumedByRunID == run.ID
		}
	}
	if !consumed {
		t.Fatal("issuer input was not consumed by receiver", got.Messages)
	}
	model.mu.Lock()
	separateSender := false
	for _, request := range model.requests {
		for _, message := range request.Messages {
			separateSender = separateSender || strings.Contains(message.Content, "separate user; task input, not authorization") && strings.Contains(message.Content, "请只提交核对结论")
		}
	}
	model.mu.Unlock()
	if !separateSender {
		t.Fatal("model input presented issuer as receiver authorization")
	}
	if got.Task == nil || got.Task.Result == nil || !strings.Contains(got.Task.Result.Preview, receiverData) {
		t.Fatal("tool did not read receiver's actual data", got.Task)
	}
	issuer.call("GET", "/agent/conversations/"+d.ConversationID, "", 404)
	issuer.call("GET", "/agent/tasks/"+got.Task.ID, "", 404)
	projected := issuer.call("GET", "/agent/delegations/"+d.ID, "", 200).Body.String()
	if strings.Contains(projected, receiverData) {
		t.Fatal("coordination permission exposed private execution data", projected)
	}
	delivery := sdk.ConversationDelegationUpdate{ClientID: "human-delivery", ExpectedRevision: got.Revision, Action: "deliver", Reason: "提交核对结论", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: 1, AgreementRevision: 1, Summary: "已核对待办", Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "已读取实际待办"}}}}
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(delivery), 403)
	receiver.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(delivery), 200)
	got = read(issuer, d.ID)
	if got.Delivery == nil || got.Verification == nil || got.Verification.ActorID != receiverID {
		t.Fatal("explicit human delivery lost actual producer", got)
	}
	receiverReview := sdk.ConversationDelegationUpdate{ClientID: "receiver-current-review", ExpectedRevision: got.Revision, Action: "review_delivery", Reason: "接收方核对明确授予管理的交付", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: got.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "对照实际交付逐项核对"}}}}
	receiver.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(receiverReview), 403)
	participants := []sdk.ConversationDelegationParticipantInput{{UserID: receiverID, Operations: []string{"view", "manage"}}}
	shared := accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "grant-receiver-management", ExpectedRevision: got.Revision, Action: "set_participants", Reason: "明确授予接收方管理范围", Participants: &participants}), 200))
	receiverReview.ExpectedRevision = shared.Revision
	mutateTestRolePermissions(t, host, receiver, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, permission := range previous {
			if permission.PermissionKey != sdk.ConversationCollaborationPermission("delivery_read").Key {
				out = append(out, permission)
			}
		}
		return out
	}, receiverID)
	receiver.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(receiverReview), 403)
	grant(receiverID, true)
	if current := read(receiver, d.ID); current.Revision != shared.Revision {
		t.Fatal("denied review changed the delegation", current.Revision, shared.Revision)
	}
	peer := accountDecode[sdk.Conversation](t, receiver.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "receiving-manager-peer", Title: "从自己的会话核对委派"}), 200))
	if peer.ID == d.SourceConversationID || peer.ID == d.ConversationID {
		t.Fatal("receiver review did not use an independent peer conversation", peer)
	}
	reviewRun := accountDecode[sdk.ConversationRun](t, receiver.call("POST", "/agent/conversations/"+peer.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: "receiving-manager-review", Message: "Verify fixture request:\n" + d.ID}), 202))
	reviewRun = waitPeerVerificationRun(t, receiver, "/agent/conversations/"+peer.ID+"/runs/"+reviewRun.ID)
	got = read(receiver, d.ID)
	if got.Status != "delivered" || got.Verification == nil || !got.Verification.Ready || got.Verification.ActorID != receiverID || got.Verification.Source == nil || got.Verification.Source.ConversationID != peer.ID || got.Verification.Source.RunID != reviewRun.ID || got.Verification.Checks[0].Method != "agent" || got.Task == nil || got.Task.ExecutionRunID != run.ID || got.ExecutionSubject.UserID != receiverID {
		t.Fatal("actual Agent review replaced the original execution or review identity", got)
	}
	var reviewResult sdk.ConversationToolResult
	reviewer.mu.Lock()
	for _, request := range reviewer.requests {
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolCallID == "issuer-review" {
				if err := json.Unmarshal([]byte(message.Content), &reviewResult); err != nil {
					reviewer.mu.Unlock()
					t.Fatal(err)
				}
			}
		}
	}
	reviewer.mu.Unlock()
	var reviewReceipt sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(reviewResult.Content, &reviewReceipt); err != nil || reviewResult.Status != "completed" || !reviewReceipt.ManagementOnly || reviewReceipt.TaskID != "" || reviewReceipt.ConversationID != "" || reviewReceipt.SourceConversationID != "" || reviewReceipt.Delivery != nil || reviewReceipt.Verification != nil {
		t.Fatal("Agent management receipt exposed private records", reviewReceipt, err)
	}
	review := sdk.ConversationDelegationUpdate{ClientID: "accept", ExpectedRevision: got.Revision, Action: "accept_delivery", Reason: "认可核对结论", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: got.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "已核对接收方提交的结论"}}}}
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(review), 200)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	receiver.handler = open()
	issuer.handler = receiver.handler
	receiver.login("admin@example.com", changed)
	issuer.login("system_administrator@example.com", changed)
	if after := read(issuer, d.ID); after.Status != "accepted_delivery" || after.ExecutionSubject.UserID != receiverID {
		t.Fatal("restart lost accepted delegation", after)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	model.mu.Lock()
	model.entered, model.release = entered, release
	model.mu.Unlock()
	pending := create("revoke-binding")
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("receiver never entered model")
	}
	mode = "caller"
	config.ClientID = "stop-owner-execution"
	receiver.call("PUT", "/agent/agents/"+agent.ID, accountJSON(config), 200)
	close(release)
	stopped := wait(pending)
	if stopped.Status == "completed" {
		t.Fatal("revoked execution binding still completed", stopped)
	}
	for _, step := range stopped.Steps {
		for _, call := range step.Calls {
			if call.Name == "todo_list" && call.Status == "completed" {
				t.Fatal("tool ran after binding revocation", call)
			}
		}
	}
	if os.Getenv("AGENT_EXECUTION_BROWSER") == "1" {
		verifyDelegationExecutionBrowser(t, host, options)
	}
	currentAgents := accountDecode[sdk.ConversationAgentPage](t, receiver.call("GET", "/agent/agents", "", 200))
	currentAgent := sdk.ConversationAgent{}
	for _, current := range currentAgents.Items {
		if current.ID == agent.ID {
			currentAgent = current
		}
	}
	if currentAgent.ID == "" {
		t.Fatal("current execution agent missing from directory")
	}
	mode = "owner"
	config.ExpectedRevision = currentAgent.Revision
	config.ClientID = "restore-before-subject-exit"
	receiver.call("PUT", "/agent/agents/"+agent.ID, accountJSON(config), 200)
	exitEntered, exitRelease := make(chan struct{}), make(chan struct{})
	model.mu.Lock()
	model.entered, model.release = exitEntered, exitRelease
	model.mu.Unlock()
	exiting := create("subject-exit")
	select {
	case <-exitEntered:
	case <-time.After(30 * time.Second):
		t.Fatal("subject-exit worker never entered model")
	}
	subjectBinding, ok := host.Agent.(agentlifecycle.SubjectBinding)
	if !ok {
		t.Fatal("embedded Agent omitted subject lifecycle ports")
	}
	erased := false
	for _, owner := range subjectBinding.LifecycleSubjectHandlers() {
		if owner.Owner(t.Context()) != "agent" {
			continue
		}
		if _, err := owner.EraseSubjectForRequest(t.Context(), "actual-executor-exit", options.WorkspaceID, receiverID, nil); err != nil {
			t.Fatal(err)
		}
		erased = true
	}
	if !erased {
		t.Fatal("Agent subject handler missing")
	}
	close(exitRelease)
	for _, id := range []string{d.ID, exiting.ID} {
		after := read(issuer, id)
		if !after.SubjectExited || !after.ContractOmitted || after.Task != nil || after.Delivery != nil || after.Access == nil || after.Access.ExecutionRead || after.Access.DeliveryRead || after.Access.Receive {
			t.Fatal("real HTTP exposed deleted execution or lost exit metadata", after)
		}
		if id == d.ID && after.Status != "accepted_delivery" {
			t.Fatal("exit rewrote original accepted status", after)
		}
		if id == exiting.ID && after.Status != "cancelled" {
			t.Fatal("exit left delegated worker active", after)
		}
	}
	issuer.call("GET", "/agent/conversations/"+exiting.SourceConversationID, "", 200)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	receiver.handler = open()
	issuer.handler = receiver.handler
	issuer.login("system_administrator@example.com", changed)
	if after := read(issuer, exiting.ID); !after.SubjectExited || after.Status != "cancelled" {
		t.Fatal("restart lost subject exit metadata", after)
	}
	if os.Getenv("AGENT_SUBJECT_EXIT_BROWSER") == "1" {
		verifyDelegationBrowser(t, host, options, "subject-exit.browser.mjs")
	}
}

func verifyDelegationExecutionBrowser(t *testing.T, host *Host, options Options) {
	t.Helper()
	verifyDelegationBrowser(t, host, options, "delegation-execution.browser.mjs")
}

func verifyDelegationBrowser(t *testing.T, host *Host, options Options, script string) {
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
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests", script))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
}
