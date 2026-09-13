package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
)

type peerOutcomeModel struct {
	outcomeExternalModel
	mu    sync.Mutex
	calls int
}

func (m *peerOutcomeModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return m.outcomeExternalModel.StreamConversationStep(ctx, in, emit)
}

// The fixture commits to disk, drops the response, then withholds the first
// receipt query. A stopped, superseded delegation must never POST again.
func TestPeerOutcomeInspectionOnlyReadsOriginalHTTPReceipt(t *testing.T) {
	peerOutcomeHTTPScenario(t, false, false)
}
func TestPeerTransferOnlyContinuesRemainingHTTPWork(t *testing.T) {
	peerOutcomeHTTPScenario(t, true, false)
}
func TestPeerTransferAgentToolContinuesRemainingHTTPWork(t *testing.T) {
	peerOutcomeHTTPScenario(t, true, true)
}

type peerTransferIssuerModel struct{ peerWebModel }

func (m *peerTransferIssuerModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	arguments, delivered := "", false
	for _, message := range in.Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "Transfer fixture request:\n") {
			arguments = strings.TrimPrefix(message.Content, "Transfer fixture request:\n")
		}
		delivered = delivered || message.Role == "tool" && message.ToolCallID == "transfer-peer"
	}
	if arguments != "" && !delivered {
		return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{{ID: "transfer-peer", Name: "delegation_update", Arguments: arguments}}}}, nil
	}
	return m.peerWebModel.StreamConversationStep(ctx, in, emit)
}

