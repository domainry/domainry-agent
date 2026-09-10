package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent/definition"
)

const maxResponseBytes = 2 << 20

type Config struct {
	BaseURL, APIKey string
	AgentID         int
	Timeout         time.Duration
	Client          *http.Client
}
type Runner struct{ config Config }

func New(config Config) *Runner {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.Timeout}
	}
	return &Runner{config: config}
}

func (r *Runner) validate() error {
	if r == nil || r.config.BaseURL == "" || r.config.APIKey == "" || r.config.AgentID <= 0 {
		return fmt.Errorf("Agent provider is not configured")
	}
	return nil
}

func (r *Runner) Validate() error { return r.validate() }

func (r *Runner) Start(ctx context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	if err := definition.ValidateTaskRequest(request); err != nil {
		return agentsdk.TaskResult{ErrorClass: "request_contract", ErrorCode: "agent.runner.request_invalid"}, err
	}
	input, err := json.Marshal(request.Input)
	if err != nil {
		return agentsdk.TaskResult{}, err
	}
	payload := map[string]any{"agent_id": r.config.AgentID, "message": strings.TrimSpace(request.Task.Instruction) + "\n\nReturn only the declared structured result.\nInput:\n" + string(input), "response_mode": "async", "external_session_id": "agent-task:" + request.WorkspaceID + ":" + request.TaskRunID, "metadata": map[string]any{"source": "domainry-agent-task-worker", "workspace_id": request.WorkspaceID, "process_id": request.ProcessID, "task_run_id": request.TaskRunID, "task_key": request.Task.Key, "task_version": request.Task.Version, "identity": request.Identity, "correlation_id": request.CorrelationID, "idempotency_key": request.IdempotencyKey, "execution_credential": request.ExecutionCredential, "tool_endpoint": "/agent/task-tools/invoke"}}
	return r.call(ctx, http.MethodPost, "/agent/v1/agent-runs", payload, request.IdempotencyKey)
}
func (r *Runner) Poll(ctx context.Context, id, key string) (agentsdk.TaskResult, error) {
	return r.call(ctx, http.MethodGet, "/agent/v1/agent-runs/"+url.PathEscape(strings.TrimSpace(id)), nil, key)
}
func (r *Runner) Cancel(ctx context.Context, id, key string) (agentsdk.TaskResult, error) {
	return r.call(ctx, http.MethodPost, "/agent/v1/agent-runs/"+url.PathEscape(strings.TrimSpace(id))+"/cancel", map[string]any{}, key)
}

func (r *Runner) Run(ctx context.Context, request agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	if err := r.validate(); err != nil {
		return agentsdk.InteractiveResult{}, err
	}
	if err := definition.ValidateInteractiveRequest(request); err != nil {
		return agentsdk.InteractiveResult{}, err
	}
	payload := map[string]any{"agent_id": r.config.AgentID, "message": request.Message, "response_mode": "blocking", "external_session_id": request.SessionID, "metadata": map[string]any{"source": "domainry-interactive-agent", "interactive_run_id": request.RunID, "idempotency_key": request.IdempotencyKey, "runtime_context": request.Context, "route_candidates": request.Candidates, "max_steps": request.MaxSteps, "max_tool_calls": request.MaxToolCalls, "execution_credential": request.ExecutionCredential}}
	raw, status, err := r.request(ctx, http.MethodPost, "/agent/v1/agent-runs", payload, request.IdempotencyKey)
	if err != nil {
		return agentsdk.InteractiveResult{}, err
	}
	if status/100 != 2 {
		return agentsdk.InteractiveResult{}, fmt.Errorf("Agent provider returned HTTP %d", status)
	}
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return agentsdk.InteractiveResult{}, fmt.Errorf("invalid interactive Agent response")
	}
	data := envelope
	if nested, ok := envelope["data"].(map[string]any); ok {
		data = nested
	}
	result := agentsdk.InteractiveResult{ExternalRunID: firstString(data, "external_run_id", "run_id", "id"), Status: firstString(data, "status", "state"), Message: firstString(data, "message", "content", "answer"), Model: firstString(data, "model", "model_name")}
	if result.Status == "" {
		result.Status = "completed"
	}
	result.Structured, _ = firstMap(data, "structured", "structured_output", "output", "result")
	result.Usage, _ = firstMap(data, "usage")
	return result, nil
}

func (r *Runner) call(ctx context.Context, method, path string, payload any, key string) (agentsdk.TaskResult, error) {
	if err := r.validate(); err != nil {
		return agentsdk.TaskResult{ErrorClass: "configuration", ErrorCode: "agent.runner.not_configured"}, err
	}
	raw, status, err := r.request(ctx, method, path, payload, key)
	if err != nil {
		return agentsdk.TaskResult{ErrorClass: "transport", ErrorCode: "agent.runner.transport_failed", Retryable: true}, err
	}
	result, decodeErr := decodeTask(raw)
	if status/100 != 2 {
		result.ErrorClass = "provider_http"
		result.ErrorCode = "agent.runner.provider_http_" + strconv.Itoa(status)
		result.Retryable = status == 429 || status >= 500
		return result, fmt.Errorf("Agent provider returned HTTP %d", status)
	}
	if decodeErr != nil {
		result.ErrorClass = "provider_contract"
		result.ErrorCode = "agent.runner.response_invalid"
	}
	return result, decodeErr
}
func (r *Runner) request(ctx context.Context, method, path string, payload any, key string) ([]byte, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.config.BaseURL+path, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(key) != "" {
		req.Header.Set("Idempotency-Key", strings.TrimSpace(key))
	}
	resp, err := r.config.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(raw) > maxResponseBytes {
		return nil, resp.StatusCode, fmt.Errorf("Agent response exceeds %d bytes", maxResponseBytes)
	}
	return raw, resp.StatusCode, nil
}
func decodeTask(raw []byte) (agentsdk.TaskResult, error) {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return agentsdk.TaskResult{}, fmt.Errorf("invalid Agent response")
	}
	if d, ok := p["data"].(map[string]any); ok {
		p = d
	}
	result := agentsdk.TaskResult{ExternalRunID: firstString(p, "external_run_id", "run_id", "id"), Status: agentsdk.ProviderRunStatus(firstString(p, "status", "state")), Outcome: firstString(p, "outcome"), Model: firstString(p, "model", "model_name")}
	result.Output, _ = firstMap(p, "output", "result", "structured_output")
	result.Usage, _ = firstMap(p, "usage")
	return result, nil
}
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func firstMap(m map[string]any, keys ...string) (map[string]any, bool) {
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			return v, true
		}
	}
	return nil, false
}

var _ agentsdk.TaskRunner = (*Runner)(nil)
var _ agentsdk.InteractiveRunner = (*Runner)(nil)
