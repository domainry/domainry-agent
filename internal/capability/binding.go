package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentdefinition "github.com/domainry/domainry-agent/definition"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
)

const (
	AuthoringCategory   = "agent.authoring"
	DialogCategory      = "agent.dialog"
	OperationsCategory  = "agent.operations"
	ProposalsCategory   = "agent.proposals"
	ToolGatewayCategory = "agent.tool_gateway"
)

var categoryPatterns = map[string][]string{
	DialogCategory: {
		"POST /agent-dialog/runs", "POST /agent-dialog/runs/stream",
		"GET /agent-dialog/sessions", "POST /agent-dialog/sessions", "POST /agent-dialog/sessions/{externalSessionID}/archive", "POST /agent-dialog/sessions/{externalSessionID}/restore",
		"GET /agent-dialog/runs/{runID}", "GET /agent-dialog/task-runs/{taskRunID}", "POST /agent-dialog/analysis/query", "GET /agent-dialog/diagnostics",
	},
	ProposalsCategory: {
		"GET /agent-dialog/proposals", "GET /agent-dialog/proposals/{proposalID}", "POST /agent-dialog/proposals", "POST /agent-dialog/proposals/{proposalID}/approve", "POST /agent-dialog/proposals/{proposalID}/reject",
	},
	OperationsCategory: {
		"GET /operations/agent/tasks", "GET /operations/agent/tasks/{taskRunID}", "POST /operations/agent/tasks/{taskRunID}/retry", "POST /operations/agent/tasks/{taskRunID}/cancel", "POST /operations/agent/tasks/{taskRunID}/resolve", "POST /operations/agent/tasks/{taskRunID}/reconcile",
	},
	ToolGatewayCategory: {"POST /agent-dialog/task-tools/invoke"},
}

func NewBinding() (*modulecapability.StaticBinding, error) {
	contract := agentsdk.AgentHTTPSurfaceContract()
	categories := make([]modulecapability.CategoryDocument, 0, 5)
	for _, specification := range []struct {
		key, name, description string
		chains                 []string
	}{
		{DialogCategory, "Agent dialog and analysis", "Run principal-scoped conversations, retain sessions and execution state, query permitted business data, and inspect Agent diagnostics.", []string{"identity_principal_to_agent_context", "agent_route_to_task_or_workflow", "agent_analysis_to_report_or_proposal"}},
		{OperationsCategory, "Agent task operations", "Inspect and recover durable Agent task runs with operator evidence and idempotent commands.", []string{"agent_task_to_operator_recovery", "agent_task_to_workflow_reconciliation"}},
		{ProposalsCategory, "Agent proposals", "Create, inspect, approve, and reject suggested business changes without granting the model direct write authority.", []string{"agent_suggestion_to_approval_to_business_action"}},
		{ToolGatewayCategory, "Agent task tool gateway", "Invoke Runtime-authorized tools through a short-lived task credential, fencing evidence, budgets, and an idempotency key.", []string{"agent_task_credential_to_runtime_guarded_tool"}},
	} {
		category, err := httpCategory(contract, specification.key, specification.name, specification.description, specification.chains)
		if err != nil {
			return nil, err
		}
		categories = append(categories, category)
	}
	authoring, err := authoringCategory()
	if err != nil {
		return nil, err
	}
	categories = append(categories, authoring)

	summary := modulecapability.ModuleSummary{
		Identity: modulecapability.ModuleIdentity{
			Key: "agent", SourceOwner: "agent", ModuleVersion: agentsdk.ProtocolVersionV1, ValidationRevision: "agent-definition-validation-v1",
			SupportedDeploymentModes: []modulecapability.DeploymentMode{modulecapability.DeploymentModeModule, modulecapability.DeploymentModeSaaS},
		},
		Name: "Agent", Description: "Owns conversational and asynchronous model execution, sessions, task state, tool-call evidence, proposal policy, guarded routing, and Agent-specific authoring contracts; Runtime remains the authority for identity, visible business schema, permissions, workflows, and concrete business effects.",
		Scenarios: modulecapability.AdaptationScenarios{
			UseWhen:              []string{"A PRD needs natural-language interaction, model-assisted reasoning, semi-autonomous multi-step work, structured Agent tasks, governed tool use, or proposals that require human approval"},
			DoNotUseWhen:         []string{"The requirement is deterministic CRUD, a fixed workflow, a scheduled trigger, a static report, or a notification delivery with no model reasoning or conversational interaction"},
			RequirementSignals:   []string{"AI assistant", "copilot", "natural language", "agent task", "tool calling", "reasoning", "human approval", "proposal", "conversation", "autonomous analysis"},
			ProvidedCapabilities: []string{"agent.interactive_dialog", "agent.asynchronous_task", "agent.guarded_tool_call", "agent.proposal_approval", "agent.principal_scoped_analysis", "agent.execution_evidence", "agent.operator_recovery"},
			RequiredModules:      []string{"audit", "identity"}, OptionalModules: []string{"integration", "notification", "report", "scheduler"}, ConflictingModules: []string{},
			AssemblyChains: []string{
				"identity_principal_to_agent_context", "agent_route_to_task_or_workflow", "agent_task_credential_to_runtime_guarded_tool",
				"agent_suggestion_to_approval_to_business_action", "agent_analysis_to_report_or_proposal",
				"agent_task_to_operator_recovery", "agent_task_to_workflow_reconciliation",
				"agent_skill_to_agent_to_task", "agent_entrypoint_to_identity_permission", "agent_task_to_object_action_or_workflow",
			},
			ValidationScopes:  []string{"agent.agent", "agent.entrypoint", "agent.service_principal", "agent.skill", "agent.task"},
			SelectionExamples: []modulecapability.ScenarioExample{{Requirement: "Let account managers ask questions about visible customer data and propose a follow-up action for approval", Reason: "Agent owns the conversation, scoped analysis, proposal state, and guarded tool orchestration while Identity and the business owner enforce access and effects"}},
			RejectionExamples: []modulecapability.ScenarioExample{{Requirement: "Send an email when an order is approved", Reason: "A deterministic Workflow plus Notification or Connector integration is sufficient; no model reasoning or Agent state is required"}},
		},
	}
	return modulecapability.NewStaticBinding(summary, categories, validator())
}

