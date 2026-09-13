package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	tools "github.com/domainry/domainry-tools-sdk"
)

// This tests real Agent execution/projection against an explicit owner fixture.
// Real Runtime row, field and process policies are covered by the owner RPC test.
type peerBusinessReadSource struct {
	peerReportSource
	sdk.ConversationBusinessWorkflowSource
}

func (*peerBusinessReadSource) BusinessSourceIdentity() string { return "peer-business-read-owner" }
func (s *peerBusinessReadSource) invoke() error {
	if s.executionDenied.Load() {
		return &sdk.Error{Class: "forbidden", Code: "business_access_denied"}
	}
	s.invocations.Add(1)
	return nil
}
func businessReadRecord(id string) sdk.ConversationBusinessRecord {
	return sdk.ConversationBusinessRecord{ID: id, Version: "v1", Data: map[string]json.RawMessage{"amount": json.RawMessage(`9007199254740993`)}}
}
func (s *peerBusinessReadSource) BusinessCatalog(context.Context, sdk.ConversationBusinessCatalogQuery, sdk.ConversationAuthority) (sdk.ConversationBusinessCatalogPage, error) {
	return sdk.ConversationBusinessCatalogPage{Items: []sdk.ConversationBusinessObject{{Key: "customer", Version: "v1", Label: "客户"}}, Complete: true}, s.invoke()
}
func (s *peerBusinessReadSource) QueryBusinessRecords(_ context.Context, q sdk.ConversationBusinessQuery, _ sdk.ConversationAuthority) (sdk.ConversationBusinessRecordPage, error) {
	return sdk.ConversationBusinessRecordPage{ObjectKey: q.ObjectKey, Page: q.Page, PageSize: q.PageSize, Items: []sdk.ConversationBusinessRecord{businessReadRecord("customer-1")}}, s.invoke()
}
func (s *peerBusinessReadSource) GetBusinessRecord(_ context.Context, q sdk.ConversationBusinessGet, _ sdk.ConversationAuthority) (sdk.ConversationBusinessRecord, error) {
	return businessReadRecord(q.RecordID), s.invoke()
}
func (s *peerBusinessReadSource) QueryRelatedBusinessRecords(_ context.Context, q sdk.ConversationBusinessRelatedQuery, _ sdk.ConversationAuthority) (sdk.ConversationBusinessRelatedPage, error) {
	return sdk.ConversationBusinessRelatedPage{SourceObjectKey: q.ObjectKey, SourceRecordID: q.RecordID, RelationKey: q.RelationKey, ConversationBusinessRecordPage: sdk.ConversationBusinessRecordPage{ObjectKey: "order", Page: 1, PageSize: q.PageSize, Items: []sdk.ConversationBusinessRecord{businessReadRecord("order-1")}}}, s.invoke()
}
func (s *peerBusinessReadSource) AuthorizeWorkflowStart(context.Context, sdk.ConversationWorkflowStart, sdk.ConversationAuthority) (sdk.ConversationToolAuthorization, error) {
	return sdk.ConversationToolAuthorization{}, nil
}
func (s *peerBusinessReadSource) GetBusinessWorkflow(_ context.Context, q sdk.ConversationWorkflowGet, _ sdk.ConversationAuthority) (sdk.ConversationWorkflowState, error) {
	return sdk.ConversationWorkflowState{WorkflowKey: q.WorkflowKey, ProcessID: q.ProcessID, Name: "审核", Status: "waiting", CurrentSteps: []string{}}, s.invoke()
}
func (s *peerBusinessReadSource) SealBusinessEvidence(_ context.Context, e sdk.ConversationBusinessEvidence, a sdk.ConversationAuthority) (string, error) {
	e.HostProof = "owner-business-read-proof"
	s.attest("business", e, a)
	return e.HostProof, nil
}
func (s *peerBusinessReadSource) RevalidateBusiness(_ context.Context, e sdk.ConversationBusinessEvidence, a sdk.ConversationAuthority) error {
	return s.check("business", e, a, true)
}
func (s *peerBusinessReadSource) RevalidateBusinessWorkflow(ctx context.Context, e sdk.ConversationBusinessEvidence, a sdk.ConversationAuthority) error {
	return s.RevalidateBusiness(ctx, e, a)
}
func (s *peerBusinessReadSource) AuthorizeBusinessResultRead(_ context.Context, e sdk.ConversationBusinessEvidence, a sdk.ConversationAuthority) error {
	return s.check("business", e, a, false)
}

