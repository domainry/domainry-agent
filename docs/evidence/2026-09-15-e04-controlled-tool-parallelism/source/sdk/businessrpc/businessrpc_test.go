package businessrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/domainry/domainry-agent-sdk"
)

var testScope = Scope{RuntimeID: "runtime-a", WorkspaceID: "workspace-a", ApplicationKey: "business-app", IdentityIssuer: "https://identity.example.test"}
var testAuthority = sdk.ConversationAuthority{Known: true, RuntimeID: "runtime-a", WorkspaceID: "workspace-a", UserID: "reader-a", RoleKey: "reader"}

const testToken = "private-test-business-service-token"
const testSource = "business-source-a"

type backendFixture struct {
	active, allow    atomic.Bool
	mu               sync.Mutex
	calls            map[string]int
	effects          map[string]sdk.ConversationBusinessActionResult
	fail             bool
	block            bool
	entered, stopped chan struct{}
}

func newBackend() *backendFixture {
	b := &backendFixture{calls: map[string]int{}, effects: map[string]sdk.ConversationBusinessActionResult{}, entered: make(chan struct{}), stopped: make(chan struct{})}
	b.active.Store(true)
	b.allow.Store(true)
	return b
}
func (b *backendFixture) hit(key string)                 { b.mu.Lock(); b.calls[key]++; b.mu.Unlock() }
func (b *backendFixture) count(key string) int           { b.mu.Lock(); defer b.mu.Unlock(); return b.calls[key] }
func (b *backendFixture) BusinessSourceIdentity() string { return testSource }
func (b *backendFixture) AuthorizeConversationExecution(context.Context, sdk.ConversationExecutionAuthorizationRequest) (bool, error) {
	b.hit("identity")
	return b.active.Load(), nil
}
func (b *backendFixture) AuthorizeConversationTool(_ context.Context, r sdk.ConversationToolRequest) (sdk.ConversationToolAuthorization, error) {
	b.hit("tool")
	return sdk.ConversationToolAuthorization{Granted: b.allow.Load(), Revision: "policy-v2"}, nil
}
func (b *backendFixture) BusinessCatalog(context.Context, sdk.ConversationBusinessCatalogQuery, sdk.ConversationAuthority) (sdk.ConversationBusinessCatalogPage, error) {
	b.hit("catalog")
	return sdk.ConversationBusinessCatalogPage{Items: []sdk.ConversationBusinessObject{{Key: "customer", Label: "Customers"}}}, nil
}
func (b *backendFixture) QueryBusinessRecords(context.Context, sdk.ConversationBusinessQuery, sdk.ConversationAuthority) (sdk.ConversationBusinessRecordPage, error) {
	b.hit("query")
	return sdk.ConversationBusinessRecordPage{Items: []sdk.ConversationBusinessRecord{{ID: "record-a", Data: map[string]json.RawMessage{"amount": json.RawMessage(`9007199254740993`)}}}}, nil
}
func (b *backendFixture) GetBusinessRecord(ctx context.Context, _ sdk.ConversationBusinessGet, _ sdk.ConversationAuthority) (sdk.ConversationBusinessRecord, error) {
	b.hit("get")
	if b.block {
		close(b.entered)
		<-ctx.Done()
		close(b.stopped)
		return sdk.ConversationBusinessRecord{}, ctx.Err()
	}
	if b.fail {
		return sdk.ConversationBusinessRecord{}, fmt.Errorf("private-token https://private.example.com/details")
	}
	return sdk.ConversationBusinessRecord{ID: "record-a", Version: "v1", Data: map[string]json.RawMessage{"name": json.RawMessage(`"Visible"`)}}, nil
}
func (b *backendFixture) QueryRelatedBusinessRecords(context.Context, sdk.ConversationBusinessRelatedQuery, sdk.ConversationAuthority) (sdk.ConversationBusinessRelatedPage, error) {
	b.hit("related")
	return sdk.ConversationBusinessRelatedPage{SourceObjectKey: "customer", SourceRecordID: "record-a", RelationKey: "orders", ConversationBusinessRecordPage: sdk.ConversationBusinessRecordPage{Items: []sdk.ConversationBusinessRecord{}}}, nil
}
func (b *backendFixture) RevalidateBusiness(context.Context, sdk.ConversationBusinessEvidence, sdk.ConversationAuthority) error {
	b.hit("revalidate")
	return nil
}
func (b *backendFixture) SealBusinessEvidence(context.Context, sdk.ConversationBusinessEvidence, sdk.ConversationAuthority) (string, error) {
	b.hit("seal")
	return "snapshot-proof", nil
}
func (b *backendFixture) AuthorizeBusinessAction(context.Context, sdk.ConversationBusinessAction, sdk.ConversationAuthority) (sdk.ConversationToolAuthorization, error) {
	b.hit("action_authorize")
	return sdk.ConversationToolAuthorization{Granted: true}, nil
}
func (b *backendFixture) InvokeBusinessAction(_ context.Context, r sdk.ConversationBusinessActionRequest) (sdk.ConversationBusinessActionResult, error) {
	b.hit("action_invoke")
	b.mu.Lock()
	defer b.mu.Unlock()
	if prior, ok := b.effects[r.IdempotencyKey]; ok {
		return prior, nil
	}
	result := sdk.ConversationBusinessActionResult{Status: "completed", InvocationID: "receipt-a", ObjectKey: r.Action.ObjectKey, ActionKey: r.Action.ActionKey}
	b.effects[r.IdempotencyKey] = result
	return result, nil
}
func (b *backendFixture) ReconcileBusinessAction(_ context.Context, r sdk.ConversationBusinessActionRequest) (sdk.ConversationBusinessActionResult, error) {
	b.hit("action_reconcile")
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.effects[r.IdempotencyKey], nil
}
func (b *backendFixture) RevalidateBusinessAction(context.Context, sdk.ConversationBusinessEvidence, sdk.ConversationAuthority) error {
	b.hit("action_revalidate")
	return nil
}
func (b *backendFixture) AuthorizeWorkflowStart(context.Context, sdk.ConversationWorkflowStart, sdk.ConversationAuthority) (sdk.ConversationToolAuthorization, error) {
	b.hit("workflow_authorize")
	return sdk.ConversationToolAuthorization{Granted: true}, nil
}
func (b *backendFixture) StartBusinessWorkflow(context.Context, sdk.ConversationWorkflowStartRequest) (sdk.ConversationWorkflowReceipt, error) {
	b.hit("workflow_start")
	return sdk.ConversationWorkflowReceipt{Status: "accepted", InvocationID: "start-a", ProcessID: "process-a"}, nil
}
func (b *backendFixture) ReconcileBusinessWorkflow(context.Context, sdk.ConversationWorkflowStartRequest) (sdk.ConversationWorkflowReceipt, error) {
	b.hit("workflow_reconcile")
	return sdk.ConversationWorkflowReceipt{Status: "accepted", InvocationID: "start-a", ProcessID: "process-a"}, nil
}
func (b *backendFixture) GetBusinessWorkflow(context.Context, sdk.ConversationWorkflowGet, sdk.ConversationAuthority) (sdk.ConversationWorkflowState, error) {
	b.hit("workflow_get")
	return sdk.ConversationWorkflowState{WorkflowKey: "review", ProcessID: "process-a", Status: "waiting", Terminal: false}, nil
}
func (b *backendFixture) RevalidateBusinessWorkflow(context.Context, sdk.ConversationBusinessEvidence, sdk.ConversationAuthority) error {
	b.hit("workflow_revalidate")
	return nil
}