func httpCategory(contract agentsdk.HTTPSurfaceContract, key, name, description string, chains []string) (modulecapability.CategoryDocument, error) {
	patterns := categoryPatterns[key]
	routesByPattern := map[string]agentsdk.HTTPRouteContract{}
	for _, route := range contract.Routes {
		routesByPattern[route.Pattern()] = route
	}
	routes := make([]modulehttp.Route, 0, len(patterns))
	operations := make(map[string]map[string]any, len(patterns))
	overrides := make(map[string]modulecapability.OperationExtension, len(patterns))
	for _, pattern := range patterns {
		source, found := routesByPattern[pattern]
		if !found {
			return modulecapability.CategoryDocument{}, fmt.Errorf("Agent capability route %q is absent from the SDK contract", pattern)
		}
		route := projectHTTPRoute(source)
		routes = append(routes, route)
		operations[pattern] = contract.OpenAPI[pattern]
		if extension, override := agentOperationOverride(route); override {
			overrides[pattern] = extension
		}
	}
	category, err := modulecapability.CategoryFromHTTPRoutes(modulecapability.HTTPRouteCategory{
		Owner: "agent", Category: modulecapability.CategorySummary{Key: key, Name: name, Description: description, AssemblyChains: chains}, Routes: routes, Operations: operations,
		Components: agentsdk.HTTPSurfaceReferencedComponents(operations), WorkspaceScope: "authenticated_workspace", ExtensionOverrides: overrides,
	})
	if err != nil {
		return modulecapability.CategoryDocument{}, fmt.Errorf("project Agent category %q: %w", key, err)
	}
	return category, nil
}

func projectHTTPRoute(source agentsdk.HTTPRouteContract) modulehttp.Route {
	return modulehttp.Route{Action: source.Action}
}

