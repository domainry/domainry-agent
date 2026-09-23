package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	identityhttp "github.com/domainry/domainry-identity-sdk/httpapi"
)

type transferClockHTTPModel struct {
	peerWebModel
	entered chan struct{}
	release chan struct{}
}

func (m *transferClockHTTPModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, _ func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
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
		if message.Role == "tool" && message.ToolCallID == "clock" {
			return sdk.ConversationStepResult{FinishReason: "stop", Message: sdk.ConversationStepMessage{Role: "assistant", Content: "Original clock checked; remaining work completed."}}, nil
		}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "clock", Name: "time_now", Arguments: `{}`}}}}, nil
}

func TestCrossSubjectTransferHTTPAdmitsIndependentReceiverAndKeepsHistoryAfterRestart(t *testing.T) {
	exerciseCrossSubjectTransferHTTP(t, false)
}

func TestParticipantTransferHTTPKeepsActualActorOriginalSourcesAndIndependentExecution(t *testing.T) {
	exerciseCrossSubjectTransferHTTP(t, true)
}

func TestParticipantDependencyTransferHTTPRechecksUpstreamAudienceDuringExecution(t *testing.T) {
	exerciseCrossSubjectTransferHTTP(t, true, true)
}

func waitTransferClockHTTP(t *testing.T, b *browser, id string) (sdk.ConversationDelegationDetail, sdk.ConversationRun) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		current := accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", "/agent/delegations/"+id, "", 200))
		if current.Task != nil && current.Task.ExecutionRunID != "" {
			run := accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, "", 200))
			if run.Terminal() {
				return accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", "/agent/delegations/"+id, "", 200)), run
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("receiving dependency execution did not settle")
	return sdk.ConversationDelegationDetail{}, sdk.ConversationRun{}
}

