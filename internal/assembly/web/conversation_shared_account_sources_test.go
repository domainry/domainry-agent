package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	identity "github.com/domainry/domainry-identity-sdk"
	integration "github.com/domainry/domainry-integration-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

// Only model-visible messages select calls. Actual Identity, Integration,
// Connectors, Tools, Agent workers and their separate databases execute them.
type sharedAccountIssuerModel struct{ peerWebModel }

type sharedAccountDeliveryModel struct{ peerReceiptDeliveryModel }

func (m *sharedAccountDeliveryModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	result, err := m.peerReceiptDeliveryModel.StreamConversationStep(ctx, in, emit)
	if err == nil && len(result.Message.ToolCalls) == 1 && result.Message.ToolCalls[0].Name == "calendar_event_update" {
		result.Message.ToolCalls[0], err = sharedAccountObservedCalendarUpdate(in, result.Message.ToolCalls[0])
	}
	if err != nil || len(result.Message.ToolCalls) != 1 || result.Message.ToolCalls[0].ID != "deliver" {
		return result, err
	}
	var args struct {
		ID     string                           `json:"id"`
		Update sdk.ConversationDelegationUpdate `json:"update"`
	}
	if err := json.Unmarshal([]byte(result.Message.ToolCalls[0].Arguments), &args); err != nil {
		return sdk.ConversationStepResult{}, err
	}
	if args.Update.Delivery == nil || len(args.Update.Delivery.Conditions) != 1 {
		return result, nil
	}
	conditions := []sdk.ConversationConditionAssessment{}
	for index, receipt := range args.Update.Delivery.Conditions[0].Receipts {
		conditions = append(conditions, sdk.ConversationConditionAssessment{Condition: index, Verdict: "met", Basis: "Retained the exact original tool receipt", Receipts: []sdk.ConversationResultReference{receipt}})
	}
	args.Update.Delivery.Conditions = conditions
	result.Message.ToolCalls[0].Arguments = accountJSON(map[string]any{"id": args.ID, "update": map[string]any{"expected_revision": args.Update.ExpectedRevision, "action": args.Update.Action, "reason": args.Update.Reason, "delivery": args.Update.Delivery}})
	return result, nil
}

func (m *sharedAccountIssuerModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	if result, handled := executionToolsModelStep(in); handled {
		return result, nil
	}
	for _, message := range in.Messages {
		if message.Role == "tool" && message.ToolCallID == "shared-account-dispatch" {
			return m.peerWebModel.StreamConversationStep(ctx, in, emit)
		}
	}
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Shared account dispatch fixture:\n") {
			return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "shared-account-dispatch", Name: "agent_delegate", Arguments: strings.TrimPrefix(message.Content, "Shared account dispatch fixture:\n")}}}}, nil
		}
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

// Hold the first real business HTTP operation after the Agent has durably
// marked the tool running. OAuth is handled by accountTransport separately.
type sharedAccountPendingTransport struct {
	connector.Transport
	entered, released chan struct{}
	once              sync.Once
	enteredAt         atomic.Int64
}

func (tr *sharedAccountPendingTransport) RoundTripHTTP(ctx context.Context, in connector.HTTPRequest) (connector.HTTPResponse, error) {
	held := false
	tr.once.Do(func() { held = true; tr.enteredAt.Store(time.Now().UnixNano()); close(tr.entered) })
	if held {
		select {
		case <-tr.released:
		case <-ctx.Done():
			return connector.HTTPResponse{}, ctx.Err()
		}
	}
	return tr.Transport.RoundTripHTTP(ctx, in)
}

func TestCrossUserWorkspaceMailSourcesAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "mail")
}

func TestCrossUserWorkspaceCalendarSourcesAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "calendar")
}

func TestCrossUserWorkspaceWebSourcesAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "web")
}

func TestCrossUserWorkspaceMailWriteReceiptsAutomaticDeliveryAndSharedExecution(t *testing.T) {
	verifySharedAccountSources(t, "mail-write")
}