func TestPeerBusinessDeliveryReadsWithoutProfessionalToolExecution(t *testing.T) {
	testPeerBusinessDeliveryReading(t, false)
}

func testPeerBusinessDeliveryReading(t *testing.T, writeReceipts bool) {
	const initial, changed = "Business-Delivery-Initial!26", "Business-Delivery-Changed!26"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "business-delivery-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "business-delivery-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	source := &peerBusinessReadSource{}
	worker := &peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "business-review"}, summary: "已核对业务原值 9007199254740993，流程仍在等待审批", sourceCalls: []sdk.ConversationToolCall{
		{ID: "catalog", Name: "business_catalog", Arguments: `{"object_key":"customer"}`},
		{ID: "query", Name: "query_records", Arguments: `{"object_key":"customer","fields":["amount"]}`},
		{ID: "record", Name: "get_record", Arguments: `{"object_key":"customer","record_id":"customer-1","fields":["amount"]}`},
		{ID: "related", Name: "query_related_records", Arguments: `{"object_key":"customer","record_id":"customer-1","relation_key":"reverse:order:customer","fields":["amount"]}`},
		{ID: "process", Name: "workflow_get", Arguments: `{"workflow_key":"review","process_id":"process-1"}`},
	}}
	var business sdk.ConversationBusinessSource = source
	if writeReceipts {
		business = &peerBusinessWriteReadSource{peerBusinessReadSource: source}
		worker.summary = "业务动作已完成；流程已受理并等待审批，附原始回执"
		worker.sourceCalls = []sdk.ConversationToolCall{
			{ID: "action", Name: "invoke_action", Arguments: accountJSON(sdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: "customer.register", Version: "v1", Data: json.RawMessage(`{"name":"Confirmed customer"}`)})},
			{ID: "workflow", Name: "workflow_start", Arguments: accountJSON(sdk.ConversationWorkflowStart{WorkflowKey: "review", Version: "v1", Data: json.RawMessage(`{"reason":"Confirmed review"}`)})},
		}
	}
	options := Options{DatabasePath: filepath.Join(t.TempDir(), "business-delivery.db"), RuntimeID: "business-delivery-runtime", WorkspaceID: "business-delivery-workspace", ApplicationKey: "business-delivery-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{Business: business, AgentModels: map[string]sdk.ConversationModel{"business-review": worker}, Poll: 5 * time.Millisecond, MaxSteps: 12}}}
	var host *Host
	open := func() http.Handler {
		t.Helper()
		var err error
		host, err = Open(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		adapters, err := host.ToolSettingsAdapters()
		if err != nil {
			t.Fatal(err)
		}
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("business delivery")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	b := &browser{t: t, handler: open(), cookies: map[string]*http.Cookie{}}
	defer func() { _ = host.Close(context.Background()) }()
	b.login("admin@example.com", initial)
	b.changePassword(initial, changed)
	grantPersonalTools(t, host, b, true)
	grantCollaborationPermissions(t, host, b)
	definitions := append(sdk.BusinessConversationTools(), sdk.BusinessRelationConversationTools()...)
	definitions = append(definitions, sdk.BusinessWorkflowConversationTools()...)
	if writeReceipts {
		definitions = append(definitions, sdk.BusinessActionConversationTools()...)
	}
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		for _, d := range definitions {
			p = append(p, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
		}
		return p
	})
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	toolsList := []string{"business_catalog", "query_records", "get_record", "query_related_records", "workflow_get", "delegation_get", "delegation_update"}
	if writeReceipts {
		toolsList = []string{"invoke_action", "workflow_start", "delegation_get", "delegation_update"}
	}
	recipient := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", accountJSON(sdk.ConversationAgentWrite{ClientID: "business-review", Name: "业务核查", Description: "核对原回执", Instructions: "核对并提交准确来源", Tools: toolsList, SkillKeys: []string{}, ModelKey: "business-review", Enabled: true, MaxConcurrent: 1}), 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"business-delivery","title":"业务交付阅读"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "business-review-work", ConversationID: conversation.ID, AgentID: recipient.ID, Purpose: "核对业务", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对原始结果与流程", Deliverable: "准确原值及状态", CompletionConditions: []string{"附各项原始回执"}}}
	var detail sdk.ConversationDelegationDetail
	if err := unmarshalPeerDetail(b.call("POST", "/agent/delegations", accountJSON(input), 200).Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	path := "/agent/delegations/" + detail.ID
	read := func() sdk.ConversationDelegationDetail {
		t.Helper()
		var d sdk.ConversationDelegationDetail
		if err := unmarshalPeerDetail(b.call("GET", path, "", 200).Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// Race instrumentation also covers source audits and the completion inbox;
	// allow that bounded asynchronous work to settle before revoking grants.
	deadline := time.Now().Add(3 * time.Minute)
	confirmed := map[string]bool{}
	for {
		detail = read()
		if writeReceipts && detail.Task != nil && detail.Task.ExecutionRunID != "" {
			runPath := "/agent/conversations/" + detail.ConversationID + "/runs/" + detail.Task.ExecutionRunID
			run := accountDecode[sdk.ConversationRun](t, b.call("GET", runPath, "", 200))
			if run.Status == "waiting_confirmation" && run.Interaction != nil && !confirmed[run.Interaction.ID] {
				if source.invocations.Load() != int64(len(confirmed)) {
					t.Fatal("business effect preceded its confirmation")
				}
				confirmed[run.Interaction.ID] = true
				b.call("POST", runPath+"/respond", accountJSON(sdk.ConversationInteractionResponse{InteractionID: run.Interaction.ID, ClientID: "confirm-" + run.Interaction.CallID, ExpectedRevision: run.Interaction.Revision, Decision: "approve"}), 200)
			}
		}
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if detail.Status == "delivered" && detail.Task != nil && detail.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) || detail.Task != nil && detail.Task.Status == "failed" {
			t.Fatalf("business delivery failed: task=%+v root=%+v delegation=%+v", detail.Task, root, detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if detail.Delivery == nil || len(detail.Delivery.Conditions) != 1 || len(detail.Delivery.Conditions[0].Receipts) != len(worker.sourceCalls) || source.invocations.Load() != int64(len(worker.sourceCalls)) || writeReceipts && len(confirmed) != 2 {
		t.Fatalf("missing real tool delivery: %+v", detail)
	}
	refs := detail.Delivery.Conditions[0].Receipts
	preferenceKey := worker.sourceCalls[0].Name
	setting := settingList(t, b)[preferenceKey]
	b.call("PUT", "/tools/preferences/"+preferenceKey, accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("disabled business tool result visible")
	}
	setting = settingList(t, b)[preferenceKey]
	b.call("PUT", "/tools/preferences/"+preferenceKey, accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, grant := range p {
			if !strings.HasPrefix(grant.PermissionKey, sdk.ConversationToolActionPrefix) {
				out = append(out, grant)
			}
		}
		return out
	})
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	source.executionDenied.Store(true)
	assertRead := func() {
		t.Helper()
		d := read()
		if d.Delivery == nil || d.Task != nil || d.Delivery.Summary != worker.summary {
			t.Fatal("delivery hidden or execution leaked", d)
		}
		history := accountDecode[sdk.ConversationDeliveryHistory](t, b.call("GET", path+"/deliveries", "", 200))
		if len(history.Items) == 0 {
			t.Fatal("history missing")
		}
		for i, ref := range refs {
			for _, revision := range []int64{0, history.Items[0].Revision} {
				r := readReleasedResult(t, b, detail.ID, revision, ref)
				var e sdk.ConversationBusinessEvidence
				if json.Unmarshal(r.Content, &e) != nil || e.Operation != worker.sourceCalls[i].Name {
					t.Fatal("wrong original receipt", r)
				}
				if !writeReceipts && i > 0 && i < 4 && !strings.Contains(string(e.Data), "9007199254740993") {
					t.Fatal("integer precision lost")
				}
				if writeReceipts {
					want := []string{`"status":"completed"`, `"status":"accepted"`}[i]
					if !strings.Contains(string(e.Data), want) || string(e.Input) != worker.sourceCalls[i].Arguments {
						t.Fatal("original write receipt/input changed", e)
					}
				}
			}
		}
		if source.invocations.Load() != int64(len(worker.sourceCalls)) {
			t.Fatal("reading reexecuted business tools")
		}
	}
	assertRead()
	for _, ref := range refs {
		run := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", run, "", 403)
		b.call("POST", run+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	}
	source.readingDenied.Store(true)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("owner revocation leaked values")
	}
	b.call("POST", path+"/delivery-result", accountJSON(sdk.ConversationDeliveryResultRead{ConversationResultRead: sdk.ConversationResultRead{Reference: refs[0], MaxBytes: 4096}}), 403)
	source.readingDenied.Store(false)
	assertRead()
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	assertRead()
}