func agentOperationOverride(route modulehttp.Route) (modulecapability.OperationExtension, bool) {
	pattern := route.Pattern()
	override, scope := false, "authenticated_workspace"
	switch {
	case pattern == "POST /agent-dialog/runs" || pattern == "POST /agent-dialog/runs/stream":
		override = true
	case strings.Contains(pattern, "/agent-dialog/sessions"):
		override = true
	case strings.Contains(pattern, "/agent-dialog/proposals"):
		override = true
	case pattern == "GET /agent-dialog/runs/{runID}":
		override = true
	case pattern == "GET /agent-dialog/task-runs/{taskRunID}":
		override = true
	case pattern == "POST /agent-dialog/analysis/query":
		override = true
	case pattern == "POST /agent-dialog/task-tools/invoke":
		override, scope = true, "credential_workspace"
	}
	if !override {
		return modulecapability.OperationExtension{}, false
	}
	idempotency := modulecapability.Idempotency{Mode: route.Action.IdempotencyDecision}
	switch pattern {
	case "POST /agent-dialog/runs", "POST /agent-dialog/runs/stream":
		idempotency.KeySource = "header.Idempotency-Key_or_body.idempotency_key"
	case "POST /agent-dialog/sessions/{externalSessionID}/archive":
		idempotency.KeySource = "path.externalSessionID+archive"
	case "POST /agent-dialog/sessions/{externalSessionID}/restore":
		idempotency.KeySource = "path.externalSessionID+restore"
	case "POST /agent-dialog/proposals/{proposalID}/approve":
		idempotency.KeySource = "path.proposalID+approve"
	case "POST /agent-dialog/proposals/{proposalID}/reject":
		idempotency.KeySource = "path.proposalID+reject"
	case "POST /agent-dialog/task-tools/invoke":
		idempotency.KeySource = "body.idempotency_key"
	}
	authorization := modulecapability.Authorization{
		Strategy: route.Action.Authorization.Strategy, PolicyKey: route.Action.Authorization.PolicyKey,
		Audiences: append([]string(nil), route.Action.Authorization.Audiences...), WorkspaceScope: scope,
	}
	if route.Action.Permission != nil {
		authorization.Permission = route.Action.Permission.Key
	}
	extension := modulecapability.OperationExtension{
		Owner: "agent", Authorization: authorization,
		Effect: modulecapability.EffectClass(route.Action.EffectClass), Idempotency: idempotency,
	}
	if pattern == "POST /agent-dialog/runs/stream" {
		extension.Transport = &modulecapability.Transport{Mode: "sse", ResumeSemantics: "Last-Event-ID must match the deterministic accepted event cursor", DeliveryOrdering: "accepted_then_terminal"}
	}
	return extension, true
}

