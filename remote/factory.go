package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/saashost"
)

const maxResponseBytes = 2 << 20

type Options struct {
	BaseURL, APIKey string
	Timeout         time.Duration
	Client          *http.Client
}

func OptionsFromEnvironment() Options {
	return Options{BaseURL: os.Getenv("AGENT_SAAS_BASE_URL"), APIKey: os.Getenv("AGENT_SAAS_API_KEY"), Timeout: 120 * time.Second}
}

type Factory struct{ options Options }

func NewFactory(options Options) *Factory { return &Factory{options: options} }
func (f *Factory) Open(context.Context, agentsdk.ApplicationRef) (agentsdk.Binding, error) {
	return nil, fmt.Errorf("Agent SaaS host is required")
}
func (f *Factory) OpenSaaS(ctx context.Context, app agentsdk.ApplicationRef, host saashost.Host) (agentsdk.Binding, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	if host == nil || host.RuntimeID() != app.RuntimeID {
		return nil, fmt.Errorf("Agent SaaS host identity mismatch")
	}
	client := newClient(f.options)
	descriptor, err := client.descriptor(ctx)
	if err != nil {
		return nil, err
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if descriptor.Mode != agentsdk.DeploymentModeSaaS {
		return nil, fmt.Errorf("Agent SaaS endpoint returned mode %q", descriptor.Mode)
	}
	return &binding{client: client, descriptor: descriptor}, nil
}

type binding struct {
	client     *client
	descriptor agentsdk.Descriptor
}

func (b *binding) Descriptor() agentsdk.Descriptor               { return b.descriptor }
func (b *binding) TaskRunner() agentsdk.TaskRunner               { return b.client }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner { return b.client }
func (*binding) Close(context.Context) error                     { return nil }

type client struct {
	baseURL, apiKey string
	http            *http.Client
}

func newClient(options Options) *client {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	httpClient := options.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &client{baseURL: strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"), apiKey: strings.TrimSpace(options.APIKey), http: httpClient}
}
func (c *client) descriptor(ctx context.Context) (agentsdk.Descriptor, error) {
	var value agentsdk.Descriptor
	err := c.call(ctx, http.MethodGet, "/api/v1/descriptor", nil, "", &value)
	return value, err
}
func (c *client) Start(ctx context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	var value agentsdk.TaskResult
	err := c.call(ctx, http.MethodPost, "/api/v1/task-runs", request, request.IdempotencyKey, &value)
	return value, err
}
func (c *client) Poll(ctx context.Context, id, key string) (agentsdk.TaskResult, error) {
	var value agentsdk.TaskResult
	err := c.call(ctx, http.MethodGet, "/api/v1/task-runs/"+url.PathEscape(strings.TrimSpace(id)), nil, key, &value)
	return value, err
}
func (c *client) Cancel(ctx context.Context, id, key string) (agentsdk.TaskResult, error) {
	var value agentsdk.TaskResult
	err := c.call(ctx, http.MethodPost, "/api/v1/task-runs/"+url.PathEscape(strings.TrimSpace(id))+"/cancel", map[string]any{}, key, &value)
	return value, err
}
func (c *client) Run(ctx context.Context, request agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	var value agentsdk.InteractiveResult
	err := c.call(ctx, http.MethodPost, "/api/v1/interactive-runs", request, request.IdempotencyKey, &value)
	return value, err
}
func (c *client) call(ctx context.Context, method, path string, payload any, key string, out any) error {
	if c == nil || c.baseURL == "" || c.apiKey == "" {
		return fmt.Errorf("Agent SaaS is not configured")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(key) != "" {
		request.Header.Set("Idempotency-Key", strings.TrimSpace(key))
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxResponseBytes {
		return fmt.Errorf("Agent SaaS response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode/100 != 2 {
		_ = json.Unmarshal(raw, out)
		var failure struct{ Code, Message string }
		_ = json.Unmarshal(raw, &failure)
		if failure.Code != "" {
			return &agentsdk.Error{Class: "saas_http", Code: failure.Code, Message: failure.Message, Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
		}
		return &agentsdk.Error{Class: "saas_http", Code: fmt.Sprintf("agent.saas.http_%d", response.StatusCode), Message: fmt.Sprintf("Agent SaaS returned HTTP %d", response.StatusCode), Retryable: response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode Agent SaaS response: %w", err)
	}
	return nil
}

var _ agentsdk.Factory = (*Factory)(nil)
var _ saashost.Factory = (*Factory)(nil)
var _ agentsdk.TaskRunner = (*client)(nil)
var _ agentsdk.InteractiveRunner = (*client)(nil)