func peerOutcomeHTTPScenario(t *testing.T, transfer, byAgent bool) {
	const initial, changed = "Peer-Outcome-Initial!22", "Peer-Outcome-Changed!33"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "peer-outcome-test-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "peer-outcome-test-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	dir := t.TempDir()
	var mu sync.Mutex
	posts, gets := map[string]int{}, map[string]int{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			http.Error(w, "missing key", 400)
			return
		}
		hash := sha256.Sum256([]byte(key))
		id := hex.EncodeToString(hash[:])
		path := filepath.Join(dir, id+".json")
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/records" {
			posts[key]++
			var args struct {
				Title string `json:"title"`
			}
			if json.NewDecoder(r.Body).Decode(&args) != nil {
				http.Error(w, "invalid", 400)
				return
			}
			raw, _ := json.Marshal(map[string]string{"id": "record-" + id[:16]})
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Error(err)
				http.Error(w, "write failed", 500)
				return
			}
			if args.Title == "lose-response" {
				c, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = c.Close()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/receipts" {
			gets[key]++
			if gets[key] == 1 {
				http.Error(w, "receipt not visible yet", 404)
				return
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				http.Error(w, "unknown", 404)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	fixture := &outcomeHTTPToolHost{confirmationWebFixture: &confirmationWebFixture{}, url: upstream.URL}
	model := &peerOutcomeModel{}
	var sourceModel sdk.ConversationModel = &peerWebModel{}
	if byAgent {
		sourceModel = &peerTransferIssuerModel{}
		t.Cleanup(func() {
			if !t.Failed() {
				return
			}
			m := sourceModel.(*peerTransferIssuerModel)
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, request := range m.requests {
				for _, message := range request.Messages {
					if message.Role == "user" {
						t.Logf("issuer user input: %q", message.Content)
					}
				}
			}
		})
	}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "peer-outcome.db"), RuntimeID: "peer-outcome-runtime", WorkspaceID: "peer-outcome-workspace", ApplicationKey: "peer-outcome-app", Agent: agentmodule.Options{ConversationProvider: sourceModel, ConversationOptions: agentmodule.ConversationOptions{AgentModels: map[string]sdk.ConversationModel{"receipt": model}, ToolHost: fixture, Lease: 2 * time.Second, Poll: 5 * time.Millisecond}}}
	// Bind the fixture while the new Identity owner is ready and before any
	// persisted worker can run. Binding after Open races restart recovery.
	options.Prepare = func(_ context.Context, current *Host) error { fixture.host = current; return nil }
	var host *Host
	var handler http.Handler
	open := func() {
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		handler, err = webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("chat")}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	open()
	defer func() { _ = host.Close(context.Background()) }()
	b := &browser{t: t, handler: handler, cookies: map[string]*http.Cookie{}}
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	permission := identitysdk.PermissionDefinition{PermissionKey: "agent.fixture_records.create", ResourceKey: "agent.fixture_records", OperationKey: "create", Label: "Peer outcome fixture", Category: "Acceptance test", SourceKind: "agent_tool"}
	registration, err := identitysdk.NewPermissionReconcileRequest(host.application, "agent:peer_outcome_fixture", "", []identitysdk.PermissionDefinition{permission})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = host.Identity.Permissions().Reconcile(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	grant := func() {
		mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
			return append(previous, identitysdk.ProjectRolePermission{PermissionKey: permission.PermissionKey, DataScope: identitysdk.DataScopeOwner})
		})
	}
	grant()
	var agent sdk.ConversationAgent
	json.Unmarshal(b.call("POST", "/agent/agents", `{"client_id":"receipt-peer","name":"回执核查 Agent","instructions":"执行隔离记录写入，保留原操作结果","tools":["create_fixture_record"],"skill_keys":[],"model_key":"receipt","enabled":true,"max_concurrent":1}`, 200).Body.Bytes(), &agent)
	var source sdk.Conversation
	json.Unmarshal(b.call("POST", "/agent/conversations", `{"client_id":"receipt-source","title":"原操作结果核查"}`, 200).Body.Bytes(), &source)
	in := sdk.ConversationDelegationCreate{ClientID: "receipt-work", ConversationID: source.ID, AgentID: agent.ID, Purpose: "核查原操作结果", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "创建两个隔离记录", Deliverable: "原操作回执", CompletionConditions: []string{"取得两项操作回执"}}}
	raw, _ := json.Marshal(in)
	var d sdk.ConversationDelegationDetail
	json.Unmarshal(b.call("POST", "/agent/delegations", string(raw), 200).Body.Bytes(), &d)
	path := "/agent/delegations/" + d.ID
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &d)
		if d.Task != nil && d.Task.ExecutionRunID != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if d.Task == nil || d.Task.ExecutionRunID == "" {
		t.Fatalf("run did not launch: %+v", d)
	}
	runPath := "/agent/conversations/" + d.ConversationID + "/runs/" + d.Task.ExecutionRunID
	outcomeApprove(t, b, runPath, outcomeWait(t, b, runPath, "waiting_confirmation"), "listed_operations")
	unknown := outcomeWait(t, b, runPath, "needs_reconciliation")
	json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &d)
	brief := d.Brief
	brief.Version++
	brief.Goal = "按新要求处理剩余事项"
	update := sdk.ConversationDelegationUpdate{ClientID: "change-requirements", ExpectedRevision: d.Revision, Action: "update_brief", Reason: "停止旧要求下的后续执行", Brief: &brief}
	raw, _ = json.Marshal(update)
	json.Unmarshal(b.call("POST", path+"/decisions", string(raw), 200).Body.Bytes(), &d)
	outcomeWait(t, b, runPath, "cancelled")
	if err = host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	open()
	b.handler = handler
	b.login("admin@example.com", changed)
	b.call("POST", runPath+"/resume", "", 409)
	inspect := sdk.ConversationDelegationUpdate{ClientID: "query-original", ExpectedRevision: d.Revision, Action: "inspect_outcome", Reason: "核查原操作回执", Inspection: &sdk.ConversationOutcomeInspectionRequest{RunID: unknown.ID, Step: 0, CallID: "external-unknown"}}
	raw, _ = json.Marshal(inspect)
	b.call("POST", path+"/decisions", string(raw), 503)
	still := outcomeWait(t, b, runPath, "cancelled")
	if still.Steps[0].Calls[1].Status != "uncertain" {
		t.Fatalf("missing receipt treated as known: %+v", still.Steps)
	}
	resume := sdk.ConversationDelegationUpdate{ClientID: "blocked-resume", ExpectedRevision: d.Revision, Action: "resume", Reason: "尝试按新要求继续"}
	resumeRaw, _ := json.Marshal(resume)
	b.call("POST", path+"/decisions", string(resumeRaw), 409)
	mutateTestRolePermissions(t, host, b, func(previous []identitysdk.ProjectRolePermission) []identitysdk.ProjectRolePermission {
		out := []identitysdk.ProjectRolePermission{}
		for _, p := range previous {
			if p.PermissionKey != permission.PermissionKey {
				out = append(out, p)
			}
		}
		return out
	})
	// The profile catalog removes revoked tools before dispatch.
	b.call("POST", path+"/decisions", string(raw), 503)
	mu.Lock()
	if len(gets) != 1 {
		t.Error("unexpected receipt queries")
	}
	for _, count := range gets {
		if count != 1 {
			t.Error("revoked query reached upstream")
		}
	}
	mu.Unlock()
	grant()
	if os.Getenv("AGENT_PEER_OUTCOME_BROWSER") == "1" {
		runPeerOutcomeBrowser(t, host, options, source.ID, changed, "peer-outcome.browser.mjs")
	} else {
		b.call("POST", path+"/decisions", string(raw), 200)
	}

	done := outcomeWait(t, b, runPath, "cancelled")
	call := done.Steps[0].Calls[1]
	if done.Metrics.ToolCalls != 2 || done.Metrics.ToolAttempts != 2 {
		t.Fatalf("receipt query counted as another invocation: %+v", done.Metrics)
	}
	inspections := 0
	for _, event := range done.Audit {
		if event.Type == "outcome_inspection" {
			inspections++
			if event.ActorID == "" {
				t.Fatal("receipt audit actor missing")
			}
		}
	}
	if inspections != 4 {
		t.Fatalf("missing separate inspection audit: %d", inspections)
	}
	if call.Status != "completed" || call.ResourceID == "" || call.OutcomeInspection == nil || call.OutcomeInspection.Status != "completed" {
		t.Fatalf("missing queried receipt: %+v", call)
	}
	json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &d)
	if d.Status != "needs_update" || d.Task.ExecutionRunID != unknown.ID {
		t.Fatalf("inspection restarted delegation: %+v", d)
	}
	// A duplicate request reads the persisted known result without another GET.
	b.call("POST", path+"/decisions", string(raw), 200)
	expectedModels := 1
	if transfer {
		var target sdk.ConversationAgent
		json.Unmarshal(b.call("POST", "/agent/agents", `{"client_id":"receipt-replacement","name":"接替核查 Agent","instructions":"读取原操作回执，只完成剩余工作","tools":["create_fixture_record"],"skill_keys":[],"model_key":"receipt","enabled":true,"max_concurrent":1}`, 200).Body.Bytes(), &target)
		oldConversation, oldTask := d.ConversationID, d.Task.ID
		update := sdk.ConversationDelegationUpdate{ClientID: "handoff", ExpectedRevision: d.Revision, Action: "transfer", Reason: "原执行已停止，换接收方继续", Transfer: &sdk.ConversationDelegationTransfer{AgentID: target.ID, RemainingWork: "核对两项原回执并完成剩余复核"}}
		transferRaw, _ := json.Marshal(update)
		var issuerRun sdk.ConversationRun
		if byAgent {
			deadline := time.Now().Add(15 * time.Second)
			for {
				var current sdk.Conversation
				json.Unmarshal(b.call("GET", "/agent/conversations/"+source.ID, "", 200).Body.Bytes(), &current)
				if current.ActiveRunID == "" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("previous issuer notification remained active: %s", b.call("GET", "/agent/conversations/"+source.ID+"/runs/"+current.ActiveRunID, "", 200).Body.String())
				}
				time.Sleep(25 * time.Millisecond)
			}
			arguments, _ := json.Marshal(map[string]any{"id": d.ID, "update": map[string]any{"expected_revision": update.ExpectedRevision, "action": update.Action, "reason": update.Reason, "transfer": update.Transfer}})
			send, _ := json.Marshal(sdk.ConversationSend{ClientMessageID: "transfer-through-agent", Message: "Transfer fixture request:\n" + string(arguments)})
			json.Unmarshal(b.call("POST", "/agent/conversations/"+source.ID+"/messages", string(send), 202).Body.Bytes(), &issuerRun)
			issuerPath := "/agent/conversations/" + source.ID + "/runs/" + issuerRun.ID
			waiting := outcomeWait(t, b, issuerPath, "waiting_confirmation")
			var before sdk.ConversationDelegationDetail
			json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &before)
			if before.ConversationID != oldConversation {
				t.Fatal("Agent transferred before its exact operation was confirmed")
			}
			outcomeApprove(t, b, issuerPath, waiting, "")
			issuerRun = outcomeWait(t, b, issuerPath, "completed")
			if len(issuerRun.Steps) < 2 || issuerRun.Steps[0].Calls[0].Status != "completed" {
				t.Fatalf("Agent transfer tool failed: %+v", issuerRun)
			}
		} else if os.Getenv("AGENT_PEER_TRANSFER_BROWSER") == "1" {
			runPeerOutcomeBrowser(t, host, options, source.ID, changed, "peer-transfer.browser.mjs")
		} else {
			b.call("POST", path+"/decisions", string(transferRaw), 200)
		}
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &d)
			if d.Task != nil && d.Task.Status == "completed" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if d.Task == nil || d.Task.Status != "completed" || d.ConversationID == oldConversation || d.Task.ID == oldTask || d.ToAgentID != target.ID || len(d.Assignments) != 2 || d.Handoff == nil || len(d.Handoff.Effects) != 2 {
			t.Fatalf("handoff failed: %+v", d)
		}
		if d.Task.Budget.MaxSteps != 11 || d.Task.Budget.MaxToolCalls != 10 {
			t.Fatalf("budget reset: %+v", d.Task.Budget)
		}
		var historicalTask sdk.ConversationTaskDetail
		json.Unmarshal(b.call("GET", "/agent/conversation-tasks/"+oldTask, "", 200).Body.Bytes(), &historicalTask)
		if historicalTask.Control.CanResume || historicalTask.Control.ResumeBlocker != "delegation_superseded" {
			t.Fatalf("historical assignment exposed active controls: %+v", historicalTask.Control)
		}
		if byAgent {
			if d.Handoff.Source == nil || d.Handoff.Source.RunID != issuerRun.ID || d.Handoff.Source.BeforeStep != 1 || d.Assignments[1].Source == nil || d.Assignments[1].Source.RunID != issuerRun.ID {
				t.Fatalf("Agent decision source missing: %+v", d)
			}
			var history sdk.ConversationAgreementHistory
			json.Unmarshal(b.call("GET", path+"/requirements", "", 200).Body.Bytes(), &history)
			if len(history.Items) == 0 || history.Items[0].ChangeSource == nil || history.Items[0].ChangeSource.RunID != issuerRun.ID {
				t.Fatalf("Agent agreement decision source missing: %+v", history)
			}
		}
		newRun := outcomeWait(t, b, "/agent/conversations/"+d.ConversationID+"/runs/"+d.Task.ExecutionRunID, "completed")
		if len(newRun.Steps) < 2 || len(newRun.Steps[0].Calls) != 2 || newRun.Metrics.ToolAttempts != 0 {
			t.Fatalf("old effects invoked again: %+v", newRun)
		}
		for _, call := range newRun.Steps[0].Calls {
			if call.ReusedFrom == nil || call.ReusedFrom.RunID != unknown.ID || call.ResourceID == "" || call.ErrorCode != "" {
				t.Fatalf("original receipt missing: %+v", call)
			}
		}
		b.call("POST", runPath+"/resume", "", 409)
		if os.Getenv("AGENT_PEER_TRANSFER_BROWSER") != "1" && !byAgent {
			b.call("POST", path+"/decisions", string(transferRaw), 200)
		}
		if err = host.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		open()
		b.handler = handler
		b.login("admin@example.com", changed)
		var after sdk.ConversationDelegationDetail
		json.Unmarshal(b.call("GET", path, "", 200).Body.Bytes(), &after)
		if after.ID != d.ID || after.ToAgentID != target.ID || len(after.Assignments) != 2 || after.Task.ExecutionRunID != newRun.ID {
			t.Fatalf("restart lost assignment: %+v", after)
		}
		expectedModels = 3
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 2 || len(gets) != 1 {
		t.Fatalf("effect counts posts=%v gets=%v", posts, gets)
	}
	for key, count := range posts {
		if count != 1 {
			t.Fatalf("repeated POST: %s %d", key, count)
		}
	}
	for key, count := range gets {
		if posts[key] != 1 || count != 2 {
			t.Fatalf("wrong original receipt key/count: %s %d", key, count)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatal("external effect mismatch", err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.calls != expectedModels {
		t.Fatalf("inspection restarted model: %d", model.calls)
	}
	t.Logf("2 actual file effects, 1 POST per key, 2 GETs for original uncertain operation, %d receiver model calls; transfer=%v; restart, exact receipt reuse and original budget verified", model.calls, transfer)
}

func runPeerOutcomeBrowser(t *testing.T, host *Host, options Options, conversationID, password, script string) {
	t.Helper()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	ui, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: origin, Files: os.DirFS(filepath.Join(project, "frontend/dist"))})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = ui
	server.Start()
	defer server.Close()
	command := exec.CommandContext(t.Context(), os.Getenv("AGENT_NODE_BINARY"), filepath.Join(project, "frontend/tests", script))
	command.Env = append(os.Environ(), "AGENT_UI_ORIGIN="+origin, "AGENT_UI_CONVERSATION="+conversationID, "AGENT_UI_PASSWORD="+password)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err = command.Run(); err != nil {
		t.Fatal(err)
	}
}
