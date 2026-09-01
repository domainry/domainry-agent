package capability

import (
	"encoding/json"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulecapability/contracttest"
)

func TestAgentCapabilityOwnsAllProductRoutesAndAuthoringValidation(t *testing.T) {
	binding, err := NewBinding()
	if err != nil {
		t.Fatal(err)
	}
	contracttest.VerifyBinding(t, binding)
	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	operations, projections := 0, 0
	for _, category := range summary.Categories {
		operations += category.OperationCount
		projections += category.ProjectionCount
	}
	if operations != 22 || projections != 15 {
		t.Fatalf("Agent operations=%d projections=%d", operations, projections)
	}
	request := modulecapability.ValidationRequest{
		ContractVersion: modulecapability.ValidationContractVersion, ModuleKey: "agent", CategoryKey: AuthoringCategory,
		ContractSHA256: summary.Identity.ContractSHA256, Kind: "agent.skill",
		Candidate: modulecapability.AuthoringFragment{Collection: "skills", Key: "customer_reader", Value: json.RawMessage(`{"key":"customer_reader","name":"Customer reader","allowed_tools":["query_records"]}`)},
	}
	result, err := binding.ValidateCapabilityCandidate(t.Context(), request)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].RuleKey != "agent.skill.invalid" {
		t.Fatalf("Agent diagnostics=%+v err=%v", result.Diagnostics, err)
	}
	contracttest.VerifyModuleRemoteParity(t, binding, contracttest.ValidationCase{Name: "skill requires object scope", Request: request})
}

func TestAgentToolGatewayDisclosesDelegatedCredentialBoundary(t *testing.T) {
	binding, err := NewBinding()
	if err != nil {
		t.Fatal(err)
	}
	category, err := binding.CapabilityCategory(t.Context(), ToolGatewayCategory)
	if err != nil {
		t.Fatal(err)
	}
	var operation map[string]any
	if err := json.Unmarshal(category.OpenAPI.Paths["/agent-dialog/task-tools/invoke"]["post"], &operation); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(operation[modulecapability.OperationExtensionKey])
	var extension modulecapability.OperationExtension
	if err := json.Unmarshal(payload, &extension); err != nil {
		t.Fatal(err)
	}
	if extension.Authorization.Strategy != actioncontract.AuthorizationDelegatedCredential || extension.Authorization.PolicyKey != "agent.task_tool_credential" || extension.Authorization.WorkspaceScope != "credential_workspace" {
		t.Fatalf("Agent tool gateway extension=%+v", extension)
	}
}

func TestAgentStreamCapabilityDisclosesResumeTransport(t *testing.T) {
	binding, err := NewBinding()
	if err != nil {
		t.Fatal(err)
	}
	category, err := binding.CapabilityCategory(t.Context(), DialogCategory)
	if err != nil {
		t.Fatal(err)
	}
	var operation map[string]any
	if err := json.Unmarshal(category.OpenAPI.Paths["/agent-dialog/runs/stream"]["post"], &operation); err != nil {
		t.Fatal(err)
	}
	raw := operation[modulecapability.OperationExtensionKey]
	payload, _ := json.Marshal(raw)
	var extension modulecapability.OperationExtension
	if err := json.Unmarshal(payload, &extension); err != nil {
		t.Fatal(err)
	}
	if extension.Transport == nil || extension.Transport.Mode != "sse" || extension.Idempotency.KeySource == "" || extension.Authorization.Strategy != actioncontract.AuthorizationAuthenticatedPrincipal || extension.Authorization.PolicyKey != "" {
		t.Fatalf("Agent stream extension=%+v", extension)
	}
}
