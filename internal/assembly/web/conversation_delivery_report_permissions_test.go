package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
	webhttp "github.com/domainry/domainry-agent/internal/transport/http/web"
	agentmodule "github.com/domainry/domainry-agent/module"
	identity "github.com/domainry/domainry-identity-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	tools "github.com/domainry/domainry-tools-sdk"
)

// This boundary fixture is separate from the real Report/Runtime RPC scenario.
// It records exact owner-issued values so the HTTP test can distinguish read
// authorization from rediscovery/execution through the product composition.
type peerReportSource struct {
	sdk.ConversationBusinessSource
	executionDenied, readingDenied atomic.Bool
	invocations, catalogs          atomic.Int64
	mu                             sync.Mutex
	issued                         map[string]bool
}

func (*peerReportSource) BusinessSourceIdentity() string { return "peer-report-owner" }
func (s *peerReportSource) attest(kind string, in any, a sdk.ConversationAuthority) {
	raw, _ := json.Marshal([]any{kind, in, a})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.issued == nil {
		s.issued = map[string]bool{}
	}
	s.issued[string(raw)] = true
}
func (s *peerReportSource) check(kind string, in any, a sdk.ConversationAuthority, execution bool) error {
	if s.readingDenied.Load() || execution && s.executionDenied.Load() {
		return &tools.Error{Class: "forbidden", Code: "source.access_denied"}
	}
	raw, _ := json.Marshal([]any{kind, in, a})
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.issued[string(raw)] {
		return &tools.Error{Class: "forbidden", Code: "source.result_invalid"}
	}
	return nil
}
func (s *peerReportSource) ReportCatalog(_ context.Context, in model.ReportCatalogRequest, a sdk.ConversationAuthority) (model.ReportCatalog, error) {
	s.catalogs.Add(1)
	if s.executionDenied.Load() {
		return model.ReportCatalog{}, &tools.Error{Class: "forbidden", Code: "source.execution_denied"}
	}
	out := model.ReportCatalog{Reports: []model.ReportCatalogEntry{{Key: "sales", Name: "销售报表", DefinitionVersion: "v1", RowLimit: 100}}, ReadProof: "owner-read-proof"}
	s.attest("report-catalog", model.ReportCatalogReadAuthorization{Request: in, Result: out}, a)
	return out, nil
}
func (s *peerReportSource) QueryReport(_ context.Context, in model.ReportObjectSQLRequest, a sdk.ConversationAuthority) (model.ReportQueryResult, error) {
	if s.executionDenied.Load() {
		return model.ReportQueryResult{}, &tools.Error{Class: "forbidden", Code: "source.execution_denied"}
	}
	s.invocations.Add(1)
	out := model.ReportQueryResult{Summary: model.ReportSummary{Key: in.ReportKey, Name: "销售报表", ExecutionMode: "object_sql_v1", Rows: []model.ReportResultRow{{Measures: map[string]string{"amount": "60"}}}, RowCount: 1}, Source: model.ReportQuerySource{ReportKey: in.ReportKey, DefinitionVersion: "v1", DataVersion: "data1", Proof: "owner-execution-proof", ReadProof: "owner-read-proof", Complete: true}}
	s.attest("report", model.ReportQueryResultAuthorization{Query: in, Result: out}, a)
	return out, nil
}
func (s *peerReportSource) AnalysisCatalog(_ context.Context, in model.AnalysisCatalogRequest, a sdk.ConversationAuthority) (model.AnalysisCatalog, error) {
	s.catalogs.Add(1)
	if s.executionDenied.Load() {
		return model.AnalysisCatalog{}, &tools.Error{Class: "forbidden", Code: "source.execution_denied"}
	}
	out := model.AnalysisCatalog{Datasets: []model.AnalysisDataset{{Key: "sales", Name: "销售明细", Kind: "business_object", Version: "v1", Columns: []model.AnalysisColumn{{Key: "amount", Type: "integer"}}}}, ReadProof: "owner-read-proof"}
	s.attest("analysis-catalog", model.AnalysisCatalogReadAuthorization{Request: in, Result: out}, a)
	return out, nil
}
func (s *peerReportSource) RunAnalysis(_ context.Context, in model.AnalysisRequest, a sdk.ConversationAuthority) (model.AnalysisResult, error) {
	if s.executionDenied.Load() {
		return model.AnalysisResult{}, &tools.Error{Class: "forbidden", Code: "source.execution_denied"}
	}
	s.invocations.Add(1)
	total := "60"
	out := model.AnalysisResult{Spec: in, Columns: []model.AnalysisColumn{{Key: "total", Type: "integer"}}, Rows: []model.AnalysisRow{{Values: map[string]*string{"total": &total}}}, Coverage: model.AnalysisCoverage{Complete: true, ReturnedRows: 1, RequestedMaxRows: in.MaxRows, Missing: []model.AnalysisMissing{}}, Visualization: model.AnalysisVisualization{OmittedReason: "single_value"}, References: []model.AnalysisReference{{Kind: "business_object", ID: "sales", Version: "v1"}}, Source: model.AnalysisSource{DatasetKey: "sales", DefinitionVersion: "v1", DataVersion: "data1", QueriedAt: "2026-09-13T00:00:00Z", Complete: true, Proof: "owner-execution-proof", ReadProof: "owner-read-proof"}}
	s.attest("analysis", model.AnalysisResultAuthorization{Request: in, Result: out}, a)
	return out, nil
}
func (s *peerReportSource) AuthorizeReportResult(_ context.Context, in model.ReportQueryResultAuthorization, a sdk.ConversationAuthority) error {
	return s.check("report", in, a, true)
}
func (s *peerReportSource) AuthorizeReportResultRead(_ context.Context, in model.ReportQueryResultAuthorization, a sdk.ConversationAuthority) error {
	return s.check("report", in, a, false)
}
func (s *peerReportSource) AuthorizeReportCatalogRead(_ context.Context, in model.ReportCatalogReadAuthorization, a sdk.ConversationAuthority) error {
	return s.check("report-catalog", in, a, false)
}
func (s *peerReportSource) AuthorizeAnalysisResult(_ context.Context, in model.AnalysisResultAuthorization, a sdk.ConversationAuthority) error {
	return s.check("analysis", in, a, true)
}
func (s *peerReportSource) AuthorizeAnalysisResultRead(_ context.Context, in model.AnalysisResultAuthorization, a sdk.ConversationAuthority) error {
	return s.check("analysis", in, a, false)
}
func (s *peerReportSource) AuthorizeAnalysisCatalogRead(_ context.Context, in model.AnalysisCatalogReadAuthorization, a sdk.ConversationAuthority) error {
	return s.check("analysis-catalog", in, a, false)
}