func verifySharedAccountSources(t *testing.T, family string) {
	t.Helper()
	var f *accountFixture
	var vendorCalls func() int32
	var writeCounts func() map[string]int
	writeEffect := ""
	var definitions []sdk.ConversationToolDefinition
	var connectWeb func() integration.ConnectionAccount
	switch family {
	case "mail":
		fixture := newMailProductFixture(t)
		f, vendorCalls, definitions = fixture.accountFixture, fixture.vendorCalls.Load, toolmodule.MailDefinitions()
	case "calendar":
		fixture := newCalendarProductFixture(t)
		f, vendorCalls, definitions = fixture.accountFixture, fixture.vendorCalls.Load, toolmodule.CalendarDefinitions()
	case "web":
		fixture := newWebProductFixture(t)
		f, vendorCalls, definitions, connectWeb = fixture.accountFixture, fixture.vendorCalls.Load, toolmodule.WebDefinitions(), fixture.connect
	case "mail-write", "mail-reply-write", "calendar-create-write", "calendar-update-write":
		fixture := newAccountWriteProductFixtureWithMailText(t, "共享发送原正文\n收件人、抄送、密送及末尾内容均须保持。")
		f, vendorCalls, definitions = fixture.accountFixture, fixture.requests.Load, toolmodule.MailWriteDefinitions()
		writeCounts = fixture.counts
		writeEffect = "mail_send"
		if family == "mail-reply-write" {
			writeEffect = "mail_reply"
		} else if strings.HasPrefix(family, "calendar-") {
			definitions = toolmodule.CalendarWriteDefinitions()
			writeEffect = "calendar_create"
			if family == "calendar-update-write" {
				writeEffect = "calendar_update"
			}
		}
	default:
		t.Fatal("unknown fixture family", family)
	}
	f.close()
	worker := &sharedAccountDeliveryModel{peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "shared-" + family}, summary: "Original workspace " + family + " receipts verified"}}
	personalWorker := &sharedAccountDeliveryModel{peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "personal-" + family}, summary: "Original personal " + family + " receipts verified"}}
	f.options.Agent.ConversationURL = ""
	f.options.Agent.ConversationProvider = &sharedAccountIssuerModel{}
	f.options.Agent.ConversationOptions.AgentModels = map[string]sdk.ConversationModel{worker.modelKey: worker, personalWorker.modelKey: personalWorker}
	gate := &sharedAccountPendingTransport{Transport: f.providerTransport, entered: make(chan struct{}), released: make(chan struct{})}
	f.providerTransport = gate
	released := false
	defer func() {
		if !released {
			close(gate.released)
		}
	}()
	f.open()
	files := fstest.MapFS{"index.html": {Data: []byte("shared account receipts")}, "oauth-callback.html": {Data: []byte("callback")}}
	owner := &browser{t: t, handler: f.boundary("http://127.0.0.1:8091", files), cookies: map[string]*http.Cookie{}}
	reader := &browser{t: t, handler: owner.handler, cookies: map[string]*http.Cookie{}}
	owner.login("admin@example.com", accountInitial)
	owner.changePassword(accountInitial, accountChanged)
	reader.login("system_administrator@example.com", accountInitial)
	reader.changePassword(accountInitial, accountChanged)
	ownerID, readerID := owner.readSession()["user_id"].(string), reader.readSession()["user_id"].(string)
	owner.call("POST", "/app/product/account-setup", `{}`, 200)
	owner.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	f.grant(owner, ownerID, "", true)
	grant := func(user string, execute bool) {
		t.Helper()
		grantSharedAccountPolicy(t, f, owner, user, definitions, execute)
	}
	grant(ownerID, true)
	grant(readerID, false)
	var account integration.ConnectionAccount
	if connectWeb != nil {
		account = connectWeb()
	} else {
		account = connectSharedSourceOAuth(t, f, owner, family, integration.ConnectionAccountScopeWorkspace)
	}
	if account.Scope != integration.ConnectionAccountScopeWorkspace || account.OwnerUserID != "" {
		t.Fatal("workspace fixture did not use the real account owner contract", account)
	}
	worker.sourceCalls = sharedAccountSourceCalls(family, account.Key)
	if writeEffect != "" {
		worker.sourceCalls = sharedAccountWriteSourceCalls(family, account)
	}
	mode, users := "owner", []string{readerID}
	tools := []string{"delegation_get", "delegation_update"}
	for _, call := range worker.sourceCalls {
		tools = append(tools, call.Name)
	}
	agent := accountDecode[sdk.ConversationAgent](t, owner.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "shared-" + family, Name: "Shared " + family, Instructions: "Read actual original sources and deliver their exact references", Tools: tools, SkillKeys: []string{}, ModelKey: worker.modelKey, Enabled: true, MaxConcurrent: 1, SharedWithUserIDs: &users, DelegationExecution: &mode}), 200))
	source := accountDecode[sdk.Conversation](t, reader.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "dispatch-" + family, Title: "Cross-user account source dispatch"}), 200))
	if writeEffect != "" {
		matches := accountDecode[sdk.ConversationAgentMatchPage](t, reader.call("POST", "/agent/agents/matches", accountJSON(sdk.ConversationAgentMatchRequest{ConversationID: source.ID, Requirements: sdk.ConversationAgentRequirements{Tools: tools}}), 200))
		for _, candidate := range matches.Items {
			if candidate.AgentID == agent.ID && !candidate.CanAccept {
				t.Fatal("actual write receiver was unavailable", candidate)
			}
		}
	}
	brief := sdk.ConversationTaskBrief{Version: 1, Goal: "Read the original workspace " + family + " sources", Deliverable: "Original source receipts", Constraints: []string{}, Assumptions: []string{}, CompletionConditions: []string{"Retain the exact successful original source receipts"}}
	brief.CompletionConditions = []string{}
	for index, call := range worker.sourceCalls {
		brief.CompletionConditions = append(brief.CompletionConditions, "Retain the original "+call.Name+" receipt")
		rule := sdk.ConversationCompletionRule{Condition: index, Kind: "receipt", Tool: call.Name, ResultSchema: json.RawMessage(`{"type":"object"}`)}
		if call.Name == "mail_send" || call.Name == "mail_reply" {
			rule.Completion = "accepted"
		}
		brief.VerificationRules = append(brief.VerificationRules, rule)
	}
	budget := sdk.ConversationTaskBudget{MaxSteps: 12, MaxToolCalls: 12, MaxOutputBytes: 8192, TimeoutSeconds: 60}
	sent := accountDecode[sdk.ConversationRun](t, reader.call("POST", "/agent/conversations/"+source.ID+"/messages", accountJSON(sdk.ConversationSend{ClientMessageID: "dispatch-" + family, Message: "Shared account dispatch fixture:\n" + accountJSON(map[string]any{"agent_id": agent.ID, "purpose": "Read separately authorized workspace sources", "brief": brief, "budget": budget, "input": "Read actual sources and submit original receipts", "requirements": sdk.ConversationAgentRequirements{Tools: tools}})}), 202))
	read := func(b *browser, id string) sdk.ConversationDelegationDetail {
		t.Helper()
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", "/agent/delegations/"+id, "", 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	var d sdk.ConversationDelegationDetail
	receiverManagementGranted := false
	started := time.Now()
	workerState := ""
	workerObserved := false
	publicationPublished := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		run := accountDecode[sdk.ConversationRun](t, reader.call("GET", "/agent/conversations/"+source.ID+"/runs/"+sent.ID, "", 200))
		approveSharedAccountRun(t, reader, source.ID, run)
		page := accountDecode[sdk.ConversationDelegationPage](t, reader.call("GET", "/agent/delegations", "", 200))
		if len(page.Items) == 1 {
			d = read(owner, page.Items[0].ID)
			if d.Task != nil && d.Task.Status == "failed" {
				t.Fatal("source invocation failed before the pending boundary", d.Task.ErrorCode)
			}
			if d.Task != nil && d.Task.ExecutionRunID != "" {
				if !workerObserved {
					// Dispatch and the admitted worker are distinct runs. Observe
					// each within its own window; leave persisted budgets unchanged.
					workerObserved = true
					deadline = time.Now().Add(time.Duration(budget.TimeoutSeconds) * time.Second)
				}
				if writeEffect != "" {
					if !receiverManagementGranted {
						current := read(reader, d.ID)
						participants := []sdk.ConversationDelegationParticipantInput{{UserID: ownerID, Operations: []string{"view", "manage"}}}
						updateSharedAccountDelegation(t, reader, "/agent/delegations/"+d.ID, func() sdk.ConversationDelegationDetail { return read(reader, d.ID) }, sdk.ConversationDelegationUpdate{ClientID: "receiver-confirmation-control", ExpectedRevision: current.Revision, Action: "set_participants", Reason: "Allow the actual receiver to approve its exact operation", Participants: &participants})
						receiverManagementGranted = true
					}
					workerRun := accountDecode[sdk.ConversationRun](t, owner.call("GET", "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "", 200))
					state := workerRun.Status + ":" + workerRun.ErrorCode
					if workerRun.Interaction != nil {
						state += ":" + workerRun.Interaction.Kind + ":" + workerRun.Interaction.Status
					}
					if state != workerState {
						t.Logf("%s worker at %s: %s, %d recorded steps", family, time.Since(started), state, len(workerRun.Steps))
						workerState = state
					}
					if i := workerRun.Interaction; i != nil && i.Status == "pending" {
						// Publish while the worker is durably waiting for the account's
						// confirmation. UI work must not hold a started vendor call.
						if !publicationPublished {
							publication := sdk.ConversationExecutionShare{ClientID: "pending-share", Reference: sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}, ExpectedRevision: read(owner, d.ID).Revision, Reason: "Share the exact admitted execution"}
							owner.call("POST", "/agent/delegations/"+d.ID+"/execution-publications", accountJSON(publication), 200)
							publicationPublished = true
						}
						approveSharedAccountRun(t, owner, d.ConversationID, workerRun)
						deadline = time.Now().Add(time.Duration(budget.TimeoutSeconds) * time.Second)
					}
				}
				select {
				case <-gate.entered:
					goto pending
				default:
				}
			}
		}
		if run.Terminal() && len(page.Items) == 0 {
			t.Fatal("actual agent_delegate did not admit the owner worker", run.Status, run.ErrorCode, run.Steps)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("real source invocation did not reach an observable running boundary", "elapsed", time.Since(started), "worker", workerState, "gate_entered_unix_nanos", gate.enteredAt.Load())
pending:
	if d.ExecutionSubject == nil || d.ExecutionSubject.UserID != ownerID {
		t.Fatal("worker borrowed the reader's execution identity", d.ExecutionSubject)
	}
	path := "/agent/delegations/" + d.ID
	runRef := sdk.ConversationRunReference{ConversationID: d.ConversationID, RunID: d.Task.ExecutionRunID}
	publication := sdk.ConversationExecutionShare{ClientID: "pending-share", Reference: runRef, ExpectedRevision: read(owner, d.ID).Revision, Reason: "Share the exact running execution"}
	if !publicationPublished {
		owner.call("POST", path+"/execution-publications", accountJSON(publication), 200)
	}
	observed := accountDecode[sdk.ConversationRun](t, reader.call("POST", path+"/execution", accountJSON(runRef), 200))
	running := false
	for _, step := range observed.Steps {
		for _, call := range step.Calls {
			if call.Status == "running" {
				running = true
				if call.AccessError != "execution_result_not_readable" || call.Arguments != "" || call.ResultPreview != "" || call.ResultReference != nil || call.Confirmation != nil || call.Authorization != nil || call.ResourceID != "" || len(call.Citations) != 0 {
					t.Fatal("pending source exposed an unverified payload", call)
				}
			}
		}
	}
	if !running || observed.Interaction != nil || observed.WriteScope != nil || observed.DraftText != "" {
		t.Fatal("shared pending execution did not preserve its public state", observed)
	}
	close(gate.released)
	released = true
	deadline = time.Now().Add(time.Duration(budget.TimeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		d = read(owner, d.ID)
		if writeEffect != "" && d.Task != nil && d.Task.ExecutionRunID != "" {
			workerRun := accountDecode[sdk.ConversationRun](t, owner.call("GET", "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "", 200))
			approveSharedAccountRun(t, owner, d.ConversationID, workerRun)
		}
		if d.Task != nil && d.Task.Status == "completed" && d.Status == "delivered" {
			break
		}
		if d.Task != nil && d.Task.Status == "failed" {
			t.Fatal("actual source worker failed", d.Task.ErrorCode, d)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d.Delivery == nil || len(d.Delivery.Conditions) != len(worker.sourceCalls) {
		t.Fatal("automatic delivery lost original sources", d)
	}
	original := map[sdk.ConversationResultReference]string{}
	for _, condition := range d.Delivery.Conditions {
		if len(condition.Receipts) != 1 {
			t.Fatal("automatic delivery mixed different source types in one condition", condition)
		}
		reference := condition.Receipts[0]
		original[reference] = accountJSON(readReleasedResult(t, owner, d.ID, 0, reference))
	}
	wantBody := map[string]string{"mail": mailFixtureBody, "calendar": "CALENDAR-BODY：讨论实际议程与预算。", "web": webFixtureBody, "mail-write": "sent-1", "mail-reply-write": "original-thread", "calendar-create-write": "created-version", "calendar-update-write": "version-2"}[family]
	foundBody := false
	for _, text := range original {
		foundBody = foundBody || strings.Contains(text, wantBody)
	}
	if !foundBody || vendorCalls() == 0 {
		t.Fatal("delivery did not use the real family connector body", family)
	}
	effects := vendorCalls()
	if writeCounts != nil {
		counts := writeCounts()
		if len(counts) != 1 || counts[writeEffect] != 1 {
			t.Fatal("original write operation was not accepted exactly once", counts)
		}
	}
	grant(ownerID, false)
	assertReadable := func() {
		t.Helper()
		current := read(reader, d.ID)
		if current.Delivery == nil || current.Verification == nil || current.Delivery.Summary != worker.summary {
			t.Fatal("source reading still required tool execution", current)
		}
		for reference, text := range original {
			if actual := accountJSON(readReleasedResult(t, reader, d.ID, 0, reference)); actual != text {
				t.Fatal("shared original delivery receipt changed", reference)
			}
			if actual := readSharedAccountResult(t, reader, path+"/execution-result", reference); actual != text {
				t.Fatal("shared original execution receipt changed", reference)
			}
		}
		run := accountDecode[sdk.ConversationRun](t, reader.call("POST", path+"/execution", accountJSON(runRef), 200))
		if run.Status != "completed" || run.Interaction != nil || run.WriteScope != nil {
			t.Fatal("execution reading exposed controls or lost the original outcome", run)
		}
		reader.call("GET", "/agent/conversations/"+runRef.ConversationID, "", 404)
		reader.call("GET", "/agent/conversations/"+runRef.ConversationID+"/runs/"+runRef.RunID, "", 404)
		reader.call("POST", "/agent/conversations/"+runRef.ConversationID+"/runs/"+runRef.RunID+"/cancel", `{}`, 404)
		if vendorCalls() != effects {
			t.Fatal("reading or permission changes fetched the original source again")
		}
	}
	assertReadable()
	current := read(reader, d.ID)
	updateSharedAccountDelegation(t, reader, path, func() sdk.ConversationDelegationDetail { return read(reader, d.ID) }, sdk.ConversationDelegationUpdate{ClientID: "accept-original", ExpectedRevision: current.Revision, Action: "accept_delivery", Reason: "Compared each original receipt", Review: &sdk.ConversationDeliveryReview{DeliveryDigest: current.Verification.DeliveryDigest}})
	f.close()
	f.open()
	owner.handler = f.boundary("http://127.0.0.1:8091", files)
	reader.handler = owner.handler
	owner.login("admin@example.com", accountChanged)
	reader.login("system_administrator@example.com", accountChanged)
	assertReadable()
	if family == "mail" || family == "calendar" {
		grant(ownerID, true)
		personalAccount := connectSharedSourceOAuth(t, f, owner, family, integration.ConnectionAccountScopePersonal)
		if personalAccount.OwnerUserID != ownerID || personalAccount.Scope != integration.ConnectionAccountScopePersonal {
			t.Fatal("personal fixture did not retain its actual owner", personalAccount)
		}
		calls := sharedAccountSourceCalls(family, personalAccount.Key)
		for _, call := range calls {
			if call.ID == "body" {
				personalWorker.sourceCalls = []sdk.ConversationToolCall{call}
			}
		}
		personalAgent := accountDecode[sdk.ConversationAgent](t, owner.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "personal-" + family, Name: "Personal " + family, Instructions: "Read the exact personal source", Tools: []string{personalWorker.sourceCalls[0].Name, "delegation_get", "delegation_update"}, SkillKeys: []string{}, ModelKey: personalWorker.modelKey, Enabled: true, MaxConcurrent: 1}), 200))
		personalSource := accountDecode[sdk.Conversation](t, owner.call("POST", "/agent/conversations", accountJSON(sdk.ConversationCreate{ClientID: "personal-source", Title: "Personal account boundary"}), 200))
		personalBrief := sdk.ConversationTaskBrief{Version: 1, Goal: "Read the exact personal source", Deliverable: "Original personal receipt", CompletionConditions: []string{"Retain the exact successful original receipt"}, VerificationRules: []sdk.ConversationCompletionRule{{Condition: 0, Kind: "receipt", Tool: personalWorker.sourceCalls[0].Name, ResultSchema: json.RawMessage(`{"type":"object"}`)}}}
		var personal sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(owner.call("POST", "/agent/delegations", accountJSON(sdk.ConversationDelegationCreate{ClientID: "personal-work", ConversationID: personalSource.ID, AgentID: personalAgent.ID, Purpose: "Verify personal account isolation", Brief: personalBrief, Budget: budget}), 200).Body.Bytes(), &personal); err != nil {
			t.Fatal(err)
		}
		personalDeadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(personalDeadline) {
			personal = read(owner, personal.ID)
			if personal.Status == "delivered" && personal.Task != nil && personal.Task.Status == "completed" {
				break
			}
			if personal.Task != nil && personal.Task.Status == "failed" {
				t.Fatal("personal owner could not execute the real source", personal.Task.ErrorCode)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if personal.Delivery == nil || len(personal.Delivery.Conditions) != 1 || len(personal.Delivery.Conditions[0].Receipts) != 1 {
			t.Fatal("personal owner did not receive the original body receipt", personal)
		}
		personalRef := personal.Delivery.Conditions[0].Receipts[0]
		if original := readReleasedResult(t, owner, personal.ID, 0, personalRef); !strings.Contains(string(original.Content), wantBody) {
			t.Fatal("personal receipt did not contain the real connector body")
		}
		participants := []sdk.ConversationDelegationParticipantInput{{UserID: readerID, Operations: []string{"view", "execution_read", "delivery_read"}}}
		personalPath := "/agent/delegations/" + personal.ID
		updateSharedAccountDelegation(t, owner, personalPath, func() sdk.ConversationDelegationDetail { return read(owner, personal.ID) }, sdk.ConversationDelegationUpdate{ClientID: "personal-participant", ExpectedRevision: personal.Revision, Action: "set_participants", Reason: "Collaboration scopes must not grant another user's personal account", Participants: &participants})
		personal = read(owner, personal.ID)
		personalRun := sdk.ConversationRunReference{ConversationID: personalRef.ConversationID, RunID: personalRef.RunID}
		owner.call("POST", personalPath+"/execution-publications", accountJSON(sdk.ConversationExecutionShare{ClientID: "personal-run-share", ExpectedRevision: personal.Revision, Reference: personalRun, Reason: "Explicit run publication must retain current personal account access"}), 200)
		if view := read(reader, personal.ID); view.Delivery != nil || view.Verification != nil {
			t.Fatal("participant scopes exposed another user's personal account body", view)
		}
		reader.call("POST", personalPath+"/execution", accountJSON(personalRun), 403)
		reader.call("POST", personalPath+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: personalRef}}), 403)
		reader.call("GET", "/integration/connection-accounts/"+personalAccount.Key, "", 403)
		grant(ownerID, false)
		effects = vendorCalls()
		assertReadable()
		t.Log(family + ": real personal owner body remains private despite explicit participant delivery/execution scopes and original run publication")
	}
	mutateTestRolePermissions(t, f.host, owner, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if p.PermissionKey != integration.ActionIntegrationConnectionAccountsRead {
				out = append(out, p)
			}
		}
		return out
	}, readerID)
	if current := read(reader, d.ID); current.Delivery != nil || current.Verification != nil {
		t.Fatal("withdrawn current account read retained the delivery", current)
	}
	reader.call("POST", path+"/execution", accountJSON(runRef), 503)
	grant(readerID, false)
	assertReadable()
	currentAccount := accountDecode[integration.ConnectionAccount](t, owner.call("GET", "/integration/connection-accounts/"+account.Key, "", 200))
	owner.call("POST", "/integration/connection-accounts/"+account.Key+"/revoke", accountJSON(map[string]string{"expected_updated_at": currentAccount.UpdatedAt}), 200)
	if current := read(reader, d.ID); current.Delivery != nil || current.Verification != nil {
		t.Fatal("revoked workspace account retained its delivery")
	}
	deniedStatus := 503
	if writeEffect != "" {
		deniedStatus = 403 // Receipt availability is scope-only; the exact owner rejects this account.
	}
	reader.call("POST", path+"/execution", accountJSON(runRef), deniedStatus)
	if vendorCalls() != effects {
		t.Fatal("revocation performed additional connector IO")
	}
	if writeCounts != nil && writeCounts()[writeEffect] != 1 {
		t.Fatal("original write operation was repeated while reading its receipt", writeCounts())
	}
	t.Logf("%s: actual cross-user agent_delegate confirmation and owner worker; %d original receipts; running payload hidden; original delivery/execution SHA and byte pages after both users' execution revocation and restart; private controls denied; reader data and workspace account revocation enforced; vendor HTTP remained %d", family, len(original), effects)
}

func connectSharedSourceOAuth(t *testing.T, f *accountFixture, owner *browser, family string, scope integration.ConnectionAccountScope) integration.ConnectionAccount {
	t.Helper()
	grant := mailReadScope
	if family == "calendar" {
		grant = accountScope
	} else if strings.HasPrefix(family, "calendar-") {
		grant = accountCalendarWriteScope
	} else if strings.HasSuffix(family, "-write") {
		grant = accountMailWriteScope
	}
	key := "shared-source-" + family + "-" + string(scope)
	owner.call("PUT", "/integration/oauth-applications/"+key, accountJSON(integration.OAuthApplicationInput{ConnectorKey: "google_workspace", ProviderKey: "google", Name: "Source sharing fixture", ClientID: "fixture-client", ClientSecret: "fixture-client-secret", RedirectURI: "http://127.0.0.1:8091/oauth/callback", Scopes: []string{grant}, Enabled: true}), 200)
	session := accountDecode[integration.OAuthAuthorizationSession](t, owner.call("POST", "/integration/oauth-authorizations", accountJSON(integration.OAuthAuthorizationInput{ApplicationKey: key, Scope: scope, Name: "Original source fixture", Scopes: []string{grant}}), 200))
	u, err := url.Parse(f.callback(session.AuthorizationURL, "connect"))
	if err != nil {
		t.Fatal(err)
	}
	done := accountDecode[integration.OAuthAuthorizationSession](t, owner.call("POST", "/integration/oauth-authorizations/callback", accountJSON(integration.OAuthAuthorizationCallback{State: u.Query().Get("state"), Code: u.Query().Get("code")}), 200))
	if done.Account == nil {
		t.Fatal("real Integration did not register the source account")
	}
	return *done.Account
}

func sharedAccountSourceCalls(family, account string) []sdk.ConversationToolCall {
	call := func(id, tool string, args any) sdk.ConversationToolCall {
		return sdk.ConversationToolCall{ID: id, Name: tool, Arguments: accountJSON(args)}
	}
	switch family {
	case "mail":
		return []sdk.ConversationToolCall{
			call("accounts", "mail_accounts", map[string]string{"operation": "mail_read"}),
			call("list", "mail_list", map[string]any{"account_key": account, "limit": 2}),
			call("search", "mail_search", map[string]any{"account_key": account, "query": "subject:预算", "query_syntax": "gmail", "limit": 2}),
			call("body", "mail_read", map[string]string{"account_key": account, "message_id": "mail-1"}),
		}
	case "calendar":
		window := map[string]string{"start": "2026-11-01T00:00:00-04:00", "end": "2026-11-02T00:00:00-05:00"}
		return []sdk.ConversationToolCall{
			call("accounts", "calendar_accounts", map[string]string{"operation": "calendar_event"}),
			call("list", "calendar_list", map[string]string{"account_key": account}),
			call("events", "calendar_events", map[string]any{"account_key": account, "calendar_id": "primary", "window": window, "time_zone": "America/New_York", "limit": 2}),
			call("body", "calendar_event", map[string]string{"account_key": account, "calendar_id": "primary", "event_id": "all-day", "time_zone": "America/New_York"}),
			call("availability", "calendar_availability", map[string]any{"account_key": account, "calendar_ids": []string{"primary", "secondary"}, "window": window, "time_zone": "America/New_York"}),
		}
	case "web":
		return []sdk.ConversationToolCall{
			call("search", "web_search", map[string]string{"query": webFixtureQuery}),
			call("body", "web_fetch", map[string]string{"url": webFixtureURL}),
		}
	}
	return nil
}

func approveSharedAccountRun(t *testing.T, b *browser, conversation string, run sdk.ConversationRun) {
	t.Helper()
	if i := run.Interaction; i != nil && i.Status == "pending" {
		b.call("POST", "/agent/conversations/"+conversation+"/runs/"+run.ID+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}), 200)
	}
}

