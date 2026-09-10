package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-foundation/modulehttp"
)

type callbackTaskHost struct {
	*taskHost
	revoked atomic.Bool
	invokes atomic.Int32
}

func (h *callbackTaskHost) AuthorizeTask(ctx context.Context, in modulehost.TaskAuthorizationRequest) (modulehost.TaskAuthorization, error) {
	out, err := h.taskHost.AuthorizeTask(ctx, in)
	if h.revoked.Load() {
		out.AllowedTools = nil
	}
	return out, err
}
func (h *callbackTaskHost) InvokeTaskTool(_ context.Context, in modulehost.TaskToolRequest) (modulehost.TaskToolResult, error) {
	if in.Credential != "scope-only-test" || in.WorkspaceID != "workspace-a" || in.Tool != agentsdk.AgentToolQueryRecords {
		return modulehost.TaskToolResult{}, &agentsdk.Error{Class: "forbidden", Code: "host.credential_scope_denied"}
	}
	if in.Identity.Execution.AuthorizationRevision != "host-auth-revision" || len(in.PreviousEvidence) == 0 || in.PreviousEvidence[len(in.PreviousEvidence)-1].AuthorizationRevision != "host-auth-revision" {
		return modulehost.TaskToolResult{}, &agentsdk.Error{Class: "forbidden", Code: "host.stale_authority"}
	}
	h.invokes.Add(1)
	return modulehost.TaskToolResult{Status: "executed", Tool: in.Tool, Output: map[string]any{"items": []any{map[string]any{"id": "customer-1"}}}, Authorization: in.PreviousEvidence[len(in.PreviousEvidence)-1]}, nil
}

type callbackApplicationHost struct {
	applicationHost
	callback *callbackTaskHost
}

func (h callbackApplicationHost) TaskAgent() modulehost.TaskHost { return h.callback }

// The real public module HTTP callback and SQLite ledger are exercised;
// business authorization and effects are explicit host fixtures.
func TestTaskCallbackHTTPReauthorizesBeforeAtomicBudgetReservation(t *testing.T) {
	for _, mode := range []agentsdk.DeploymentMode{agentsdk.DeploymentModeModule, agentsdk.DeploymentModeSaaS} {
		t.Run(string(mode), func(t *testing.T) { verifyTaskCallbackHTTP(t, mode) })
	}
}
func verifyTaskCallbackHTTP(t *testing.T, mode agentsdk.DeploymentMode) {
	harness := newBindingHarness(t, mode)
	binding := harness.open(t)
	defer binding.Close(context.Background())
	request := taskRequest("callback-run", "workspace-a", 1)
	if _, err := binding.TaskRunner().Start(serviceContext(t.Context(), agentsdk.ActionAgentTaskExecutionStart), request); err != nil {
		t.Fatal(err)
	}
	state := binding.(persistence.ExecutionStateBinding).AgentTaskState()
	claim, found, err := state.ClaimTask(t.Context(), "workspace-a", request.TaskRunID, "callback-worker", time.Minute)
	if err != nil || !found {
		t.Fatal("claim", err)
	}
	host := &callbackTaskHost{taskHost: newTaskHost(state)}
	if err = binding.(modulehost.ApplicationHostBinder).BindApplicationHost(callbackApplicationHost{applicationHost: applicationHost{ports: &unusedApplicationPorts{}}, callback: host}); err != nil {
		t.Fatal(err)
	}
	provider := binding.(interface{ HTTPAdapters() []modulehttp.Adapter })
	var handler http.Handler
	for _, adapter := range provider.HTTPAdapters() {
		for _, route := range adapter.Routes() {
			if route.Pattern() == "POST /agent/task-tools/invoke" {
				handler = adapter.Handler()
			}
		}
	}
	if handler == nil {
		t.Fatal("public task callback not mounted")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	invoke := func(key string, want int, wantCode string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"credential": "scope-only-test", "workspace_id": "workspace-a", "task_run_id": request.TaskRunID, "tool": "query_records", "input": map[string]any{"object_key": "customer"}, "idempotency_key": key})
		response, err := server.Client().Post(server.URL+"/agent/task-tools/invoke", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Code   string `json:"code"`
			Status string `json:"status"`
		}
		if json.Unmarshal(body, &result) != nil || response.StatusCode != want || wantCode != "" && result.Code != wantCode {
			t.Fatalf("HTTP %d, code=%s; want %d/%s", response.StatusCode, result.Code, want, wantCode)
		}
		if want == 200 && result.Status != "executed" {
			t.Fatal("host effect did not complete")
		}
	}
	invoke("first", 200, "")
	host.revoked.Store(true)
	invoke("revoked", 403, "agent.tool.tool_scope_denied")
	stored, found, err := state.Get(t.Context(), "workspace-a", request.TaskRunID)
	if err != nil || !found || stored.ToolCallCount != 1 || len(stored.Evidence.ToolInvocations) != 1 || host.invokes.Load() != 1 {
		t.Fatal("revoked callback charged or invoked the tool", err)
	}
	if stored.Evidence.ToolInvocations[0].Authorization.AuthorizationRevision != "host-auth-revision" {
		t.Fatal("ledger retained stale authorization")
	}
	host.revoked.Store(false)
	invoke("second", 200, "")
	invoke("over-budget", 429, "agent.task.tool_call_limit")
	ledger := binding.(interface {
		AgentTaskRunRepository() persistence.AgentTaskRunRepository
	}).AgentTaskRunRepository().(persistence.AgentToolCallLedger)
	_, _, budgetErr := ledger.BeginAgentToolCall(t.Context(), persistence.AgentToolCallStart{WorkspaceID: "workspace-a", TaskRunID: request.TaskRunID, Owner: claim.Lease.Owner, FencingToken: claim.Lease.FencingToken, Tool: "query_records", MaxToolCalls: 2, MaxCostUnits: 5, CostUnits: 2})
	var coded *agentsdk.Error
	if !errors.As(budgetErr, &coded) || coded.Class != "rate_limited" || coded.Code != "agent.task.tool_call_limit" || coded.Retryable {
		t.Fatalf("repository RPC lost budget semantics: %v", budgetErr)
	}
	if host.invokes.Load() != 2 {
		t.Fatal("host invoked after shared budget rejection")
	}
	if _, _, err = state.RequestCancel(t.Context(), "workspace-a", request.TaskRunID, "stop"); err != nil {
		t.Fatal(err)
	}
	invoke("after-cancel", 403, "agent.tool.task_scope_denied")
	stored, found, err = state.Get(t.Context(), "workspace-a", request.TaskRunID)
	if err != nil || !found || stored.ToolCallCount != 2 || stored.Lease.FencingToken != claim.Lease.FencingToken {
		t.Fatal("callback changed unrelated run state", err)
	}
}
