package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func TestTaskRunnerMapsDomainryRequestAndIdempotency(t *testing.T) {
	var payload map[string]any
	var key string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"run_id":"provider-1","status":"accepted","model":"gpt"}}`))
	}))
	defer upstream.Close()
	runner := New(Config{BaseURL: upstream.URL, APIKey: "secret", AgentID: 7, Client: upstream.Client()})
	result, err := runner.Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task", ProcessID: "process", WorkspaceID: "workspace", Task: agentsdk.TaskDefinition{Key: "review", Version: "1", Instruction: "Review"}, Input: map[string]any{"id": 1}, ExecutionCredential: "credential", IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider-1" || key != "key" {
		t.Fatalf("result=%+v key=%q err=%v", result, key, err)
	}
	metadata := payload["metadata"].(map[string]any)
	if metadata["task_run_id"] != "task" || metadata["execution_credential"] != "credential" || payload["agent_id"] != float64(7) {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestTaskRunnerClassifiesProviderFailures(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":"failed"}`))
	}))
	defer upstream.Close()
	runner := New(Config{BaseURL: upstream.URL, APIKey: "secret", AgentID: 7, Client: upstream.Client()})
	result, err := runner.Poll(t.Context(), "run", "key")
	if err == nil || result.ErrorClass != "provider_http" || result.ErrorCode != "agent.runner.provider_http_429" || !result.Retryable {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
