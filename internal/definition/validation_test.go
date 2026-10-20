package definition

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

func validTaskRequest() agentsdk.TaskRequest {
	return agentsdk.TaskRequest{TaskRunID: "run", WorkspaceID: "workspace", IdempotencyKey: "key", Task: agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: "review", Version: "1", AgentKey: "reviewer", Instruction: "review", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, AllowedOutcomes: []string{"success"}, SideEffectMode: agentsdk.AgentTaskSideEffectAnalysisOnly, Enabled: true}}
}

func TestValidateTaskRequest(t *testing.T) {
	if err := ValidateTaskRequest(validTaskRequest()); err != nil {
		t.Fatal(err)
	}
	invalid := validTaskRequest()
	invalid.Task.SideEffectMode = "direct_write"
	if err := ValidateTaskRequest(invalid); err == nil {
		t.Fatal("unsupported side effect accepted")
	}
}

func TestValidateInteractiveRequest(t *testing.T) {
	request := agentsdk.InteractiveRequest{RunID: "run", SessionID: "session", IdempotencyKey: "key", Message: "hello", Context: agentsdk.GlobalContext{ContractVersion: agentsdk.GlobalAgentContextContractVersion, ContextRevision: "revision", EntrypointKey: "assistant", AgentKey: "assistant", Principal: agentsdk.PrincipalReference{WorkspaceID: "workspace", UserID: "user"}}, Candidates: []agentsdk.RouteCandidate{{RouteType: agentsdk.AgentRouteTask, TargetKey: "review"}}}
	if err := ValidateInteractiveRequest(request); err != nil {
		t.Fatal(err)
	}
	request.Candidates[0].RouteType = "root"
	if err := ValidateInteractiveRequest(request); err == nil {
		t.Fatal("unsupported route accepted")
	}
}
