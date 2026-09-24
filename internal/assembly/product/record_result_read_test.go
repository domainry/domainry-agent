package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	agent "github.com/domainry/domainry-agent-sdk"
	agentmodule "github.com/domainry/domainry-agent/module"
	"github.com/domainry/domainry-foundation/requestcontext"
	identity "github.com/domainry/domainry-identity-sdk"
	identityhttp "github.com/domainry/domainry-identity-sdk/httpapi"
	"github.com/domainry/domainry-knowledge-sdk/contract"
	"github.com/domainry/domainry-orm/query"
	toolsdk "github.com/domainry/domainry-tools-sdk"
	toolmodule "github.com/domainry/domainry-tools/module"
)

type recordPolicyBaseHost struct{}

func (recordPolicyBaseHost) ConversationTools(context.Context, agent.ConversationAuthority) ([]agent.ConversationToolDefinition, error) {
	return nil, nil
}
func (recordPolicyBaseHost) AuthorizeConversationTool(context.Context, agent.ConversationToolRequest) (agent.ConversationToolAuthorization, error) {
	return agent.ConversationToolAuthorization{}, nil
}
func (recordPolicyBaseHost) InvokeConversationTool(context.Context, agent.ConversationToolRequest) (agent.ConversationToolResult, error) {
	return agent.ConversationToolResult{}, fmt.Errorf("unselected base tool")
}
func (recordPolicyBaseHost) ReconcileConversationTool(context.Context, agent.ConversationToolRequest) (agent.ConversationToolResult, error) {
	return agent.ConversationToolResult{}, fmt.Errorf("unselected base tool")
}

func TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart(t *testing.T) {
	const initial = "Record-Policy-Initial!2026"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "record-policy-signing-secret-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "record-policy-data-secret-long-enough")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SAAS_AGENT_CONFIG", "")
	t.Setenv("SAAS_SKILLS_CONFIG", "")
	var validations int
	spec := toolmodule.RecordSpec{Kind: "requirements", Prefix: "requirements", Name: "Requirements", DataSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"}},"required":["amount"],"additionalProperties":false}`), Validate: func(_, next *contract.Record) error { validations++; next.Status = "draft"; return nil }}
	p, err := ConfigureRecords("record-policy", map[string]any{}, []byte(`{"key":"record-policy","version":"1","name":"Record policy","instructions":"Read original structured records and preserve exact immutable receipt references.","tools":["requirements_list","requirements_read","requirements_save","agent_delegate","delegation_get","delegation_update","delegation_execution_publish","delegation_executions","delegation_execution_read","delegation_execution_result_read"]}`), []byte(`[]`), spec)
	if err != nil {
		t.Fatal(err)
	}
	p.CalendarTools, p.MailTools, p.WebTools, p.CalendarWriteTools, p.MailWriteTools, p.ReportTools, p.AnalysisTools, p.ScheduleTools = false, false, false, false, false, false, false, false
	var selected agent.ConversationToolHost
	prepare := p.Prepare
	p.Prepare = func(ctx context.Context, h *Host) (Binding, error) {
		b, e := prepare(ctx, h)
		if e == nil {
			selected, e = b.AssembleTools(recordPolicyBaseHost{})
		}
		return b, e
	}
	options := ProductOptions{Host: Options{DatabasePath: filepath.Join(t.TempDir(), "records.db"), RuntimeID: "record-runtime", WorkspaceID: "record-workspace", ApplicationKey: "record-policy", Agent: agentmodule.Options{ConversationProvider: recordDeliveryModel{}, ConversationOptions: agentmodule.ConversationOptions{Poll: 5 * time.Millisecond, AgentModels: map[string]agent.ConversationModel{"record-inspector": recordInspectorModel{}}}}}, Origin: "http://127.0.0.1:8091", Files: fstest.MapFS{"index.html": {Data: []byte("record policy")}}}
	var host *ProductHost
	open := func() {
		t.Helper()
		var e error
		host, e = OpenProduct(t.Context(), p, options)
		if e != nil {
			t.Fatal(e)
		}
	}
	open()
	t.Cleanup(func() {
		if host != nil {
			_ = host.Close(context.Background())
		}
	})
	cookies := map[string]*http.Cookie{}
	scope := ""
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest(method, options.Origin+path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", options.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Agent-Scope", scope)
		idempotencyKey := sha256.Sum256([]byte(method + "\x00" + path + "\x00" + string(raw)))
		r.Header.Set("Idempotency-Key", "record-policy-"+hex.EncodeToString(idempotencyKey[:16]))
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		host.Handler.ServeHTTP(w, r)
		for _, c := range w.Result().Cookies() {
			cookies[c.Name] = c
		}
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	session := func() agent.ConversationAuthority {
		t.Helper()
		var value struct {
			Scope  string `json:"scope"`
			UserID string `json:"user_id"`
		}
		if e := json.Unmarshal(call("GET", "/app/session", nil, 200), &value); e != nil {
			t.Fatal(e)
		}
		scope = value.Scope
		resolved, e := host.Identity.Principals().Resolve(requestcontext.WithWorkspaceID(t.Context(), options.Host.WorkspaceID), identity.PrincipalResolutionRequest{SubjectID: identity.SubjectID(value.UserID)})
		if e != nil || !resolved.Principal.Known || resolved.Principal.UserID != value.UserID || resolved.Principal.WorkspaceID != options.Host.WorkspaceID || resolved.Principal.RoleKey == "" {
			t.Fatal("actual session principal and selected role missing", e)
		}
		return agent.ConversationAuthority{Known: true, RuntimeID: options.Host.RuntimeID, WorkspaceID: options.Host.WorkspaceID, UserID: value.UserID, RoleKey: resolved.Principal.RoleKey}
	}
	call("POST", "/auth/login", map[string]string{"login": "admin@example.com", "password": initial}, 200)
	call("POST", "/auth/password/change", map[string]string{"current_password": initial, "new_password": "Record-Policy-Changed!2026"}, 200)
	owner := session()
	call("POST", "/app/product/setup", map[string]any{}, 200)
	owner = session()
	definitions := toolmodule.RecordDefinitions(spec)
	request := func(key, args, idempotency string) agent.ConversationToolRequest {
		var d agent.ConversationToolDefinition
		for _, v := range definitions {
			if v.Key == key {
				d = v
			}
		}
		return agent.ConversationToolRequest{Authority: owner, ConversationID: "original-record-conversation", RunID: "original-record-run", Step: 1, Definition: d, Call: agent.ConversationToolCall{ID: key, Name: key, Arguments: args}, IdempotencyKey: idempotency}
	}
	invoke := func(r agent.ConversationToolRequest) agent.ConversationToolResult {
		t.Helper()
		out, e := selected.InvokeConversationTool(t.Context(), r)
		if e != nil || out.Status != "completed" {
			t.Fatal("actual registered record invocation", r.Call.Name, out, e)
		}
		return out
	}
	saveRequest := request("requirements_save", `{"expected_revision":0,"title":"Original","status":"draft","data":{"amount":9007199254740993}}`, "record-save-original")
	saved := invoke(saveRequest)
	var original contract.Record
	if json.Unmarshal(saved.Content, &original) != nil || original.Revision != 1 || string(original.Data) != `{"amount":9007199254740993}` || saved.ResourceID != original.ID {
		t.Fatal("actual original save changed", saved)
	}
	readRequest := request("requirements_read", fmt.Sprintf(`{"id":%q}`, original.ID), "")
	readResult := invoke(readRequest)
	listRequest := request("requirements_list", `{"query":"Original","limit":1}`, "")
	listResult := invoke(listRequest)
	emptyRequest := request("requirements_list", `{"query":"absent","limit":1}`, "")
	emptyResult := invoke(emptyRequest)
	invoke(request("requirements_save", fmt.Sprintf(`{"id":%q,"expected_revision":1,"title":"Updated","status":"draft","data":{"amount":2}}`, original.ID), "record-save-update"))
	if validations != 2 {
		t.Fatal("unexpected durable save count", validations)
	}
	call("POST", "/app/product/collaboration-setup", map[string]any{}, 200)
	var peer agent.ConversationAgent
	if json.Unmarshal(call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "record-peer", Name: "Structured record verifier", Instructions: "Create one personal structured record and verify its exact saved revision and original list; deliver all original receipts.", Tools: []string{"requirements_save", "requirements_read", "requirements_list", "delegation_get", "delegation_update"}, SkillKeys: []string{}, ModelKey: "default", Enabled: true, MaxConcurrent: 1}, 200), &peer) != nil {
		t.Fatal("actual peer configuration missing")
	}
	var conversation agent.Conversation
	if json.Unmarshal(call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "record-dispatch", Title: "Verify original structured records"}, 200), &conversation) != nil {
		t.Fatal("source conversation missing")
	}
	var issued agent.ConversationRun
	if json.Unmarshal(call("POST", "/agent/conversations/"+conversation.ID+"/messages", agent.ConversationSend{ClientMessageID: "record-dispatch-message", Message: "Delegate record request:\n" + peer.ID}, 202), &issued) != nil {
		t.Fatal("actual dispatch missing")
	}
	var delivered agent.ConversationDelegationDetail
	var execution agent.ConversationRun
	approve := func(run agent.ConversationRun) {
		if i := run.Interaction; i != nil && i.Status == "pending" {
			call("POST", "/agent/conversations/"+run.ConversationID+"/runs/"+run.ID+"/respond", agent.ConversationInteractionResponse{InteractionID: i.ID, ClientID: "record-approve-" + i.ID, ExpectedRevision: i.Revision, Decision: "approve"}, 200)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	receivingObserved := false
	for time.Now().Before(deadline) {
		if !receivingObserved {
			var current agent.ConversationRun
			if json.Unmarshal(call("GET", "/agent/conversations/"+conversation.ID+"/runs/"+issued.ID, nil, 200), &current) != nil {
				t.Fatal("source run unavailable")
			}
			approve(current)
			var page agent.ConversationDelegationPage
			if json.Unmarshal(call("GET", "/agent/delegations", nil, 200), &page) != nil {
				t.Fatal("actual delegation index unavailable")
			}
			if len(page.Items) > 0 {
				if json.Unmarshal(call("GET", "/agent/delegations/"+page.Items[0].ID, nil, 200), &delivered) != nil {
					t.Fatal("actual agreement unavailable")
				}
				if task := delivered.Task; task != nil && task.ExecutionRunID != "" {
					// Dispatch/approval and the independently admitted task have
					// separate observation windows. The actual task stays at 60s.
					deadline = time.Now().Add(60 * time.Second)
					receivingObserved = true
				}
			} else if current.Terminal() {
				t.Fatal("actual model did not dispatch", current)
			}
		}
		if receivingObserved {
			// Track the exact admitted execution; do not continuously project
			// the completed dispatcher and full delegation while it is working.
			if json.Unmarshal(call("GET", "/agent/conversations/"+delivered.ConversationID+"/runs/"+delivered.Task.ExecutionRunID, nil, 200), &execution) != nil {
				t.Fatal("actual receiving run unavailable")
			}
			approve(execution)
			if execution.Terminal() {
				if json.Unmarshal(call("GET", "/agent/delegations/"+delivered.ID, nil, 200), &delivered) != nil {
					t.Fatal("actual completed agreement unavailable")
				}
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if execution.Status != "completed" || delivered.Delivery == nil || delivered.Verification == nil || !delivered.Verification.Ready || len(execution.Steps) != 6 || validations != 3 {
		t.Fatalf("actual original record delivery incomplete: run=%s agreement=%s saves=%d", recordProductJSON(execution), recordProductJSON(delivered), validations)
	}
	refs := []agent.ConversationResultReference{}
	for _, condition := range delivered.Delivery.Conditions {
		refs = append(refs, condition.Receipts...)
	}
	if len(refs) != 3 || string(delivered.Delivery.Data) != `{"amount":9007199254740993}` {
		t.Fatal("actual original record data or references changed", delivered.Delivery)
	}
	var history agent.ConversationDeliveryHistory
	if json.Unmarshal(call("GET", "/agent/delegations/"+delivered.ID+"/deliveries", nil, 200), &history) != nil || len(history.Items) != 1 || history.Items[0].Kind != "deliver" || history.Items[0].Revision < 1 {
		t.Fatal("actual immutable delivery revision missing")
	}
	historicalRevision := history.Items[0].Revision
	runReference := agent.ConversationRunReference{ConversationID: execution.ConversationID, RunID: execution.ID}
	var publications []agent.ConversationExecutionPublication
	if json.Unmarshal(call("GET", "/agent/delegations/"+delivered.ID+"/executions", nil, 200), &publications) != nil || len(publications) != 0 {
		t.Fatal("delivery implicitly published private execution")
	}
	var publicationBase agent.ConversationDelegationDetail
	if json.Unmarshal(call("GET", "/agent/delegations/"+delivered.ID, nil, 200), &publicationBase) != nil {
		t.Fatal("current original execution publication version missing")
	}
	var published agent.ConversationExecutionPublication
	if json.Unmarshal(call("POST", "/agent/delegations/"+delivered.ID+"/execution-publications", agent.ConversationExecutionShare{ClientID: "record-original-execution", Reference: runReference, ExpectedRevision: publicationBase.Revision, Reason: "Original execution owner explicitly publishes the actual structured record run"}, 200), &published) != nil || published.Reference != runReference || published.Publisher != owner || published.Withdrawn {
		t.Fatalf("actual owner publication changed execution identity: publication=%s owner=%s", recordProductJSON(published), recordProductJSON(owner))
	}
	var inspector agent.ConversationAgent
	if json.Unmarshal(call("POST", "/agent/agents", agent.ConversationAgentWrite{ClientID: "record-inspector", Name: "Shared execution reader", Instructions: "Publish and inspect the exact original shared execution through actual collaboration tools; retain its original result pages.", Tools: []string{"delegation_get", "delegation_execution_publish", "delegation_executions", "delegation_execution_read", "delegation_execution_result_read"}, SkillKeys: []string{}, ModelKey: "record-inspector", Enabled: true, MaxConcurrent: 1}, 200), &inspector) != nil {
		t.Fatal("actual inspector configuration missing")
	}
	var inspectionConversation agent.Conversation
	if json.Unmarshal(call("POST", "/agent/conversations", agent.ConversationCreate{ClientID: "record-inspection", AgentID: inspector.ID, Title: "Inspect original shared structured record"}, 200), &inspectionConversation) != nil {
		t.Fatal("actual inspector conversation missing")
	}
	var inspection agent.ConversationRun
	if json.Unmarshal(call("POST", "/agent/conversations/"+inspectionConversation.ID+"/messages", agent.ConversationSend{ClientMessageID: "record-inspection", Message: "Inspect shared record execution:\n" + recordProductJSON(recordInspectorInput{ID: delivered.ID, Reference: runReference})}, 202), &inspection) != nil {
		t.Fatal("actual inspector execution missing")
	}
	inspectionPath := "/agent/conversations/" + inspectionConversation.ID + "/runs/" + inspection.ID
	publicationConfirmed := false
	// This is an ordinary conversation, using the service's default five-minute
	// run timeout. Observe its actual budget; the delegated task remains at 60s.
	for deadline := time.Now().Add(5 * time.Minute); time.Now().Before(deadline); {
		if json.Unmarshal(call("GET", inspectionPath, nil, 200), &inspection) != nil {
			t.Fatal("actual inspector run missing")
		}
		if i := inspection.Interaction; i != nil && i.Status == "pending" {
			if i.Kind != "confirmation" || i.Tool != "delegation_execution_publish" {
				t.Fatal("shared execution read requested unexpected confirmation", i.Tool)
			}
			approve(inspection)
			publicationConfirmed = true
		}
		if inspection.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if inspection.Status != "completed" || !publicationConfirmed || len(inspection.Steps) != 7 {
		t.Fatalf("actual shared tool inspector incomplete: %s", recordProductJSON(inspection))
	}
	inspectionRefs := []agent.ConversationResultReference{}
	for _, step := range inspection.Steps {
		for _, tool := range step.Calls {
			if tool.ResultReference == nil {
				t.Fatal("actual shared tool inspector lost original wrapper reference", tool.Name)
			}
			inspectionRefs = append(inspectionRefs, *tool.ResultReference)
		}
	}
	if len(inspectionRefs) != 6 {
		t.Fatal("actual shared inspector tool set incomplete", inspectionRefs)
	}
	assertInspection := func(want bool) {
		t.Helper()
		var read agent.ConversationRun
		if json.Unmarshal(call("GET", inspectionPath, nil, 200), &read) != nil || (read.AccessError == "") != want || want && len(read.Steps) != 7 || !want && len(read.Steps) != 0 {
			t.Fatal("saved shared execution wrapper ignored current source rights", recordProductJSON(read))
		}
		if !want {
			return
		}
		for _, ref := range inspectionRefs {
			var full strings.Builder
			for offset := 0; ; {
				var slice agent.ConversationResultSlice
				if json.Unmarshal(call("POST", inspectionPath+"/result", agent.ConversationResultRead{Reference: ref, Offset: offset, MaxBytes: 8192}, 200), &slice) != nil || slice.Reference != ref || slice.Offset != offset || slice.NextOffset <= offset {
					t.Fatal("original saved execution tool wrapper changed", ref)
				}
				full.WriteString(slice.JSONText)
				offset = slice.NextOffset
				if slice.Complete {
					break
				}
			}
			sum := sha256.Sum256([]byte(full.String()))
			if hex.EncodeToString(sum[:]) != ref.SHA256 {
				t.Fatal("original shared wrapper SHA changed", ref)
			}
			t.Logf("Exact original saved execution wrapper %s: original SHA-256 verified", ref.CallID)
		}
	}
	assertInspection(true)
	assertDelivery := func(want int) {
		t.Helper()
		if want == 200 {
			var originalRun agent.ConversationRun
			if json.Unmarshal(call("POST", "/agent/delegations/"+delivered.ID+"/execution", runReference, 200), &originalRun) != nil || originalRun.ID != execution.ID || len(originalRun.Steps) != 6 || originalRun.Interaction != nil || originalRun.WriteScope != nil {
				t.Fatal("actual shared record execution changed or exposed confirmation controls")
			}
		} else {
			call("POST", "/agent/delegations/"+delivered.ID+"/execution", runReference, want)
		}
		for _, source := range []struct {
			name     string
			path     string
			revision int64
		}{{"current delivery", "delivery-result", 0}, {"original historical delivery", "delivery-result", historicalRevision}, {"shared execution", "execution-result", 0}} {
			for _, ref := range refs {
				var complete strings.Builder
				offset, pages := 0, 0
				for {
					read := agent.ConversationResultRead{Reference: ref, Offset: offset, MaxBytes: 256}
					var input any = read
					if source.path == "delivery-result" {
						input = agent.ConversationDeliveryResultRead{DeliveryRevision: source.revision, ConversationResultRead: read}
					}
					body := call("POST", "/agent/delegations/"+delivered.ID+"/"+source.path, input, want)
					if want != 200 {
						break
					}
					var slice agent.ConversationResultSlice
					if json.Unmarshal(body, &slice) != nil || slice.Reference != ref || slice.Offset != offset || slice.NextOffset <= offset {
						t.Fatal("exact original record page changed", ref)
					}
					complete.WriteString(slice.JSONText)
					pages++
					offset = slice.NextOffset
					if slice.Complete {
						break
					}
				}
				if want == 200 {
					sum := sha256.Sum256([]byte(complete.String()))
					if hex.EncodeToString(sum[:]) != ref.SHA256 {
						t.Fatal("exact original record SHA changed", ref)
					}
					t.Logf("Exact original structured result %s via %s: %d pages, original SHA-256 verified", ref.CallID, source.name, pages)
				}
			}
		}
	}
	assertDelivery(200)
	var currentDelivery agent.ConversationDelegationDetail
	if json.Unmarshal(call("GET", "/agent/delegations/"+delivered.ID, nil, 200), &currentDelivery) != nil || currentDelivery.Verification == nil || !currentDelivery.Verification.Ready || currentDelivery.Verification.DeliveryDigest != delivered.Verification.DeliveryDigest || currentDelivery.Brief.Version != delivered.Brief.Version || currentDelivery.AgreementRevision != delivered.AgreementRevision {
		t.Fatal("actual delivery changed during original page verification")
	}
	call("POST", "/agent/delegations/"+delivered.ID+"/decisions", agent.ConversationDelegationUpdate{ClientID: "accept-record-originals", ExpectedRevision: currentDelivery.Revision, Action: "accept_delivery", Reason: "Checked actual original save, read and list pages", Review: &agent.ConversationDeliveryReview{DeliveryDigest: currentDelivery.Verification.DeliveryDigest}}, 200)
	expectedSaves := validations
	permissionToken := ""
	mutateRole := func(actor agent.ConversationAuthority, dropRead, dropWrite bool) {
		t.Helper()
		roleAssignments, e := host.Identity.Projection().ListUserRoleAssignments(t.Context(), identity.UserRoleAssignmentQuery{UserID: identity.SubjectID(actor.UserID)})
		if e != nil || len(roleAssignments) == 0 {
			t.Fatal("actual actor role missing", e)
		}
		mux := http.NewServeMux()
		for _, a := range host.Identity.(identityhttp.Provider).HTTPAdapters() {
			for _, r := range a.Routes() {
				mux.Handle(r.Pattern(), a.Handler())
			}
		}
		path := "/identity/roles/" + roleAssignments[0].RoleID + "/permissions"
		roleCall := func(method, body, hash string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(method, path, strings.NewReader(body))
			token := ""
			for name, c := range cookies {
				if strings.HasSuffix(name, "_access") {
					token = c.Value
				}
			}
			if permissionToken != "" {
				token = permissionToken
			}
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Workspace-ID", actor.WorkspaceID)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Expected-Schema-Hash", hash)
			r.Header.Set("Idempotency-Key", fmt.Sprintf("record-role-%s-%d-%v-%v", actor.UserID, validations, dropRead, dropWrite))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("actual Identity %s: %d %s", method, w.Code, w.Body.String())
			}
			return w
		}
		before := roleCall("GET", "", "")
		var permissions []identity.ProjectRolePermission
		if json.Unmarshal(before.Body.Bytes(), &permissions) != nil {
			t.Fatal("permissions response invalid")
		}
		out := []identity.ProjectRolePermission{}
		for _, grant := range permissions {
			if strings.HasPrefix(grant.PermissionKey, "requirements.") {
				continue
			}
			out = append(out, grant)
		}
		if !dropRead {
			out = append(out, identity.ProjectRolePermission{PermissionKey: "requirements.read", DataScope: identity.DataScopeOwner}, identity.ProjectRolePermission{PermissionKey: "requirements.list", DataScope: identity.DataScopeOwner})
		}
		if !dropWrite {
			out = append(out, identity.ProjectRolePermission{PermissionKey: "requirements.save", DataScope: identity.DataScopeOwner})
		}
		raw, _ := json.Marshal(map[string]any{"permissions": out, "business_reason": "Isolated original record read capability acceptance"})
		roleCall("PUT", string(raw), before.Header().Get("X-Resource-Hash"))
	}
	mutate := func(dropRead, dropWrite bool) { mutateRole(owner, dropRead, dropWrite) }
	assertRead := func(want bool) {
		t.Helper()
		policy, ok := selected.(toolsdk.ResultReadAuthorizer)
		if !ok {
			t.Fatal("product did not expose independent source policy")
		}
		for i, item := range []struct {
			request agent.ConversationToolRequest
			result  agent.ConversationToolResult
		}{{saveRequest, saved}, {readRequest, readResult}, {listRequest, listResult}, {emptyRequest, emptyResult}} {
			originalBytes := string(item.result.Content)
			err := policy.AuthorizeConversationToolResultRead(t.Context(), item.request, item.result)
			if (err == nil) != want {
				t.Fatal("actual current read grant not honored", i, err)
			}
			if string(item.result.Content) != originalBytes {
				t.Fatal("original version replaced")
			}
		}
		if validations != expectedSaves {
			t.Fatal("saved result reading created an effect", validations)
		}
	}
	mutate(false, true)
	assertRead(true)
	assertDelivery(200)
	assertInspection(true)
	if _, e := selected.InvokeConversationTool(t.Context(), saveRequest); e == nil {
		t.Fatal("read role granted write execution")
	}
	if _, e := selected.ReconcileConversationTool(t.Context(), saveRequest); e == nil {
		t.Fatal("read role granted write reconciliation")
	}
	changed := saveRequest
	changed.IdempotencyKey = "wrong-original-save"
	if e := selected.(toolsdk.ResultReadAuthorizer).AuthorizeConversationToolResultRead(t.Context(), changed, saved); e == nil {
		t.Fatal("wrong original ledger key accepted")
	}
	mutate(true, true)
	assertRead(false)
	assertDelivery(403)
	assertInspection(false)
	mutate(false, true)
	assertRead(true)
	if e := host.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	host = nil
	open()
	call("POST", "/auth/login", map[string]string{"login": "admin@example.com", "password": "Record-Policy-Changed!2026"}, 200)
	session()
	assertRead(true)
	assertDelivery(200)
	assertInspection(true)
	if _, e := selected.InvokeConversationTool(t.Context(), saveRequest); e == nil {
		t.Fatal("restart restored write grants")
	}
	for name, cookie := range cookies {
		if strings.HasSuffix(name, "_access") {
			permissionToken = cookie.Value
		}
	}
	if permissionToken == "" {
		t.Fatal("actual administrator authoring token missing")
	}
	cookies, scope = map[string]*http.Cookie{}, ""
	call("POST", "/auth/login", map[string]string{"login": "system_administrator@example.com", "password": initial}, 200)
	call("POST", "/auth/password/change", map[string]string{"current_password": initial, "new_password": "Record-Reader-Changed!2026"}, 200)
	other := session()
	if !other.Known || other.UserID == owner.UserID || other.WorkspaceID != owner.WorkspaceID {
		t.Fatal("actual second record owner was not distinct", other)
	}
	mutateRole(other, false, true)
	for _, source := range []agent.ConversationToolRequest{readRequest, listRequest} {
		source.Authority = other
		grant, e := selected.AuthorizeConversationTool(t.Context(), source)
		if e != nil || !grant.Granted || grant.ConfirmationRequired {
			t.Fatal("actual second account lacked record read rights", source.Call.Name, e)
		}
	}
	policy := selected.(toolsdk.ResultReadAuthorizer)
	for _, item := range []struct {
		request agent.ConversationToolRequest
		result  agent.ConversationToolResult
	}{{saveRequest, saved}, {readRequest, readResult}, {listRequest, listResult}, {emptyRequest, emptyResult}} {
		item.request.Authority, item.request.ResultProducer = other, &owner
		err := policy.AuthorizeConversationToolResultRead(t.Context(), item.request, item.result)
		var denied *toolsdk.Error
		if !errors.As(err, &denied) || denied.Code != "tool.record.owner_mismatch" {
			t.Fatal("read-authorized second account consumed original personal receipt", item.request.Call.Name, err)
		}
	}
	foreignRead := readRequest
	foreignRead.Authority = other
	if _, e := selected.InvokeConversationTool(t.Context(), foreignRead); e == nil {
		t.Fatal("actual second reader queried first owner's original record")
	}
	foreignList := listRequest
	foreignList.Authority = other
	listed := invoke(foreignList)
	var ownPage contract.Page
	if json.Unmarshal(listed.Content, &ownPage) != nil || len(ownPage.Items) != 0 || !ownPage.Complete || validations != expectedSaves {
		t.Fatal("actual second account list exposed personal records or mutated data", listed)
	}
	t.Log("Actual second Identity account has read/list rights; original personal receipts and record are denied, its own list remains empty")
	renderer := host.Dialect().(query.Renderer)
	statement, args, e := query.NewSelectBuilder(renderer, "sqlite_master").Columns("name").Where(query.Equal("type", "table")).Build()
	if e != nil {
		t.Fatal(e)
	}
	rows, e := host.Database().QueryContext(t.Context(), statement, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	ledgers := 0
	for rows.Next() {
		var name string
		if e := rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(name, "schema_migrations") {
			if name != "_schema_migrations" {
				t.Fatal("module-owned migration ledger", name)
			}
			ledgers++
		}
	}
	if rows.Err() != nil || ledgers != 1 {
		t.Fatal("host migration ledger not unique", ledgers, rows.Err())
	}
}

func recordProductJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