type peerReceiptDeliveryModel struct {
	peerWebModel
	sourceCalls []sdk.ConversationToolCall
	summary     string
}

func (m *peerReceiptDeliveryModel) StreamConversationStep(ctx context.Context, in sdk.ConversationStepRequest, emit func(sdk.ConversationModelEvent) error) (sdk.ConversationStepResult, error) {
	id, delivered := "", false
	var detail sdk.ConversationDelegationDetail
	receipts := map[string]sdk.ConversationResultReference{}
	for _, message := range in.Messages {
		if match := regexp.MustCompile(`Delegation ID: (delegation_[a-z0-9]+)`).FindStringSubmatch(message.Content); len(match) > 1 {
			id = match[1]
		}
		if message.Role != "tool" {
			continue
		}
		var wire struct {
			sdk.ConversationToolResult
			Reference *sdk.ConversationResultReference `json:"reference"`
		}
		if json.Unmarshal([]byte(message.Content), &wire) != nil || wire.Status != "completed" {
			continue
		}
		if wire.Reference != nil {
			receipts[message.ToolCallID] = *wire.Reference
		}
		if message.ToolCallID == "agreement" {
			_ = unmarshalPeerDetail(wire.Content, &detail)
		}
		delivered = delivered || message.ToolCallID == "deliver"
	}
	if id == "" || delivered {
		return m.peerWebModel.StreamConversationStep(ctx, in, emit)
	}
	calls := []sdk.ConversationToolCall{
		{ID: "report-catalog", Name: "report_query", Arguments: `{"operation":"catalog"}`},
		{ID: "report-query", Name: "report_query", Arguments: `{"operation":"query","report_key":"sales"}`},
		{ID: "analysis-catalog", Name: "analysis_run", Arguments: `{"operation":"catalog","dataset_key":"sales"}`},
		{ID: "analysis-result", Name: "analysis_run", Arguments: `{"operation":"run","spec":{"dataset_key":"sales","measures":[{"key":"total","function":"sum","field":"amount"}]}}`},
	}
	if m.sourceCalls != nil {
		calls = m.sourceCalls
	}
	var next sdk.ConversationToolCall
	refs := []sdk.ConversationResultReference{}
	for _, call := range calls {
		ref, ok := receipts[call.ID]
		if !ok {
			next = call
			break
		}
		refs = append(refs, ref)
	}
	if next.ID == "" && detail.ID == "" {
		next = sdk.ConversationToolCall{ID: "agreement", Name: "delegation_get", Arguments: accountJSON(map[string]string{"id": id})}
	}
	if next.ID == "" {
		summary := m.summary
		if summary == "" {
			summary = "报表和分析均为 60，附四份来源回执"
		}
		next = sdk.ConversationToolCall{ID: "deliver", Name: "delegation_update", Arguments: accountJSON(map[string]any{"id": id, "update": map[string]any{"expected_revision": detail.Revision, "action": "deliver", "reason": "已核对原工具回执", "delivery": sdk.ConversationDelegationDelivery{BriefVersion: detail.Brief.Version, AgreementRevision: detail.AgreementRevision, Summary: summary, Conditions: []sdk.ConversationConditionAssessment{{Condition: 0, Verdict: "met", Basis: "核对保存的原工具回执", Receipts: refs}}, Data: json.RawMessage(`{"verified":true}`), Evidence: []sdk.ConversationRunReference{}, Unresolved: []string{}}}})}
	}
	return sdk.ConversationStepResult{FinishReason: "tool_calls", Message: sdk.ConversationStepMessage{Role: "assistant", ToolCalls: []sdk.ConversationToolCall{next}}}, nil
}