func authoringCategory() (modulecapability.CategoryDocument, error) {
	types := []struct {
		kind  string
		value any
	}{
		{"agent.agent", agentsdk.AgentSchema{}},
		{"agent.entrypoint", agentsdk.AgentEntrypointAssignment{}},
		{"agent.service_principal", agentsdk.AgentServicePrincipalBinding{}},
		{"agent.skill", agentsdk.SkillSchema{}},
		{"agent.task", agentsdk.AgentTaskDefinition{}},
	}
	projections := make([]modulecapability.SourceProjection, 0, len(types)+len(agentsdk.AgentToolKeys()))
	for _, source := range types {
		schema := modulecapability.JSONSchemaForGoValue(source.value)
		applyAgentSchemaConstraints(source.kind, schema)
		payload, err := json.Marshal(schema)
		if err != nil {
			return modulecapability.CategoryDocument{}, err
		}
		projections = append(projections, modulecapability.SourceProjection{Kind: "agent.validation_schema", Key: source.kind, Payload: payload})
	}
	for _, key := range agentsdk.AgentToolKeys() {
		tool, _ := agentsdk.LookupAgentTool(key)
		payload, err := json.Marshal(map[string]any{"key": tool.Key, "requires_allowed_objects": tool.RequiresAllowedObjects, "requires_allowed_actions": tool.RequiresAllowedActions, "writes": tool.Writes})
		if err != nil {
			return modulecapability.CategoryDocument{}, err
		}
		projections = append(projections, modulecapability.SourceProjection{Kind: "agent.tool", Key: key, Payload: payload})
	}
	return modulecapability.CategoryDocument{
		Category: modulecapability.CategorySummary{
			Key: AuthoringCategory, Name: "Agent authoring", Description: "Author Agent skills, agents, tasks, entrypoints, service identities, and guarded tool allowlists before cross-module references are assembled.",
			AssemblyChains:   []string{"agent_skill_to_agent_to_task", "agent_entrypoint_to_identity_permission", "agent_task_to_object_action_or_workflow"},
			ValidationScopes: []string{"agent.agent", "agent.entrypoint", "agent.service_principal", "agent.skill", "agent.task"},
		},
		OpenAPI:     modulecapability.OpenAPIFragment{OpenAPI: "3.1.0", Paths: map[string]map[string]json.RawMessage{}},
		Projections: projections,
		ValidationContracts: []modulecapability.ValidationScopeContract{
			{Kind: "agent.agent", Description: "Validate one Agent definition.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"agents"}},
			{Kind: "agent.entrypoint", Description: "Validate one Agent entrypoint assignment.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"agent_entrypoints"}, ReferencedCollections: []string{"agent_tasks", "agents", "permissions", "workflows"}},
			{Kind: "agent.service_principal", Description: "Validate one Agent service-principal binding.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"agent_service_principals"}, ReferencedCollections: []string{"roles"}},
			{Kind: "agent.skill", Description: "Validate one Agent skill definition.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"skills"}},
			{Kind: "agent.task", Description: "Validate one Agent task definition.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"agent_tasks"}, ReferencedCollections: []string{"actions", "agents", "objects"}},
		},
	}, nil
}

func applyAgentSchemaConstraints(kind string, schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	toolEnum := append([]string(nil), agentsdk.AgentToolKeys()...)
	stringArrayWithEnum := func(values []string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": values}}
	}
	switch kind {
	case "agent.skill":
		properties["allowed_tools"] = stringArrayWithEnum(toolEnum)
	case "agent.agent":
		properties["tools"] = stringArrayWithEnum(toolEnum)
	case "agent.task":
		properties["contract_version"] = map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskContractVersion}}
		properties["allowed_outcomes"] = stringArrayWithEnum(append([]string(nil), agentsdk.AgentTaskOutcomes...))
		properties["side_effect_mode"] = map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskSideEffectAnalysisOnly, agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed}}
	case "agent.entrypoint":
		properties["contract_version"] = map[string]any{"type": "string", "enum": []string{agentsdk.AgentEntrypointContractVersion}}
	case "agent.service_principal":
		properties["contract_version"] = map[string]any{"type": "string", "enum": []string{agentsdk.AgentServicePrincipalContractVersion}}
	}
}

func validator() modulecapability.Validator {
	return func(_ context.Context, request modulecapability.ValidationRequest) (modulecapability.ValidationResult, error) {
		invalid := func(rule string, err error) (modulecapability.ValidationResult, error) {
			return modulecapability.ValidationResult{Diagnostics: []modulecapability.Diagnostic{{Owner: "agent", RuleKey: rule, Severity: modulecapability.SeverityError, FieldPath: "$.candidate.value", Message: err.Error()}}}, nil
		}
		var err error
		switch request.Kind {
		case "agent.skill":
			var candidate agentsdk.SkillSchema
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				err = agentdefinition.ValidateSkillDefinition(candidate)
			}
		case "agent.agent":
			var candidate agentsdk.AgentSchema
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				err = agentdefinition.ValidateAgentDefinition(candidate)
			}
		case "agent.task":
			var candidate agentsdk.AgentTaskDefinition
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				err = agentdefinition.ValidateTaskDefinition(candidate)
			}
		case "agent.entrypoint":
			var candidate agentsdk.AgentEntrypointAssignment
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				err = agentdefinition.ValidateEntrypointDefinition(candidate)
			}
		case "agent.service_principal":
			var candidate agentsdk.AgentServicePrincipalBinding
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				err = agentdefinition.ValidateServicePrincipalDefinition(candidate)
			}
		default:
			return modulecapability.ValidationResult{}, &modulecapability.Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid"}
		}
		if err != nil {
			return invalid(request.Kind+".invalid", err)
		}
		return modulecapability.ValidationResult{Diagnostics: []modulecapability.Diagnostic{}}, nil
	}
}