func fixture(t *testing.T, b *backendFixture, wrap func(http.Handler) http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	handler, err := NewHandler(ServerOptions{Scope: testScope, Token: testToken, Backend: b})
	if err != nil {
		t.Fatal(err)
	}
	if wrap != nil {
		handler = wrap(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := Open(t.Context(), ClientOptions{BaseURL: server.URL, Token: testToken, Scope: testScope, ExpectedSourceIdentity: testSource, ExpectedContractSHA256: ContractSHA256()})
	if err != nil {
		t.Fatal(err)
	}
	return c, server
}
func actionRequest() sdk.ConversationBusinessActionRequest {
	return sdk.ConversationBusinessActionRequest{Authority: testAuthority, Action: sdk.ConversationBusinessAction{ObjectKey: "customer", ActionKey: "create", Version: "v1", Data: json.RawMessage(`{"name":"Visible"}`)}, ConversationID: "conversation-a", RunID: "run-a", CorrelationID: "run-a", CallID: "call-a", IdempotencyKey: "key-a", Arguments: `{"name":"Visible"}`, Confirmation: &sdk.ConversationConfirmation{ID: "confirmation-a", UserID: testAuthority.UserID, ApprovedAt: time.Now().UTC()}}
}

func TestHTTPBusinessProfilePreservesAllPorts(t *testing.T) {
	b := newBackend()
	c, _ := fixture(t, b, nil)
	a := testAuthority
	ctx := t.Context()
	if c.BusinessSourceIdentity() != testSource || c.Descriptor().Scope != testScope {
		t.Fatal("binding changed")
	}
	catalog, err := c.BusinessCatalog(ctx, sdk.ConversationBusinessCatalogQuery{}, a)
	if err != nil || len(catalog.Items) != 1 {
		t.Fatal(catalog, err)
	}
	page, err := c.QueryBusinessRecords(ctx, sdk.ConversationBusinessQuery{ObjectKey: "customer"}, a)
	if err != nil || string(page.Items[0].Data["amount"]) != "9007199254740993" {
		t.Fatal("numeric precision", page, err)
	}
	record, err := c.GetBusinessRecord(ctx, sdk.ConversationBusinessGet{ObjectKey: "customer", RecordID: "record-a"}, a)
	if err != nil || record.ID != "record-a" {
		t.Fatal(record, err)
	}
	related, err := c.QueryRelatedBusinessRecords(ctx, sdk.ConversationBusinessRelatedQuery{ObjectKey: "customer", RecordID: "record-a", RelationKey: "orders"}, a)
	if err != nil || related.RelationKey != "orders" {
		t.Fatal(related, err)
	}
	evidence := sdk.ConversationBusinessEvidence{Version: 1, Source: testSource, Operation: "get_record", Input: json.RawMessage(`{}`), Data: json.RawMessage(`{}`)}
	if err := c.RevalidateBusiness(ctx, evidence, a); err != nil {
		t.Fatal(err)
	}
	proof, err := c.SealBusinessEvidence(ctx, evidence, a)
	if err != nil || proof != "snapshot-proof" {
		t.Fatal(proof, err)
	}
	action := actionRequest()
	auth, err := c.AuthorizeBusinessAction(ctx, action.Action, a)
	if err != nil || !auth.Granted {
		t.Fatal(auth, err)
	}
	receipt, err := c.InvokeBusinessAction(ctx, action)
	if err != nil || receipt.Status != "completed" {
		t.Fatal(receipt, err)
	}
	replayed, err := c.ReconcileBusinessAction(ctx, action)
	if err != nil || replayed.InvocationID != receipt.InvocationID {
		t.Fatal(replayed, err)
	}
	evidence.Operation = "invoke_action"
	if err := c.RevalidateBusinessAction(ctx, evidence, a); err != nil {
		t.Fatal(err)
	}
	start := sdk.ConversationWorkflowStartRequest{Authority: a, Start: sdk.ConversationWorkflowStart{WorkflowKey: "review", Version: "v1", Data: json.RawMessage(`{}`)}, RunID: "run-a", IdempotencyKey: "workflow-a"}
	auth, err = c.AuthorizeWorkflowStart(ctx, start.Start, a)
	if err != nil || !auth.Granted {
		t.Fatal(auth, err)
	}
	accepted, err := c.StartBusinessWorkflow(ctx, start)
	if err != nil || accepted.Status != "accepted" {
		t.Fatal(accepted, err)
	}
	accepted, err = c.ReconcileBusinessWorkflow(ctx, start)
	if err != nil || accepted.ProcessID != "process-a" {
		t.Fatal(accepted, err)
	}
	state, err := c.GetBusinessWorkflow(ctx, sdk.ConversationWorkflowGet{WorkflowKey: "review", ProcessID: "process-a"}, a)
	if err != nil || state.Terminal || state.Status != "waiting" {
		t.Fatal(state, err)
	}
	evidence.Operation = "workflow_get"
	if err := c.RevalidateBusinessWorkflow(ctx, evidence, a); err != nil {
		t.Fatal(err)
	}
	d, _ := definition("get_record")
	auth, err = c.AuthorizeConversationTool(ctx, sdk.ConversationToolRequest{Authority: a, Definition: d})
	if err != nil || !auth.Granted {
		t.Fatal(auth, err)
	}
	for _, m := range methods {
		key := m.key
		if key == "tool_authorize" || key == "business_result_read" || reportOperation(key) || analysisOperation(key) || sharedResultReadOperation(key) {
			continue
		}
		if b.count(key) != 1 {
			t.Errorf("%s count %d", key, b.count(key))
		}
	}
	if b.count("identity") != 16 || b.count("tool") != 16 {
		t.Fatalf("not checked every call %d/%d", b.count("identity"), b.count("tool"))
	}
	t.Log("16 actual HTTP methods; current identity/tool checks each time; 64-bit JSON numbers retained; no retry; workflow accepted remains nonterminal")
}

func TestCurrentPolicyAndTransportAdmission(t *testing.T) {
	b := newBackend()
	c, server := fixture(t, b, nil)
	a := testAuthority
	b.allow.Store(false)
	if _, err := c.GetBusinessRecord(t.Context(), sdk.ConversationBusinessGet{}, a); err == nil {
		t.Fatal("tool revoke accepted")
	}
	if b.count("get") != 0 {
		t.Fatal("revoked call dispatched")
	}
	b.allow.Store(true)
	b.active.Store(false)
	if _, err := c.GetBusinessRecord(t.Context(), sdk.ConversationBusinessGet{}, a); err == nil {
		t.Fatal("inactive subject accepted")
	}
	b.active.Store(true)
	other := a
	other.WorkspaceID = "other"
	if _, err := c.GetBusinessRecord(t.Context(), sdk.ConversationBusinessGet{}, other); err == nil {
		t.Fatal("cross workspace accepted")
	}
	req := request{Descriptor: c.descriptor, Operation: "get", Authority: a, Payload: json.RawMessage(`{}`)}
	direct := func(in request, token string, status int) {
		t.Helper()
		raw, _ := json.Marshal(in)
		r, _ := http.NewRequest("POST", server.URL+CallPath, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		res, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("got %d want %d", res.StatusCode, status)
		}
	}
	direct(req, "wrong-token", 401)
	req.Authority = other
	direct(req, testToken, 403)
	req.Authority = a
	req.Descriptor.SourceIdentity = "replacement"
	direct(req, testToken, 409)
	req.Descriptor = c.descriptor
	req.Descriptor.Scope.IdentityIssuer = "https://different-identity.example.test"
	direct(req, testToken, 409)
	req.Descriptor = c.descriptor
	req.Operation = "unknown"
	direct(req, testToken, 400)
	req.Operation = "action_invoke"
	nested := actionRequest()
	nested.Authority = other
	req.Payload, _ = json.Marshal(nested)
	direct(req, testToken, 400)
	req.Operation = "get"
	req.Payload = json.RawMessage(`{"record_id":"r","sql":"SELECT private"}`)
	direct(req, testToken, 400)
	if b.count("get") != 0 || b.count("action_invoke") != 0 {
		t.Fatal("invalid request reached business")
	}
	b.fail = true
	_, err := c.GetBusinessRecord(t.Context(), sdk.ConversationBusinessGet{}, a)
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("private error escaped", err)
	}
	if _, err := Open(t.Context(), ClientOptions{BaseURL: server.URL, Token: testToken, Scope: testScope, ExpectedSourceIdentity: "replacement", ExpectedContractSHA256: ContractSHA256()}); err == nil {
		t.Fatal("source mismatch accepted")
	}
	for _, issuer := range []string{"", "https://different-identity.example.test"} {
		scope := testScope
		scope.IdentityIssuer = issuer
		if _, err := Open(t.Context(), ClientOptions{BaseURL: server.URL, Token: testToken, Scope: scope, ExpectedSourceIdentity: testSource, ExpectedContractSHA256: ContractSHA256()}); err == nil {
			t.Fatal("missing or mismatched Identity issuer accepted")
		}
	}
}

func TestCancellationAndLostWriteResponse(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		b := newBackend()
		b.block = true
		c, _ := fixture(t, b, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { _, err := c.GetBusinessRecord(ctx, sdk.ConversationBusinessGet{}, testAuthority); done <- err }()
		select {
		case <-b.entered:
		case <-time.After(time.Second):
			t.Fatal("not dispatched")
		}
		cancel()
		select {
		case <-b.stopped:
		case <-time.After(time.Second):
			t.Fatal("context not propagated")
		}
		if <-done == nil {
			t.Fatal("cancel succeeded")
		}
	})
	t.Run("unknown-write", func(t *testing.T) {
		b := newBackend()
		var dropped atomic.Bool
		c, _ := fixture(t, b, func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == CallPath && !dropped.Swap(true) {
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, r)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				h.ServeHTTP(w, r)
			})
		})
		receipt, err := c.InvokeBusinessAction(t.Context(), actionRequest())
		if err != nil || receipt.Status != "uncertain" || b.count("action_invoke") != 1 {
			t.Fatal(receipt, err, b.count("action_invoke"))
		}
		replay, err := c.ReconcileBusinessAction(t.Context(), actionRequest())
		if err != nil || replay.Status != "completed" || b.count("action_invoke") != 1 {
			t.Fatal("reconciliation repeated write", replay, err)
		}
	})
}

