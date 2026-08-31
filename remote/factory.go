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
	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-agent-sdk/saashost"
	agentapplication "github.com/domainry/domainry-agent/internal/application"
	agentcomposition "github.com/domainry/domainry-agent/internal/composition"
	agenthttp "github.com/domainry/domainry-agent/internal/transport/http/module"
	"github.com/domainry/domainry-foundation/modulehttp"
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
	tasks := taskRepository{client: client}
	taskState := agentapplication.NewTaskStateService(tasks)
	taskExecution := agentapplication.NewTaskExecutionService(taskState, client, "")
	binding := &binding{client: client, descriptor: descriptor, tasks: tasks, taskExecution: taskExecution, taskState: taskState, interactive: agentapplication.NewInteractiveStateService(tasks)}
	surface, err := agenthttp.NewSurface(binding)
	if err != nil {
		return nil, err
	}
	binding.surfaces = []modulehttp.Surface{surface}
	binding.taskExecution.StartWorker(ctx)
	return binding, nil
}

type binding struct {
	client        *client
	descriptor    agentsdk.Descriptor
	tasks         taskRepository
	taskExecution *agentapplication.TaskExecutionService
	taskState     agentpersistence.AgentTaskStateService
	interactive   agentpersistence.AgentInteractiveStateService
	surfaces      []modulehttp.Surface
}

func (b *binding) Descriptor() agentsdk.Descriptor                        { return b.descriptor }
func (b *binding) TaskRunner() agentsdk.TaskRunner                        { return b.taskExecution }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner          { return b.client }
func (b *binding) DialogState() agentsdk.AgentDialogStateService          { return b.client }
func (b *binding) AgentTaskState() agentpersistence.AgentTaskStateService { return b.taskState }
func (b *binding) AgentInteractiveState() agentpersistence.AgentInteractiveStateService {
	return b.interactive
}
func (b *binding) HTTPSurfaces() []modulehttp.Surface {
	return append([]modulehttp.Surface(nil), b.surfaces...)
}
func (b *binding) BindApplicationHost(host modulehost.ApplicationHost) error {
	surface, err := agentcomposition.BindApplicationSurface(agentcomposition.ApplicationSurfaceDependencies{
		Binding: b, DialogState: b.client, TaskState: b.taskState, InteractiveState: b.interactive,
		ToolLedger: b.tasks, TaskExecution: b.taskExecution, InteractiveRunner: b.client, Host: host,
	})
	if err != nil {
		return err
	}
	b.surfaces = []modulehttp.Surface{surface}
	return nil
}
func (b *binding) DefinitionRepository() agentpersistence.DefinitionRepository { return b.client }
func (b *binding) AgentStateRepository() agentpersistence.AgentStateRepository { return b.client }
func (b *binding) AgentTaskRunRepository() agentpersistence.AgentTaskRunRepository {
	return b.tasks
}
func (b *binding) AgentLifecycleRepository() agentpersistence.AgentLifecycleRepository {
	return b.client
}
func (b *binding) Close(ctx context.Context) error {
	_ = ctx
	if b != nil && b.taskExecution != nil {
		b.taskExecution.Close()
	}
	return nil
}

var _ modulehost.ApplicationHostBinder = (*binding)(nil)

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

func (c *client) ListSessions(ctx context.Context, query agentsdk.AgentSessionQuery, authority agentsdk.AgentAuthority) ([]agentsdk.AgentSession, error) {
	var value []agentsdk.AgentSession
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/sessions/query", struct {
		Query     agentsdk.AgentSessionQuery `json:"query"`
		Authority agentsdk.AgentAuthority    `json:"authority"`
	}{Query: query, Authority: authority}, "", &value)
	return value, err
}

func (c *client) UpsertSession(ctx context.Context, input agentsdk.AgentSessionUpsertRequest, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	var value agentsdk.AgentSession
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/sessions/upsert", struct {
		Input     agentsdk.AgentSessionUpsertRequest `json:"input"`
		Authority agentsdk.AgentAuthority            `json:"authority"`
	}{Input: input, Authority: authority}, "", &value)
	return value, err
}

func (c *client) SetSessionArchived(ctx context.Context, externalID string, archived bool, authority agentsdk.AgentAuthority) (agentsdk.AgentSession, error) {
	var value agentsdk.AgentSession
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/sessions/"+url.PathEscape(strings.TrimSpace(externalID))+"/archive", struct {
		Archived  bool                    `json:"archived"`
		Authority agentsdk.AgentAuthority `json:"authority"`
	}{Archived: archived, Authority: authority}, "", &value)
	return value, err
}

func (c *client) ListProposals(ctx context.Context, status string, authority agentsdk.AgentAuthority) ([]agentsdk.AgentProposal, error) {
	var value []agentsdk.AgentProposal
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/proposals/query", struct {
		Status    string                  `json:"status,omitempty"`
		Authority agentsdk.AgentAuthority `json:"authority"`
	}{Status: status, Authority: authority}, "", &value)
	return value, err
}

func (c *client) GetProposal(ctx context.Context, proposalID string, authority agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	var value agentsdk.AgentProposal
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/proposals/"+url.PathEscape(strings.TrimSpace(proposalID))+"/get", struct {
		Authority agentsdk.AgentAuthority `json:"authority"`
	}{Authority: authority}, "", &value)
	return value, err
}

func (c *client) StoreProposal(ctx context.Context, proposal agentsdk.AgentProposal) (agentsdk.AgentProposal, error) {
	var value agentsdk.AgentProposal
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/proposals/store", proposal, "", &value)
	return value, err
}

func (c *client) DecideProposal(ctx context.Context, decision agentsdk.AgentProposalDecision, authority agentsdk.AgentAuthority) (agentsdk.AgentProposal, error) {
	var value agentsdk.AgentProposal
	err := c.call(ctx, http.MethodPost, "/api/v1/dialog-state/proposals/decide", struct {
		Decision  agentsdk.AgentProposalDecision `json:"decision"`
		Authority agentsdk.AgentAuthority        `json:"authority"`
	}{Decision: decision, Authority: authority}, "", &value)
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
var _ agentsdk.AgentDialogStateService = (*client)(nil)
var _ agentsdk.AgentDialogStateBinding = (*binding)(nil)
var _ agentpersistence.ExecutionStateBinding = (*binding)(nil)
var _ modulehttp.Provider = (*binding)(nil)