func TestPeerReportDeliveryUsesReadingPolicyWithoutExecutableCatalog(t *testing.T) {
	const initial, changed = "Peer-Report-Initial!2026", "Peer-Report-Changed!2026"
	t.Setenv("AUTH_DEFAULT_PASSWORD", initial)
	t.Setenv("AUTH_JWT_SECRET", "peer-report-identity-signing-key-long-enough")
	t.Setenv("IDENTITY_DATA_SECRET_KEY", "peer-report-identity-data-key-long-enough")
	t.Setenv("APP_ENV", "development")
	source := &peerReportSource{}
	worker := &peerReceiptDeliveryModel{peerWebModel: peerWebModel{modelKey: "report-review"}}
	options := Options{ReportTools: true, AnalysisTools: true, DatabasePath: filepath.Join(t.TempDir(), "peer-report.db"), RuntimeID: "report-runtime", WorkspaceID: "report-workspace", ApplicationKey: "report-app", Agent: agentmodule.Options{ConversationProvider: &peerWebModel{}, ConversationOptions: agentmodule.ConversationOptions{Business: source, AgentModels: map[string]sdk.ConversationModel{"report-review": worker}, Poll: 5 * time.Millisecond, MaxSteps: 12}}}
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
		h, err := webhttp.NewHandler(webhttp.Options{Identity: host.Identity, Agent: host.Agent, RuntimeID: options.RuntimeID, WorkspaceID: options.WorkspaceID, ApplicationKey: options.ApplicationKey, Origin: "http://127.0.0.1:8091", Model: "peer", Files: fstest.MapFS{"index.html": {Data: []byte("report collaboration")}}, ModuleAdapters: adapters, ApplicationRoutes: host.ToolSettingsSetupRoutes()})
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
	definitions := append(tools.ReportQueryDefinitions(), tools.AnalysisDefinitions()...)
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		for _, d := range definitions {
			p = append(p, identity.ProjectRolePermission{PermissionKey: d.ActionKey, DataScope: identity.DataScopeOwner})
		}
		return p
	})
	b.call("POST", "/app/product/tool-settings-setup", `{}`, 200)
	recipient := accountDecode[sdk.ConversationAgent](t, b.call("POST", "/agent/agents", `{"client_id":"report-review","name":"报表核查","description":"核查报表和分析","instructions":"核查并提供来源","tools":["report_query","analysis_run","delegation_get","delegation_update"],"skill_keys":[],"model_key":"report-review","enabled":true,"max_concurrent":1}`, 200))
	conversation := accountDecode[sdk.Conversation](t, b.call("POST", "/agent/conversations", `{"client_id":"report-delivery","title":"报表成果读取"}`, 200))
	input := sdk.ConversationDelegationCreate{ClientID: "report-review-work", ConversationID: conversation.ID, AgentID: recipient.ID, Purpose: "核对报表", Brief: sdk.ConversationTaskBrief{Version: 1, Goal: "核对销售总额", Deliverable: "提供报表和分析对照", CompletionConditions: []string{"报表和分析均附来源"}}}
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
	deadline := time.Now().Add(90 * time.Second)
	for {
		detail = read()
		root := accountDecode[sdk.Conversation](t, b.call("GET", "/agent/conversations/"+conversation.ID, "", 200))
		if detail.Status == "delivered" && detail.Task != nil && detail.Task.Status == "completed" && root.ActiveRunID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("report delivery did not finish: %+v", detail)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if detail.Delivery == nil || len(detail.Delivery.Conditions) != 1 || len(detail.Delivery.Conditions[0].Receipts) != 4 || source.invocations.Load() != 2 {
		t.Fatalf("missing real worker delivery: %+v", detail)
	}
	refs := detail.Delivery.Conditions[0].Receipts
	for _, key := range []string{"report_query", "analysis_run"} {
		setting := settingList(t, b)[key]
		b.call("PUT", "/tools/preferences/"+key, accountJSON(tools.ToolSettingInput{Enabled: false, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
		if d := read(); d.Delivery != nil || d.Verification != nil {
			t.Fatal("disabled tool's source remained readable")
		}
		setting = settingList(t, b)[key]
		b.call("PUT", "/tools/preferences/"+key, accountJSON(tools.ToolSettingInput{Enabled: true, ExpectedRevision: setting.Revision, ToolVersion: setting.Version}), 200)
	}
	mutateTestRolePermissions(t, host, b, func(p []identity.ProjectRolePermission) []identity.ProjectRolePermission {
		out := []identity.ProjectRolePermission{}
		for _, grant := range p {
			if grant.PermissionKey != definitions[0].ActionKey && grant.PermissionKey != definitions[1].ActionKey {
				out = append(out, grant)
			}
		}
		return out
	})
	setTestCollaborationPermissions(t, host, b, "view", "delivery_read")
	source.executionDenied.Store(true)
	catalogs := source.catalogs.Load()
	assertRead := func() {
		t.Helper()
		d := read()
		if d.Delivery == nil || !strings.Contains(d.Delivery.Summary, "均为 60") || d.Task != nil || len(d.Messages) != 0 {
			t.Fatalf("delivery hidden or execution leaked: %+v", d)
		}
		if body := b.call("GET", path+"/deliveries", "", 200).Body.String(); !strings.Contains(body, "均为 60") {
			t.Fatal("delivery history lost report sources")
		}
		if source.invocations.Load() != 2 || source.catalogs.Load() != catalogs {
			t.Fatal("reading rediscovered executable catalog or reexecuted owner")
		}
	}
	assertRead()
	for _, ref := range refs {
		if result := readReleasedResult(t, b, detail.ID, 0, ref); result.Status != "completed" {
			t.Fatal("released original receipt unavailable without execution rights")
		}
	}
	for _, ref := range refs {
		run := "/agent/conversations/" + ref.ConversationID + "/runs/" + ref.RunID
		b.call("GET", run, "", 403)
		b.call("POST", run+"/result", accountJSON(sdk.ConversationResultRead{Reference: ref, MaxBytes: 4096}), 403)
	}
	if err := host.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.handler = open()
	b.login("admin@example.com", changed)
	// Startup may discover current capabilities. Delivery reads themselves must
	// still avoid execution catalogs and preserve the saved result references.
	catalogs = source.catalogs.Load()
	assertRead()
	source.readingDenied.Store(true)
	if d := read(); d.Delivery != nil || d.Verification != nil {
		t.Fatal("explicit owner revocation exposed report values")
	}
	if body := b.call("GET", path+"/deliveries", "", 403).Body.String(); strings.Contains(body, "均为 60") {
		t.Fatal("revoked owner remained visible through history")
	}
	source.readingDenied.Store(false)
	assertRead()
	t.Log("Agent/Identity HTTP and durable worker: four owner-attested Report/Analysis result references, tool preferences enforced, delivery/history readable without execution catalog or tool Action, raw execution denied, restart preserved, owner read denial hid delivery")
}