func readSharedAccountResult(t *testing.T, b *browser, path string, reference sdk.ConversationResultReference) string {
	t.Helper()
	text, offset := "", 0
	for {
		page := accountDecode[sdk.ConversationResultSlice](t, b.call("POST", path, accountJSON(sdk.ConversationResultRead{Reference: reference, Offset: offset, MaxBytes: 256}), 200))
		if page.Reference != reference || page.Offset != offset {
			t.Fatal("original result page changed its reference or offset")
		}
		text += page.JSONText
		if page.Complete {
			break
		}
		if page.NextOffset <= offset {
			t.Fatal("original result pagination did not advance")
		}
		offset = page.NextOffset
	}
	hash := sha256.Sum256([]byte(text))
	if hex.EncodeToString(hash[:]) != reference.SHA256 {
		t.Fatal("original complete result SHA-256 changed")
	}
	return text
}

func grantSharedAccountPolicy(t *testing.T, f *accountFixture, owner *browser, user string, definitions []sdk.ConversationToolDefinition, execute bool) {
	t.Helper()
	familyActions := map[string]bool{}
	for _, definition := range definitions {
		familyActions[definition.ActionKey] = true
	}
	replacedActions := map[string]bool{sdk.ConversationInteractionPermission().Key: true}
	for _, definition := range sdk.ConversationCollaborationTools() {
		replacedActions[definition.ActionKey] = true
	}
	mutateTestRolePermissions(t, f.host, owner, func(prior []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, p := range prior {
			if !familyActions[p.PermissionKey] && !replacedActions[p.PermissionKey] && !strings.HasPrefix(p.PermissionKey, sdk.ConversationCollaborationPermissionPrefix) && p.PermissionKey != integration.ActionIntegrationConnectionAccountsRead && p.PermissionKey != integration.ActionIntegrationConnectionAccountsList && p.PermissionKey != integration.ActionIntegrationConnectionAccountsWrite {
				out = append(out, p)
			}
		}
		for _, op := range sdk.ConversationCollaborationOperations() {
			out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationCollaborationPermission(op).Key, DataScope: identity.DataScopeAll})
		}
		for _, definition := range sdk.ConversationCollaborationTools() {
			out = append(out, identity.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identity.DataScopeOwner})
		}
		out = append(out, identity.ProjectRolePermission{PermissionKey: sdk.ConversationInteractionPermission().Key, DataScope: identity.DataScopeOwner})
		for _, key := range []string{integration.ActionIntegrationConnectionAccountsRead, integration.ActionIntegrationConnectionAccountsList} {
			out = append(out, identity.ProjectRolePermission{PermissionKey: key, DataScope: identity.DataScopeAll})
		}
		if execute {
			if f.options.MailWriteTools || f.options.CalendarWriteTools {
				out = append(out, identity.ProjectRolePermission{PermissionKey: integration.ActionIntegrationConnectionAccountsWrite, DataScope: identity.DataScopeAll})
			}
			for _, definition := range definitions {
				out = append(out, identity.ProjectRolePermission{PermissionKey: definition.ActionKey, DataScope: identity.DataScopeOwner})
			}
		}
		return out
	}, user)
}