func exerciseCrossSubjectTransferHTTP(t *testing.T, managed bool, dependencyMode ...bool) {
	dependent := len(dependencyMode) > 0 && dependencyMode[0]
	const initial, changed = "Transfer-Initial!26", "Transfer-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "subject-transfer-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "subject-transfer-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	firstModel := &transferClockHTTPModel{peerWebModel: peerWebModel{modelKey: "first-clock"}}
	secondModel := &transferClockHTTPModel{peerWebModel: peerWebModel{modelKey: "second-clock"}}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "transfer.db"), RuntimeID: "transfer-runtime", WorkspaceID: "transfer-workspace", ApplicationKey: "transfer-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"first-clock": firstModel, "second-clock": secondModel}, Poll: 5 * time.Millisecond}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("transfer")}}})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	first := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	issuer := &browser{t: t, handler: first.handler, cookies: map[string]*http.Cookie{}}
	first.login("admin@example.com", initial)
	first.changePassword(initial, changed)
	issuer.login("system_administrator@example.com", initial)
	issuer.changePassword(initial, changed)
	firstID, issuerID := first.readSession()["user_id"].(string), issuer.readSession()["user_id"].(string)
	grant := func(user string, professional bool) {
		t.Helper()
		mutateTestRolePermissions(t, host, first, func(previous []identity.ProjectRolePermission) []identity.ProjectRolePermission {
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
					if tool.Key == "time_now" {
						out = append(out, identity.ProjectRolePermission{PermissionKey: tool.ActionKey, DataScope: identity.DataScopeOwner})
					}
				}
			}
			return out
		}, user)
	}
	grant(firstID, true)
	grant(issuerID, false)
	// Create the third account through the real Identity HTTP module.
	mux := http.NewServeMux()
	for _, adapter := range host.Identity.(identityhttp.Provider).HTTPAdapters() {
		for _, route := range adapter.Routes() {
			mux.Handle(route.Pattern(), adapter.Handler())
		}
	}
	identityCall := func(method, path, body, hash string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+first.cookies["domainry_agent_access"].Value)
		r.Header.Set("X-Workspace-ID", options.WorkspaceID)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Expected-Schema-Hash", hash)
		r.Header.Set("Idempotency-Key", "transfer-"+method+path)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("Identity %s %s: %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	const secondID, secondEmail, secondPassword = "transfer-receiver", "transfer-receiver@example.com", "Transfer-Receiver!26"
	user := map[string]any{"id": secondID, "name": "独立接手人", "email": secondEmail, "status": "active"}
	created := accountDecode[struct {
		InitialPassword string `json:"initial_password"`
	}](t, identityCall("POST", "/identity/users", accountJSON(user), "", 201))
	roles, err := host.Identity.Projection().ListUserRoleAssignments(t.Context(), identity.UserRoleAssignmentQuery{UserID: identity.SubjectID(firstID)})
	if err != nil || len(roles) == 0 {
		t.Fatal(roles, err)
	}
	assignment := identityCall("GET", "/identity/users/"+secondID+"/role-assignments", "", "", 200)
	identityCall("PUT", "/identity/users/"+secondID+"/account-and-roles", accountJSON(map[string]any{"user": user, "assignments": []map[string]any{{"role_id": roles[0].RoleID}}}), assignment.Header().Get("X-Resource-Hash"), 200)
	second := &browser{t: t, handler: first.handler, cookies: map[string]*http.Cookie{}}
	second.login(secondEmail, created.InitialPassword)
	second.changePassword(created.InitialPassword, secondPassword)
	grant(secondID, true)
	configure := func(b *browser, client, name, model string) sdk.ConversationAgent {
		t.Helper()
		users, mode := []string{issuerID}, "owner"
		return accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: client, Name: name, Instructions: "Read the real clock and complete remaining work", Tools: []string{"time_now"}, SkillKeys: []string{}, ModelKey: model, Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}), 200))
	}
	firstAgent := configure(first, "first", "原执行 Agent", "first-clock")
	secondAgent := configure(second, "second", "接手 Agent", "second-clock")
	source := accountDecode[sdk.Conversation](t, issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "source", Title: "跨主体接手"}), 200))
	requirements := sdk.ConversationAgentRequirements{Tools: []string{"time_now"}}
	if managed {
		// The manager can consume this explicitly shared original issuer root,
		// but has no private conversation ownership or permission to republish it.
		run := accountDecode[sdk.ConversationRun](t, issuer.call("POST", "/agent/conversations/"+source.ID+"/messages", `{"client_message_id":"original-contract","message":"保留原资料的准确来源"}`, 202))
		waitPeerVerificationRun(t, issuer, "/agent/conversations/"+source.ID+"/runs/"+run.ID)
		requirements.Sources = []sdk.ConversationRunReference{{ConversationID: source.ID, RunID: run.ID, BeforeStep: 1}}
	}
	var upstream sdk.ConversationDelegationDetail
	var dependencies []sdk.ConversationDependencyInput
	if dependent {
		upstream = accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "upstream", ConversationID: source.ID, AgentID: firstAgent.ID, Purpose: "原始要求", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "原始范围", Deliverable: "范围核对", CompletionConditions: []string{"核对真实时钟"}}, Requirements: requirements}), 200))
		var run sdk.ConversationRun
		upstream, run = waitTransferClockHTTP(t, first, upstream.ID)
		if run.Status != "completed" {
			t.Fatal("upstream execution failed", run)
		}
		viewers := []sdk.ConversationDelegationParticipantInput{{UserID: secondID, Operations: []string{"view"}}}
		issuer.call("POST", "/agent/delegations/"+upstream.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "upstream-view", ExpectedRevision: upstream.Revision, Action: "set_participants", Participants: &viewers, Reason: "明确接手人读取上游要求"}), 200)
		dependencies = []sdk.ConversationDependencyInput{{DelegationID: upstream.ID, BriefVersion: 1, AgreementRevision: 1, Fields: []string{"goal"}}}
	}
	d := accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "work", ConversationID: source.ID, AgentID: firstAgent.ID, Purpose: "独立核对", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对原执行并完成剩余工作", Deliverable: "核对结果", CompletionConditions: []string{"核对真实时钟"}}, Requirements: requirements, Dependencies: dependencies}), 200))
	read := func(b *browser) sdk.ConversationDelegationDetail {
		t.Helper()
		return accountDecode[sdk.ConversationDelegationDetail](t, b.call("GET", "/agent/delegations/"+d.ID, "", 200))
	}
	wait := func(b *browser) (sdk.ConversationDelegationDetail, sdk.ConversationRun) {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			current := read(b)
			if current.Task != nil && current.Task.ExecutionRunID != "" {
				run := accountDecode[sdk.ConversationRun](t, b.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+current.Task.ExecutionRunID, "", 200))
				if run.Terminal() {
					return read(b), run
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("receiving execution did not settle")
		return sdk.ConversationDelegationDetail{}, sdk.ConversationRun{}
	}
	old, originalRun := wait(first)
	if originalRun.Status != "completed" || old.Task == nil {
		t.Fatal("original actual execution failed", originalRun)
	}
	participants := []sdk.ConversationDelegationParticipantInput{{UserID: firstID, Operations: []string{"view", "execution_read"}}, {UserID: secondID, Operations: []string{"view", "execution_read"}}}
	actor := issuer
	if managed {
		participants[1].Operations = append(participants[1].Operations, "manage")
		actor = second
	}
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "readers", ExpectedRevision: read(issuer).Revision, Action: "set_participants", Reason: "明确允许接手人读取原共享执行", Participants: &participants}), 200)
	transfer := sdk.ConversationDelegationUpdate{ClientID: "transfer", ExpectedRevision: read(issuer).Revision, Action: "transfer", Reason: "由独立接手人继续", Transfer: &sdk.ConversationDelegationTransfer{AgentID: secondAgent.ID, RemainingWork: "核对原时钟回执并完成剩余工作"}}
	actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 403)
	if after := read(issuer); after.TaskID != old.TaskID || after.Revision != transfer.ExpectedRevision {
		t.Fatal("unpublished transfer mutated the original work", after)
	}
	ref := sdk.ConversationRunReference{ConversationID: old.ConversationID, RunID: originalRun.ID}
	first.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "share-original", ExpectedRevision: read(first).Revision, Reference: ref, Reason: "原执行人明确共享本次运行"}), 200)
	second.call("POST", "/agent/delegations/"+d.ID+"/execution", accountJSON(ref), 200)
	grant(secondID, false)
	transfer.ExpectedRevision = read(issuer).Revision
	actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 409)
	grant(secondID, true)
	transfer.ExpectedRevision = read(issuer).Revision
	contractPublications := func() string {
		t.Helper()
		rows, err := host.db.QueryContext(t.Context(), `SELECT owner_key, release_id, payload_json FROM _agent_source_releases WHERE delegation_id = ?`, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		values := map[string]string{}
		for rows.Next() {
			var owner, id string
			var raw []byte
			if err := rows.Scan(&owner, &id, &raw); err != nil {
				t.Fatal(err)
			}
			var value struct {
				Purpose   string                     `json:"purpose"`
				Publisher *sdk.ConversationAuthority `json:"publisher"`
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if value.Purpose == "contract" {
				if value.Publisher == nil || value.Publisher.UserID != issuerID {
					t.Fatal("manager became publisher of issuer private roots", string(raw))
				}
				values[owner+":"+id] = string(raw)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if managed && len(values) == 0 {
			t.Fatal("managed transfer has no original contract publication")
		}
		raw, _ := json.Marshal(values)
		return string(raw)
	}
	originalPublications := ""
	if managed {
		originalPublications = contractPublications()
	}
	var next sdk.ConversationDelegationDetail
	if os.Getenv("AGENT_CROSS_TRANSFER_BROWSER") == "1" {
		t.Setenv("AGENT_UI_CONVERSATION", source.ID)
		t.Setenv("AGENT_UI_PASSWORD", changed)
		if managed {
			t.Setenv("AGENT_UI_TRANSFER_PARTICIPANT", "1")
		}
		if dependent {
			t.Setenv("AGENT_UI_TRANSFER_DEPENDENCY", upstream.ID)
		}
		verifyDelegationBrowser(t, host, options, "cross-subject-transfer.browser.mjs")
		if managed {
			raw, err := os.ReadFile(filepath.Join(os.Getenv("AGENT_UI_TEST_OUTPUT"), "report.json"))
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Transfer sdk.ConversationDelegationUpdate `json:"transfer_request"`
			}
			if err := json.Unmarshal(raw, &report); err != nil || report.Transfer.ClientID == "" {
				t.Fatal("browser transfer request missing", err)
			}
			transfer = report.Transfer
		}
		next = read(issuer)
	} else {
		next = accountDecode[sdk.ConversationDelegationDetail](t, actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 200))
		if managed {
			if !next.ManagementOnly || next.Task != nil || next.SourceConversationID != "" || next.ConversationID != "" {
				t.Fatal("manager transfer returned private execution contents", next)
			}
			next = read(issuer)
		}
	}
	if next.ExecutionSubject == nil || next.ExecutionSubject.UserID != secondID || next.TaskID == old.TaskID || len(next.Assignments) != 2 || next.Assignments[0].ConversationID != old.ConversationID {
		t.Fatal("transfer lost independent responsibility or history", next)
	}
	if dependent && (len(next.Dependencies) != 1 || next.Dependencies[0].DelegationID != upstream.ID || len(next.Dependencies[0].Fields) != 1 || next.Dependencies[0].Fields[0] != "goal") {
		t.Fatal("transfer dropped original dependency or selected field scope", next.Dependencies)
	}
	if next.Assignments[1].ActorID != actor.readSession()["user_id"].(string) {
		t.Fatal("transfer attributed the manager's action to the issuer", next.Assignments)
	}
	if managed {
		if contractPublications() != originalPublications {
			t.Fatal("manager transfer rewrote original contract publication")
		}
		second.call("GET", "/agent/conversations/"+source.ID, "", 404)
		// Exact retry returns a receipt, without admitting a third assignment.
		retry := accountDecode[sdk.ConversationDelegationDetail](t, actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 200))
		if !retry.ManagementOnly || retry.Task != nil || retry.SourceConversationID != "" || retry.ConversationID != "" {
			t.Fatal("transfer retry exposed private execution contents", retry)
		}
		participants[1].Operations = []string{"view", "execution_read"}
		issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "withdraw-manager", ExpectedRevision: read(issuer).Revision, Action: "set_participants", Participants: &participants, Reason: "移除额外管理范围"}), 200)
		actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 403)
		participants[1].Operations = append(participants[1].Operations, "manage")
		issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "restore-manager", ExpectedRevision: read(issuer).Revision, Action: "set_participants", Participants: &participants, Reason: "明确恢复管理范围"}), 200)
	}
	current, received := wait(second)
	if received.Status != "completed" || received.Agent == nil || received.Agent.DelegationRoleKey != secondAgent.DelegationRoleKey || current.Task == nil || current.Task.Budget.MaxSteps != old.Budget.MaxSteps-2 || current.Task.Budget.MaxToolCalls != old.Budget.MaxToolCalls-1 {
		t.Fatal("new receiver role or cumulative budget failed", current, received)
	}
	issuer.call("GET", "/agent/conversations/"+current.ConversationID+"/runs/"+received.ID, "", 404)
	second.call("GET", "/agent/conversations/"+old.ConversationID+"/runs/"+originalRun.ID, "", 404)
	first.call("GET", "/agent/conversations/"+old.ConversationID+"/runs/"+originalRun.ID, "", 200)
	var resultRef sdk.ConversationResultReference
	for _, step := range received.Steps {
		for _, call := range step.Calls {
			if call.Name == "time_now" && call.ResultReference != nil {
				resultRef = *call.ResultReference
			}
		}
	}
	if resultRef.CallID == "" {
		t.Fatal("missing actual replacement clock receipt")
	}
	condition := sdk.ConversationConditionAssessment{Condition: 0, Verdict: "met", Basis: "对照接手人的真实时钟回执", Receipts: []sdk.ConversationResultReference{resultRef}}
	delivery := sdk.ConversationDelegationUpdate{ClientID: "new-delivery", ExpectedRevision: current.Revision, Action: "deliver", Reason: "接手人提交真实结果", Delivery: &sdk.ConversationDelegationDelivery{BriefVersion: current.Brief.Version, AgreementRevision: current.AgreementRevision, Summary: "已核对原执行并完成剩余工作", Conditions: []sdk.ConversationConditionAssessment{condition}}}
	second.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(delivery), 200)
	current = read(issuer)
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "accept", ExpectedRevision: current.Revision, Action: "accept_delivery", Reason: "核对接手人的原结果", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: current.Verification.DeliveryDigest, Conditions: []sdk.ConversationConditionAssessment{condition}}}), 200)
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	issuer.handler = open()
	first.handler, second.handler = issuer.handler, issuer.handler
	issuer.login("system_administrator@example.com", changed)
	first.login("admin@example.com", changed)
	second.login(secondEmail, secondPassword)
	after := read(issuer)
	if after.Status != "accepted_delivery" || after.ExecutionSubject.UserID != secondID || len(after.Assignments) != 2 || after.Assignments[0].ConversationID != old.ConversationID || after.Delivery == nil {
		t.Fatal("restart lost transferred acceptance or immutable original assignment", after)
	}
	if dependent {
		d = accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "dependency-withdraw-running", ConversationID: source.ID, AgentID: firstAgent.ID, Purpose: "检查上游撤权", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "采用准确原始范围", Deliverable: "范围核对", CompletionConditions: []string{"核对真实时钟"}}, Requirements: requirements, Dependencies: dependencies}), 200))
		old, originalRun = wait(first)
		issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "dependency-running-readers", ExpectedRevision: read(issuer).Revision, Action: "set_participants", Participants: &participants, Reason: "明确接手管理与执行阅读"}), 200)
		ref = sdk.ConversationRunReference{ConversationID: old.ConversationID, RunID: originalRun.ID}
		first.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "dependency-running-share", ExpectedRevision: read(first).Revision, Reference: ref, Reason: "原执行人明确共享将转交的执行"}), 200)
		entered, release := make(chan struct{}), make(chan struct{})
		secondModel.mu.Lock()
		beforeRequests := len(secondModel.requests)
		secondModel.entered, secondModel.release = entered, release
		secondModel.mu.Unlock()
		transfer.ClientID, transfer.ExpectedRevision, transfer.Dependencies = "dependency-running-transfer", read(issuer).Revision, &dependencies
		actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 200)
		select {
		case <-entered:
		case <-time.After(25 * time.Second):
			t.Fatal("replacement never entered the admitted dependency model request")
		}
		receiving := read(second)
		if receiving.Task == nil || receiving.Task.ExecutionRunID == "" || receiving.ConversationID == "" {
			t.Fatal("missing actual receiving run before audience withdrawal", receiving)
		}
		upstream = accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("GET", "/agent/delegations/"+upstream.ID, "", 200))
		empty := []sdk.ConversationDelegationParticipantInput{}
		issuer.call("POST", "/agent/delegations/"+upstream.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "dependency-withdraw", ExpectedRevision: upstream.Revision, Action: "set_participants", Participants: &empty, Reason: "撤回接手人上游查看范围"}), 200)
		close(release)
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			after = read(second)
			if after.Status == "failed" || after.Status == "cancelled" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if (after.Status != "failed" && after.Status != "cancelled") || !after.ContractOmitted || after.Task != nil {
			t.Fatal("withdrawn upstream audience continued execution or exposed task", after)
		}
		secondModel.mu.Lock()
		newRequests := append([]sdk.ConversationStepRequest(nil), secondModel.requests[beforeRequests:]...)
		secondModel.mu.Unlock()
		if len(newRequests) != 1 {
			t.Fatal("model continued after upstream audience withdrawal", len(newRequests))
		}
		var attempts int
		if err := host.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _agent_run_steps WHERE record_kind = 'tool_call' AND conversation_id = ? AND run_id = ?`, receiving.ConversationID, receiving.Task.ExecutionRunID).Scan(&attempts); err != nil || attempts != 0 {
			t.Fatal("new clock invocation entered the original ledger after upstream withdrawal", attempts, err)
		}
		return
	}
	first.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "withdraw-original-after-transfer", ExpectedRevision: read(first).Revision, Reference: ref, Withdraw: true, Reason: "原执行人撤回仍属自己的发布"}), 200)
	second.call("POST", "/agent/delegations/"+d.ID+"/execution", accountJSON(ref), 403)
	// Saved handoff and delivery remain traceable but cannot reuse withdrawn data.
	after = read(issuer)
	if after.Status != "accepted_delivery" || !after.ContractOmitted {
		t.Fatal("withdrawal did not hide inherited handoff without changing acceptance", after)
	}
	// A separate admitted transfer is held inside its first real model request.
	// Withdrawing the old source must stop work before the new clock tool runs.
	source = accountDecode[sdk.Conversation](t, issuer.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "source-withdraw-running"}), 200))
	d = accountDecode[sdk.ConversationDelegationDetail](t, issuer.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "withdraw-running", ConversationID: source.ID, AgentID: firstAgent.ID, Purpose: "停止失效资料工作", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对新的原执行", Deliverable: "核对结果", CompletionConditions: []string{"核对真实时钟"}}, Requirements: sdk.ConversationAgentRequirements{Tools: []string{"time_now"}}}), 200))
	old, originalRun = wait(first)
	issuer.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(sdk.ConversationDelegationUpdate{ClientID: "running-readers", ExpectedRevision: read(issuer).Revision, Action: "set_participants", Reason: "明确原执行和接手人的读取范围", Participants: &participants}), 200)
	ref = sdk.ConversationRunReference{ConversationID: old.ConversationID, RunID: originalRun.ID}
	first.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "running-share", ExpectedRevision: read(first).Revision, Reference: ref, Reason: "明确共享将转交的原执行"}), 200)
	entered, release := make(chan struct{}), make(chan struct{})
	secondModel.mu.Lock()
	beforeRequests := len(secondModel.requests)
	secondModel.entered, secondModel.release = entered, release
	secondModel.mu.Unlock()
	transfer.ClientID, transfer.ExpectedRevision = "running-transfer", read(issuer).Revision
	actor.call("POST", "/agent/delegations/"+d.ID+"/decisions", accountJSON(transfer), 200)
	select {
	case <-entered:
	case <-time.After(25 * time.Second):
		t.Fatal("new subject never entered the admitted model request")
	}
	first.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "running-withdraw", ExpectedRevision: read(first).Revision, Reference: ref, Withdraw: true, Reason: "原执行人撤回接手人正在使用的原资料"}), 200)
	close(release)
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		after = read(second)
		if after.Status == "failed" || after.Status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if (after.Status != "failed" && after.Status != "cancelled") || !after.ContractOmitted || after.Task != nil {
		t.Fatal("withdrawn handoff continued execution or disclosed task content", after)
	}
	secondModel.mu.Lock()
	newRequests := append([]sdk.ConversationStepRequest(nil), secondModel.requests[beforeRequests:]...)
	secondModel.mu.Unlock()
	if len(newRequests) != 1 {
		t.Fatal("model continued after handoff withdrawal", len(newRequests))
	}
	for _, message := range newRequests[0].Messages {
		if message.Role == "tool" && message.ToolCallID == "clock" {
			t.Fatal("new original clock tool ran before revoked source was checked")
		}
	}
}
