package capability

import (
	"context"
	"encoding/json"
	"fmt"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentdefinition "github.com/domainry/domainry-agent/definition"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
	"github.com/domainry/domainry-foundation/modulehttp"
)

const (
	AuthoringCategory             = "agent.authoring"
	DialogCategory                = agentsdk.AgentCapabilityDialog
	OperationsCategory            = agentsdk.AgentCapabilityOperations
	ProposalsCategory             = agentsdk.AgentCapabilityProposals
	ToolGatewayCategory           = agentsdk.AgentCapabilityToolGateway
	agentInitialDefinitionVersion = "1.0.0"
	agentDefaultSelectedRecords   = 20
	agentDefaultContextBytes      = 65536
)

func NewBinding() (*modulecapability.StaticBinding, error) {
	contract, err := agentsdk.CompileAgentHTTPAdapterContract()
	if err != nil {
		return nil, err
	}
	categories := make([]modulecapability.CategoryDocument, 0, 5)
	for _, specification := range []struct {
		key, name, description string
		chains                 []string
	}{
		{agentsdk.AgentCapabilityConversation, "Persistent personal conversations", "Own durable messages, explicit personal memory, context compaction and resumable tool execution, independently of business routing.", []string{"identity_principal_to_persistent_conversation"}},
		{agentsdk.AgentCapabilityBackgroundTasks, "Durable background tasks", "Inspect, cancel and resume owner-scoped background executions while preserving their source conversation and run boundaries.", []string{"identity_principal_to_persistent_conversation"}},
		{agentsdk.AgentCapabilityPersonalTodos, "Personal todos", "Manage owner-scoped work items, ordered batches, deadlines and completion independently of Agent execution and scheduled jobs.", []string{"identity_principal_to_persistent_conversation"}},
		{agentsdk.AgentCapabilityKnowledgeLibraries, "Knowledge libraries", "Personal and shared libraries with live Identity authorization and library membership roles.", []string{"identity_principal_to_knowledge_library_membership"}},
		{agentsdk.AgentCapabilityAttachments, "Private conversation attachments", "Upload, inspect, download and delete owner-scoped original files without exposing host paths or treating stored files as indexed knowledge.", []string{"identity_principal_to_private_conversation_attachment"}},
		{agentsdk.AgentCapabilityArtifacts, "Saved artifacts", "Create, read, edit and export owner-scoped artifacts with immutable versions and current source authorization.", []string{"conversation_to_versioned_artifact_and_download"}},
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
			ProvidedCapabilities: []string{"agent.persistent_conversation", "agent.interactive_dialog", "agent.asynchronous_task", "agent.guarded_tool_call", "agent.proposal_approval", "agent.principal_scoped_analysis", "agent.execution_evidence", "agent.operator_recovery"},
			RequiredModules:      []string{"audit", "identity"}, OptionalModules: []string{"integration", "notification", "report", "scheduler"}, ConflictingModules: []string{},
			AssemblyChains: []string{
				"identity_principal_to_knowledge_library_membership", "identity_principal_to_private_conversation_attachment", "conversation_to_versioned_artifact_and_download",
				"identity_principal_to_persistent_conversation", "identity_principal_to_agent_context", "agent_route_to_task_or_workflow", "agent_task_credential_to_runtime_guarded_tool",
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

func httpCategory(contract agentsdk.HTTPAdapterContract, key, name, description string, chains []string) (modulecapability.CategoryDocument, error) {
	routes := []modulehttp.Route{}
	operations := map[string]map[string]any{}
	overrides := map[string]modulecapability.OperationExtension{}
	for _, source := range contract.Routes {
		if source.Action.CapabilityKey != key {
			continue
		}
		route, err := projectHTTPRoute(source)
		if err != nil {
			return modulecapability.CategoryDocument{}, fmt.Errorf("project Agent capability Action %q: %w", source.Action.Key, err)
		}
		pattern := route.Pattern()
		routes = append(routes, route)
		operations[pattern] = contract.OpenAPI[pattern]
		if extension, override := agentOperationOverride(route); override {
			overrides[pattern] = extension
		}
	}
	if len(routes) == 0 {
		return modulecapability.CategoryDocument{}, fmt.Errorf("Agent capability %q has no HTTP Actions", key)
	}
	category, err := modulecapability.CategoryFromHTTPRoutes(modulecapability.HTTPRouteCategory{
		Owner: "agent", Category: modulecapability.CategorySummary{Key: key, Name: name, Description: description, AssemblyChains: chains}, Routes: routes, Operations: operations,
		Components: agentsdk.HTTPAdapterReferencedComponents(operations), WorkspaceScope: "authenticated_workspace", ExtensionOverrides: overrides,
	})
	if err != nil {
		return modulecapability.CategoryDocument{}, fmt.Errorf("project Agent category %q: %w", key, err)
	}
	return category, nil
}

func projectHTTPRoute(source agentsdk.HTTPRouteContract) (modulehttp.Route, error) {
	return modulehttp.RouteFromAction(source.Action)
}

func agentOperationOverride(route modulehttp.Route) (modulecapability.OperationExtension, bool) {
	override, scope := false, "authenticated_workspace"
	switch route.Action.Authorization.Strategy {
	case actioncontract.AuthorizationAuthenticated:
		override = route.Action.Permission == nil
	case actioncontract.AuthorizationSigned:
		override, scope = true, "credential_workspace"
	}
	if !override {
		return modulecapability.OperationExtension{}, false
	}
	idempotency := modulecapability.Idempotency{Mode: route.Action.IdempotencyDecision}
	switch route.Action.Key {
	case agentsdk.ActionAgentRunsExecute, agentsdk.ActionAgentRunsStream:
		idempotency.KeySource = "header.Idempotency-Key_or_body.idempotency_key"
	case agentsdk.ActionAgentSessionsArchive:
		idempotency.KeySource = "path.externalSessionID+archive"
	case agentsdk.ActionAgentSessionsRestore:
		idempotency.KeySource = "path.externalSessionID+restore"
	case agentsdk.ActionAgentProposalsApprove:
		idempotency.KeySource = "path.proposalID+approve"
	case agentsdk.ActionAgentProposalsReject:
		idempotency.KeySource = "path.proposalID+reject"
	case agentsdk.ActionAgentTaskToolsInvoke:
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
	if route.Action.Key == agentsdk.ActionAgentRunsStream {
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
		removeAgentBackendDefault(schema, "version")
		properties["allowed_tools"] = stringArrayWithEnum(toolEnum)
	case "agent.agent":
		removeAgentBackendDefault(schema, "version")
		properties["tools"] = stringArrayWithEnum(toolEnum)
	case "agent.task":
		removeAgentBackendDefault(schema, "contract_version")
		removeAgentBackendDefault(schema, "version")
		properties["allowed_outcomes"] = stringArrayWithEnum(append([]string(nil), agentsdk.AgentTaskOutcomes...))
		properties["side_effect_mode"] = map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskSideEffectAnalysisOnly, agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed}}
	case "agent.entrypoint":
		removeAgentBackendDefault(schema, "contract_version")
		removeAgentNestedBackendDefault(properties, "context_contract", "contract_version")
		removeAgentNestedBackendDefault(properties, "context_contract", "max_selected_records")
		removeAgentNestedBackendDefault(properties, "context_contract", "max_context_bytes")
		removeAgentNestedBackendDefault(properties, "routing_contract", "contract_version")
		removeAgentNestedBackendDefault(properties, "routing_contract", "allow_recursive")
	case "agent.service_principal":
		removeAgentBackendDefault(schema, "contract_version")
		removeAgentBackendDefault(schema, "rotation_version")
	}
}

func removeAgentNestedBackendDefault(properties map[string]any, objectKey, fieldKey string) {
	object, _ := properties[objectKey].(map[string]any)
	removeAgentBackendDefault(object, fieldKey)
}

func removeAgentBackendDefault(schema map[string]any, field string) {
	properties, _ := schema["properties"].(map[string]any)
	delete(properties, field)
	required, _ := schema["required"].([]string)
	kept := required[:0]
	for _, key := range required {
		if key != field {
			kept = append(kept, key)
		}
	}
	if len(kept) == 0 {
		delete(schema, "required")
	} else {
		schema["required"] = kept
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
				candidate.Version = agentInitialDefinitionVersion
				err = agentdefinition.ValidateSkillDefinition(candidate)
			}
		case "agent.agent":
			var candidate agentsdk.AgentSchema
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				candidate.Version = agentInitialDefinitionVersion
				err = agentdefinition.ValidateAgentDefinition(candidate)
			}
		case "agent.task":
			var candidate agentsdk.AgentTaskDefinition
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				if candidate.ContractVersion == "" {
					candidate.ContractVersion = agentsdk.AgentTaskContractVersion
				}
				candidate.Version = agentInitialDefinitionVersion
				err = agentdefinition.ValidateTaskDefinition(candidate)
			}
		case "agent.entrypoint":
			var candidate agentsdk.AgentEntrypointAssignment
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				if candidate.ContractVersion == "" {
					candidate.ContractVersion = agentsdk.AgentEntrypointContractVersion
				}
				if candidate.ContextContract.ContractVersion == "" {
					candidate.ContextContract.ContractVersion = agentsdk.GlobalAgentContextContractVersion
				}
				candidate.ContextContract.MaxSelectedRecord = agentDefaultSelectedRecords
				candidate.ContextContract.MaxContextBytes = agentDefaultContextBytes
				if candidate.RoutingContract.ContractVersion == "" {
					candidate.RoutingContract.ContractVersion = agentsdk.AgentRoutingContractVersion
				}
				err = agentdefinition.ValidateEntrypointDefinition(candidate)
			}
		case "agent.service_principal":
			var candidate agentsdk.AgentServicePrincipalBinding
			if err = modulecapability.DecodeKeyedAuthoringValue(request.Candidate, "key", &candidate); err == nil {
				if candidate.ContractVersion == "" {
					candidate.ContractVersion = agentsdk.AgentServicePrincipalContractVersion
				}
				candidate.RotationVersion = 1
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