func TestRedirectAndBounds(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer redirect.Close()
	o := ClientOptions{BaseURL: redirect.URL, Token: testToken, Scope: testScope, ExpectedSourceIdentity: testSource, ExpectedContractSHA256: ContractSHA256()}
	if _, err := Open(t.Context(), o); err == nil || leaked.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
	b := newBackend()
	c, server := fixture(t, b, nil)
	if _, err := c.GetBusinessRecord(t.Context(), sdk.ConversationBusinessGet{RecordID: strings.Repeat("x", MaxRequestBytes)}, testAuthority); err == nil || b.count("get") != 0 {
		t.Fatal("oversize request dispatched")
	}
	r, _ := http.NewRequest("POST", server.URL+CallPath, strings.NewReader(strings.Repeat("x", MaxRequestBytes+1)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+testToken)
	res, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 {
		t.Fatal(res.StatusCode)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.Repeat("x", MaxResponseBytes+1))
	}))
	defer bad.Close()
	o.BaseURL = bad.URL
	if _, err := Open(t.Context(), o); err == nil {
		t.Fatal("oversize handshake accepted")
	}
	t.Logf("protocol %s contract %s", ProtocolVersion, ContractSHA256())
}

func TestContractAndConfigurationFailBeforeIO(t *testing.T) {
	// C05 adds explicit shared Report/Analysis provenance to optional read ports.
	// The actual reader remains the RPC authority; producer is proof context.
	// Business snapshots now also carry separate original producer provenance.
	// Existing DTOs and Go Backend remain unchanged, but strict descriptors require
	// coordinated client/server deployment pins. Only explicit unsupported
	// owners retain the old tool authorization path; denials never fall back.
	// E04 adds the trusted tool-owner parallelism declaration. Agent remains the
	// execution owner and treats empty/legacy declarations as serial.
	const expected = "cc2c8c87fdc30f8e9eecf8b087a28ad63a5f6d9f9227471e9fd5d3568d44d2d7"
	if ContractSHA256() != expected {
		t.Fatalf("public contract changed without compatibility review: %s", ContractSHA256())
	}
	var backend *backendFixture
	if _, err := NewHandler(ServerOptions{Scope: testScope, Token: testToken, Backend: backend}); err == nil {
		t.Fatal("typed nil backend accepted")
	}
	for _, base := range []string{"https://user:secret@example.com", "http://public.example.com", "https://example.com/path", "https://example.com?token=private", "https://example.com#private", "https://example.com:", "https://example.com:65536", "https://example.com:0"} {
		if _, err := Open(t.Context(), ClientOptions{BaseURL: base, Token: testToken, Scope: testScope, ExpectedSourceIdentity: testSource, ExpectedContractSHA256: expected}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe endpoint accepted/leaked %q: %v", base, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b := newBackend()
	c, _ := fixture(t, b, nil)
	if _, err := c.GetBusinessRecord(ctx, sdk.ConversationBusinessGet{}, testAuthority); err == nil || b.count("get") != 0 {
		t.Fatal("cancelled request dispatched")
	}
}
