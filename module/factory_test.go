package module

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/contracttest"
	"testing"
)

type host string

func (h host) RuntimeID() string { return string(h) }
func TestModuleDescriptor(t *testing.T) {
	b, err := NewFactory(Options{BaseURL: "https://agent.test", APIKey: "key", AgentID: 1}).OpenModule(t.Context(), agentsdk.ApplicationRef{RuntimeID: "runtime"}, host("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Descriptor().Mode != agentsdk.DeploymentModeModule {
		t.Fatalf("descriptor=%+v", b.Descriptor())
	}
	contracttest.VerifyBinding(t, b, agentsdk.DeploymentModeModule)
}
