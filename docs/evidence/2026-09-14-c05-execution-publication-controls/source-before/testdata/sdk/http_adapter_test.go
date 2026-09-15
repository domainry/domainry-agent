package agentsdk

import (
	"encoding/json"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestAgentHTTPAdapterContractOwnsCompleteRouteCatalog(t *testing.T) {
	actions, err := AgentAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 116+len(ConversationToolActions()) {
		t.Fatalf("Agent Action count=%d", len(actions))
	}
	roleActions, nonHTTPActions := 0, 0
	for _, action := range actions {
		if (action.Key == ConversationActionPrefix+"result_read" || action.Key == ConversationActionPrefix+"delegations_result" || action.Key == ConversationActionPrefix+"delegations_publication" || action.Key == ConversationActionPrefix+"delegations_publications" || action.Key == ConversationActionPrefix+"delegations_contract_publication" || action.Key == ConversationActionPrefix+"delegations_contract_candidates" || action.Key == ConversationActionPrefix+"delegations_contract_publications" || action.Key == ConversationActionPrefix+"delegations_artifact" || action.Key == ConversationActionPrefix+"delegations_export") && action.EffectClass != actioncontract.EffectRead {
			t.Fatal("stored-result read was classified as a mutation")
		}
		if action.Permission != nil {
			roleActions++
			if action.Permission == nil || action.Permission.Key != action.Key || action.Permission.Owner != action.Owner {
				t.Fatalf("Agent role Action %q permission=%+v", action.Key, action.Permission)
			}
		}
		if action.HTTP == nil {
			nonHTTPActions++
			strategy := actioncontract.AuthorizationSigned
			if strings.HasPrefix(action.Key, ConversationToolActionPrefix) || strings.HasPrefix(action.Key, ConversationCollaborationPermissionPrefix) {
				strategy = actioncontract.AuthorizationAuthenticated
			}
			if len(action.NonHTTP) != 1 || action.Authorization.Strategy != strategy {
				t.Fatalf("Agent non-HTTP Action %q binding=%v authorization=%+v", action.Key, action.NonHTTP, action.Authorization)
			}
		}
		separator := strings.LastIndex(action.Key, ".")
		if separator <= 0 || action.OperationKey != action.Key[separator+1:] {
			t.Fatalf("Agent Action %q operation=%q", action.Key, action.OperationKey)
		}
	}
	if roleActions != 41+len(ConversationToolActions()) || nonHTTPActions != 13+len(ConversationToolActions()) {
		t.Fatalf("Agent role Actions=%d non-HTTP Actions=%d", roleActions, nonHTTPActions)
	}

	contract, err := CompileAgentHTTPAdapterContract()
	if err != nil {
		t.Fatal(err)
	}
	if contract.ContractVersion != AgentHTTPAdapterContractVersion || contract.Owner != "agent" || len(contract.Routes) != 103 || len(contract.OpenAPI) != 103 {
		t.Fatalf("Agent HTTP contract=%s owner=%s routes=%d operations=%d", contract.ContractVersion, contract.Owner, len(contract.Routes), len(contract.OpenAPI))
	}
	seen := map[string]bool{}
	response := contract.OpenAPI["POST /agent/delegations/{delegationID}/delivery-export"]["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	if response["text/markdown"] == nil || response["text/csv"] == nil || response["application/json"] != nil {
		t.Fatal("released export OpenAPI disagrees with binary HTTP response")
	}
	for _, route := range contract.Routes {
		pattern := route.Pattern()
		if seen[pattern] || contract.OpenAPI[pattern]["operationId"] == nil || contract.OpenAPI[pattern]["responses"] == nil {
			t.Fatalf("incomplete or duplicate Agent route %q", pattern)
		}
		seen[pattern] = true
	}
	stream := contract.OpenAPI["POST /agent/runs/stream"]
	if stream["requestBody"] == nil || stream["parameters"] == nil || stream["x-domainry-runtime-client-method"] != "runAgentStream" {
		t.Fatalf("Agent stream operation=%#v", stream)
	}
	tool := contract.OpenAPI["POST /agent/task-tools/invoke"]
	security, ok := tool["security"].([]any)
	if !ok || len(security) != 0 {
		t.Fatalf("Agent tool callback security=%#v", tool["security"])
	}
	var toolAuthorization actioncontract.Authorization
	for _, route := range contract.Routes {
		if route.Pattern() == "POST /agent/task-tools/invoke" {
			toolAuthorization = route.Action.Authorization
			break
		}
	}
	if toolAuthorization.Strategy != actioncontract.AuthorizationSigned || toolAuthorization.PolicyKey != "agent.task_tool_credential" {
		t.Fatalf("Agent tool callback authorization=%+v", toolAuthorization)
	}
	components := HTTPAdapterReferencedComponents(map[string]map[string]any{"POST /agent/runs": contract.OpenAPI["POST /agent/runs"]})
	if components["securitySchemes"]["BearerAuth"] == nil || components["schemas"]["AgentInteractiveRunRequest"] == nil || components["schemas"]["AgentInteractiveExecutionResult"] == nil || components["schemas"]["AgentInteractiveRun"] == nil {
		t.Fatalf("Agent referenced component closure=%v", components)
	}
}

func TestResolvedAgentOpenAPIOperationsContainNoDanglingComponentReferences(t *testing.T) {
	operations := HTTPAdapterResolvedOpenAPIOperations()
	payload, err := json.Marshal(operations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "#/components/") {
		t.Fatalf("resolved Agent OpenAPI operations retain a component reference: %s", payload)
	}
	retry := operations["POST /agent/tasks/{taskRunID}/retry"]
	request := retry["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if request["type"] != "object" || request["properties"].(map[string]any)["reason"] == nil {
		t.Fatalf("resolved retry request schema=%#v", request)
	}
	response := retry["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if response["type"] != "object" || response["properties"].(map[string]any)["task"] == nil {
		t.Fatalf("resolved retry response schema=%#v", response)
	}
	diagnostics := operations["GET /agent/diagnostics"]
	diagnosticsResponse := diagnostics["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	diagnosticsProperties := diagnosticsResponse["properties"].(map[string]any)
	runner := diagnosticsProperties["agent_runner"].(map[string]any)
	if runner["additionalProperties"] != false || runner["properties"].(map[string]any)["transport"] == nil {
		t.Fatalf("resolved diagnostics runner schema=%#v", runner)
	}
	tools := diagnosticsProperties["effective_tools"].(map[string]any)["items"].(map[string]any)
	if tools["additionalProperties"] != false || tools["properties"].(map[string]any)["risk_level"] == nil {
		t.Fatalf("resolved diagnostics tool schema=%#v", tools)
	}
}
