package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

type runnerStub struct{}

func (runnerStub) Start(context.Context, agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunAccepted}, nil
}
func (runnerStub) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunRunning}, nil
}
func (runnerStub) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "run", Status: agentsdk.ProviderRunCancelled}, nil
}
func (runnerStub) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{Status: "completed"}, nil
}
func TestServerAuthenticationAndIdempotency(t *testing.T) {
	handler := New(Config{APIKey: "secret", Runner: runnerStub{}, Interactive: runnerStub{}}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/descriptor", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	body := `{"task_run_id":"task","workspace_id":"workspace","task":{"key":"task","version":"1","instruction":"run"},"identity":{},"idempotency_key":"key","deadline":"0001-01-01T00:00:00Z"}`
	request = httptest.NewRequest(http.MethodPost, "/api/v1/task-runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Idempotency-Key", "other")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/task-runs", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Idempotency-Key", "key")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"external_run_id":"run"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
