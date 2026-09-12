package capability

import (
	"encoding/json"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
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
	declaredChains := map[string]bool{}
	for _, chain := range summary.Scenarios.AssemblyChains {
		declaredChains[chain] = true
	}
	for _, category := range summary.Categories {
		for _, chain := range category.AssemblyChains {
			if !declaredChains[chain] {
				t.Errorf("category %s references undeclared assembly chain %s", category.Key, chain)
			}
		}
	}
	operations, projections := 0, 0
	for _, category := range summary.Categories {
		operations += category.OperationCount
		projections += category.ProjectionCount
	}
	if operations != 74 || projections != 15 {
		t.Fatalf("Agent operations=%d projections=%d", operations, projections)
	}
	contract, err := agentsdk.CompileAgentHTTPAdapterContract()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range contract.Routes {
		category, err := binding.CapabilityCategory(t.Context(), route.Action.CapabilityKey)
		if err != nil {
			t.Fatalf("route %s has no published capability: %v", route.Pattern(), err)
		}
		parts := strings.SplitN(route.Pattern(), " ", 2)
		if len(category.OpenAPI.Paths[parts[1]][strings.ToLower(parts[0])]) == 0 {
			t.Fatalf("route %s missing from its capability", route.Pattern())
		}
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

func TestAgentAuthoringContractHidesAndDefaultsProtocolVersions(t *testing.T) {
	binding, err := NewBinding()
	if err != nil {
		t.Fatal(err)
	}
	category, err := binding.CapabilityCategory(t.Context(), AuthoringCategory)
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range category.Projections {
		if projection.Kind != "agent.validation_schema" {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(projection.Payload, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", projection.Key, err)
		}
		properties, _ := schema["properties"].(map[string]any)
		if projection.Key == "agent.task" || projection.Key == "agent.entrypoint" || projection.Key == "agent.service_principal" {
			if _, exposed := properties["contract_version"]; exposed {
				t.Fatalf("%s exposes compiler-owned contract_version", projection.Key)
			}
		}
		if projection.Key == "agent.skill" || projection.Key == "agent.agent" || projection.Key == "agent.task" {
			if _, exposed := properties["version"]; exposed {
				t.Fatalf("%s exposes backend-owned initial version", projection.Key)
			}
		}
		if projection.Key == "agent.service_principal" {
			if _, exposed := properties["rotation_version"]; exposed {
				t.Fatal("agent.service_principal exposes backend-owned initial rotation version")
			}
		}
		if projection.Key == "agent.entrypoint" {
			for _, nestedKey := range []string{"context_contract", "routing_contract"} {
				nested, _ := properties[nestedKey].(map[string]any)
				nestedProperties, _ := nested["properties"].(map[string]any)
				if _, exposed := nestedProperties["contract_version"]; exposed {
					t.Fatalf("agent.entrypoint.%s exposes compiler-owned contract_version", nestedKey)
				}
				if nestedKey == "routing_contract" {
					if _, exposed := nestedProperties["allow_recursive"]; exposed {
						t.Fatal("agent.entrypoint.routing_contract exposes backend-fixed allow_recursive")
					}
				}
				if nestedKey == "context_contract" {
					for _, backendOwned := range []string{"max_selected_records", "max_context_bytes"} {
						if _, exposed := nestedProperties[backendOwned]; exposed {
							t.Fatalf("agent.entrypoint.context_contract exposes backend-owned %s", backendOwned)
						}
					}
				}
			}
		}
	}

	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind, collection, key, value string
	}{
		{kind: "agent.task", collection: "agent_tasks", key: "review", value: `{"key":"review","agent_key":"reviewer","instruction":"review","input_schema":{"type":"object"},"output_schema":{"type":"object"},"allowed_outcomes":["success"],"side_effect_mode":"analysis_only","enabled":true}`},
		{kind: "agent.entrypoint", collection: "agent_entrypoints", key: "assistant", value: `{"key":"assistant","agent_key":"reviewer","required_permissions":["agent.task.operate"],"route_patterns":["workspace"],"context_contract":{},"routing_contract":{"allowed_route_types":["agent_task"]},"enabled":true}`},
		{kind: "agent.service_principal", collection: "agent_service_principals", key: "reviewer_service", value: `{"key":"reviewer_service","user_id":"reviewer_user","role_key":"reviewer","enabled":true}`},
	} {
		result, err := binding.ValidateCapabilityCandidate(t.Context(), modulecapability.ValidationRequest{
			ContractVersion: modulecapability.ValidationContractVersion,
			ModuleKey:       "agent",
			CategoryKey:     AuthoringCategory,
			ContractSHA256:  summary.Identity.ContractSHA256,
			Kind:            test.kind,
			Candidate:       modulecapability.AuthoringFragment{Collection: test.collection, Key: test.key, Value: json.RawMessage(test.value)},
		})
		if err != nil || len(result.Diagnostics) != 0 {
			t.Fatalf("%s diagnostics=%+v err=%v", test.kind, result.Diagnostics, err)
		}
	}
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
	if err := json.Unmarshal(category.OpenAPI.Paths["/agent/task-tools/invoke"]["post"], &operation); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(operation[modulecapability.OperationExtensionKey])
	var extension modulecapability.OperationExtension
	if err := json.Unmarshal(payload, &extension); err != nil {
		t.Fatal(err)
	}
	if extension.Authorization.Strategy != actioncontract.AuthorizationSigned || extension.Authorization.PolicyKey != "agent.task_tool_credential" || extension.Authorization.WorkspaceScope != "credential_workspace" {
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
	if err := json.Unmarshal(category.OpenAPI.Paths["/agent/runs/stream"]["post"], &operation); err != nil {
		t.Fatal(err)
	}
	raw := operation[modulecapability.OperationExtensionKey]
	payload, _ := json.Marshal(raw)
	var extension modulecapability.OperationExtension
	if err := json.Unmarshal(payload, &extension); err != nil {
		t.Fatal(err)
	}
	if extension.Transport == nil || extension.Transport.Mode != "sse" || extension.Idempotency.KeySource == "" || extension.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || extension.Authorization.PolicyKey != "" {
		t.Fatalf("Agent stream extension=%+v", extension)
	}
}