// Worker completion and message acknowledgement may update the resource
// revision. Refresh only that revision; changed agreement, participants or
// delivery proof must never be silently adopted by the fixture decision.
func updateSharedAccountDelegation(t *testing.T, b *browser, path string, read func() sdk.ConversationDelegationDetail, input sdk.ConversationDelegationUpdate) {
	t.Helper()
	initial := read()
	for attempt := 0; attempt < 8; attempt++ {
		current := read()
		if current.AgreementRevision != initial.AgreementRevision || current.ToAgentID != initial.ToAgentID || accountJSON(current.Participants) != accountJSON(initial.Participants) || input.Review != nil && (current.Verification == nil || current.Verification.DeliveryDigest != input.Review.DeliveryDigest) {
			t.Fatal("decision target changed while refreshing its resource revision")
		}
		input.ExpectedRevision = current.Revision
		request := httptest.NewRequest("POST", "http://127.0.0.1:8091"+path+"/decisions", strings.NewReader(accountJSON(input)))
		request.Header.Set("Origin", "http://127.0.0.1:8091")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Agent-Scope", b.scope)
		request.Header.Set("Idempotency-Key", "shared-decision-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		for _, cookie := range b.cookies {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		b.handler.ServeHTTP(response, request)
		if response.Code == 200 {
			return
		}
		var failure struct {
			Code string `json:"code"`
		}
		if response.Code != 409 || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Code != "agent.conversation.revision_conflict" {
			t.Fatal("decision rejected", input.Action, response.Code, response.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("decision revision never stabilized")
}
