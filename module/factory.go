package module

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-agent/internal/provider"
)

type Options struct {
	BaseURL, APIKey string
	AgentID         int
	Timeout         time.Duration
	Client          *http.Client
}

func OptionsFromEnvironment() Options {
	id, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("AGENT_HTTP_AGENT_ID")))
	return Options{BaseURL: os.Getenv("AGENT_HTTP_BASE_URL"), APIKey: os.Getenv("AGENT_HTTP_API_KEY"), AgentID: id, Timeout: 120 * time.Second}
}

type Factory struct{ options Options }

func NewFactory(options Options) *Factory { return &Factory{options: options} }
func (f *Factory) Open(context.Context, agentsdk.ApplicationRef) (agentsdk.Binding, error) {
	return nil, fmt.Errorf("Agent Module host is required")
}
func (f *Factory) OpenModule(_ context.Context, app agentsdk.ApplicationRef, host modulehost.Host) (agentsdk.Binding, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	if host == nil || host.RuntimeID() != app.RuntimeID {
		return nil, fmt.Errorf("Agent Module host identity mismatch")
	}
	runner := provider.New(provider.Config{BaseURL: f.options.BaseURL, APIKey: f.options.APIKey, AgentID: f.options.AgentID, Timeout: f.options.Timeout, Client: f.options.Client})
	if err := runner.Validate(); err != nil {
		return nil, err
	}
	return newBinding(runner, agentsdk.DeploymentModeModule), nil
}

type binding struct {
	runner *provider.Runner
	mode   agentsdk.DeploymentMode
}

func newBinding(r *provider.Runner, m agentsdk.DeploymentMode) *binding {
	return &binding{runner: r, mode: m}
}
func (b *binding) Descriptor() agentsdk.Descriptor {
	return agentsdk.Descriptor{ProtocolVersion: agentsdk.ProtocolVersionV1, Mode: b.mode, Capabilities: []string{"task.start", "task.poll", "task.cancel", "interactive.run", "structured_output", "usage", "tool_callback"}}
}
func (b *binding) TaskRunner() agentsdk.TaskRunner               { return b.runner }
func (b *binding) InteractiveRunner() agentsdk.InteractiveRunner { return b.runner }
func (*binding) Close(context.Context) error                     { return nil }

var _ agentsdk.Factory = (*Factory)(nil)
var _ modulehost.Factory = (*Factory)(nil)
