package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

type scheduleRouteHost struct {
	request   toolsdk.Request
	reconcile bool
}

func (*scheduleRouteHost) ConversationTools(context.Context, toolsdk.Authority) ([]toolsdk.Definition, error) {
	return nil, nil
}
func (*scheduleRouteHost) AuthorizeConversationTool(context.Context, toolsdk.Request) (toolsdk.Authorization, error) {
	return toolsdk.Authorization{Granted: true}, nil
}
func (h *scheduleRouteHost) InvokeConversationTool(_ context.Context, request toolsdk.Request) (toolsdk.Result, error) {
	h.request, h.reconcile = request, false
	return toolsdk.Result{Status: "completed", Content: json.RawMessage(`{"operation":"list","request_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","items":[]}`)}, nil
}
func (h *scheduleRouteHost) ReconcileConversationTool(_ context.Context, request toolsdk.Request) (toolsdk.Result, error) {
	h.request, h.reconcile = request, true
	return toolsdk.Result{Status: "completed", Content: json.RawMessage(`{"operation":"pause","request_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","plan":{"id":"plan-1"}}`)}, nil
}

func TestScheduleRoutesDeriveOwnerAndPathPlanID(t *testing.T) {
	toolHost := &scheduleRouteHost{}
	host := &Host{runtimeID: "runtime", scheduleToolHost: toolHost}
	mux := http.NewServeMux()
	for pattern, handler := range host.ScheduleRoutes() {
		mux.Handle(pattern, handler)
	}

	request := httptest.NewRequest(http.MethodPost, "/app/product/plans/plan-1/pause", strings.NewReader(`{"expected_revision":4}`))
	request.Header.Set("Idempotency-Key", "pause-1")
	request = request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user", RoleKey: "member"}}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !toolHost.reconcile {
		t.Fatalf("status=%d body=%s reconcile=%v", response.Code, response.Body.String(), toolHost.reconcile)
	}
	if toolHost.request.Authority != (toolsdk.Authority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user", RoleKey: "member"}) || toolHost.request.Definition.Key != toolsdk.SchedulePauseToolKey || toolHost.request.IdempotencyKey != "pause-1" {
		t.Fatalf("request=%+v", toolHost.request)
	}
	var arguments map[string]any
	if json.Unmarshal([]byte(toolHost.request.Call.Arguments), &arguments) != nil || arguments["plan_id"] != "plan-1" || arguments["expected_revision"] != float64(4) {
		t.Fatalf("arguments=%s", toolHost.request.Call.Arguments)
	}

	for _, test := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"identity required", http.MethodGet, "/app/product/plans", "", http.StatusUnauthorized},
		{"idempotency required", http.MethodPost, "/app/product/plans/plan-1/pause", `{"expected_revision":4}`, http.StatusBadRequest},
		{"path owns id", http.MethodPut, "/app/product/plans/plan-1", `{"plan_id":"other","expected_revision":4,"name":"x"}`, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.name != "identity required" {
				req = req.WithContext(identitysdk.WithRequestIdentity(req.Context(), identitysdk.RequestIdentity{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}}))
			}
			if test.name == "path owns id" {
				req.Header.Set("Idempotency-Key", "write")
			}
			out := httptest.NewRecorder()
			mux.ServeHTTP(out, req)
			if out.Code != test.want {
				t.Fatalf("status=%d body=%s", out.Code, out.Body.String())
			}
		})
	}
}

var _ toolsdk.Host = (*scheduleRouteHost)(nil)
