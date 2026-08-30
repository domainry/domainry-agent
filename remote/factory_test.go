package remote

import (
	"context"
	"net/http/httptest"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/contracttest"
	"github.com/domainry/domainry-agent/server"
)

type host string

func (h host) RuntimeID() string { return string(h) }

type runner struct{ started agentsdk.TaskRequest }

func (r *runner) Start(_ context.Context, request agentsdk.TaskRequest) (agentsdk.TaskResult, error) {
	r.started = request
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunAccepted}, nil
}
func (*runner) Poll(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunRunning}, nil
}
func (*runner) Cancel(context.Context, string, string) (agentsdk.TaskResult, error) {
	return agentsdk.TaskResult{ExternalRunID: "provider-1", Status: agentsdk.ProviderRunCancelled}, nil
}
func (*runner) Run(context.Context, agentsdk.InteractiveRequest) (agentsdk.InteractiveResult, error) {
	return agentsdk.InteractiveResult{Status: "completed", Message: "ok"}, nil
}

func TestSaaSFactoryValidatesDescriptorAndRunsProtocol(t *testing.T) {
	provider := &runner{}
	service := httptest.NewServer(server.New(server.Config{APIKey: "secret", Runner: provider, Interactive: provider}).Handler())
	defer service.Close()
	binding, err := NewFactory(Options{BaseURL: service.URL, APIKey: "secret", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	contracttest.VerifyBinding(t, binding, agentsdk.DeploymentModeSaaS)
	result, err := binding.TaskRunner().Start(t.Context(), agentsdk.TaskRequest{TaskRunID: "task-1", WorkspaceID: "workspace", Task: agentsdk.TaskDefinition{Key: "review", Version: "1", Instruction: "review"}, IdempotencyKey: "key"})
	if err != nil || result.ExternalRunID != "provider-1" || provider.started.TaskRunID != "task-1" {
		t.Fatalf("result=%+v request=%+v err=%v", result, provider.started, err)
	}
}

func TestSaaSFactoryFailsClosed(t *testing.T) {
	if _, err := NewFactory(Options{}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host("runtime")); err == nil {
		t.Fatal("missing endpoint accepted")
	}
	service := httptest.NewServer(server.New(server.Config{APIKey: "right"}).Handler())
	defer service.Close()
	if _, err := NewFactory(Options{BaseURL: service.URL, APIKey: "wrong", Client: service.Client()}).OpenSaaS(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host("runtime")); err == nil {
		t.Fatal("invalid credential accepted")
	}
}
