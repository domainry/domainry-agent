// Package definition validates Agent-owned execution definitions before they
// are translated to any provider protocol.
package definition

import (
	"fmt"
	"regexp"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
)

var stableDefinitionKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

func ValidateTaskRequest(request agentsdk.TaskRequest) error {
	if strings.TrimSpace(request.TaskRunID) == "" || strings.TrimSpace(request.WorkspaceID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return fmt.Errorf("Agent task execution identity is incomplete")
	}
	return ValidateTaskDefinition(request.Task)
}

func ValidateTaskDefinition(task agentsdk.AgentTaskDefinition) error {
	if task.ContractVersion != agentsdk.AgentTaskContractVersion {
		return fmt.Errorf("unsupported Agent task contract %q", task.ContractVersion)
	}
	if !stableDefinitionKey.MatchString(strings.TrimSpace(task.Key)) || strings.TrimSpace(task.Version) == "" || !stableDefinitionKey.MatchString(strings.TrimSpace(task.AgentKey)) || strings.TrimSpace(task.Instruction) == "" {
		return fmt.Errorf("Agent task definition is incomplete")
	}
	if err := validateAgentObjectSchema(task.InputSchema); err != nil {
		return fmt.Errorf("Agent task input schema: %w", err)
	}
	if err := validateAgentObjectSchema(task.OutputSchema); err != nil {
		return fmt.Errorf("Agent task output schema: %w", err)
	}
	if len(task.AllowedOutcomes) == 0 {
		return fmt.Errorf("Agent task schemas and outcomes are required")
	}
	if err := validateUniqueAgentValues(task.AllowedObjects, "allowed object"); err != nil {
		return err
	}
	if err := validateUniqueAgentValues(task.AllowedActions, "allowed action"); err != nil {
		return err
	}
	if err := validateUniqueAgentValues(task.AllowedOutcomes, "outcome"); err != nil {
		return err
	}
	switch task.SideEffectMode {
	case agentsdk.AgentTaskSideEffectAnalysisOnly:
		if len(task.AllowedActions) != 0 {
			return fmt.Errorf("analysis-only Agent task cannot allow actions")
		}
	case agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed:
		if len(task.AllowedActions) == 0 {
			return fmt.Errorf("Agent task side-effect mode %q requires allowed actions", task.SideEffectMode)
		}
	default:
		return fmt.Errorf("unsupported Agent task side-effect mode %q", task.SideEffectMode)
	}
	allowed := map[string]bool{}
	for _, outcome := range agentsdk.AgentTaskOutcomes {
		allowed[outcome] = true
	}
	for _, outcome := range task.AllowedOutcomes {
		if !allowed[strings.TrimSpace(outcome)] {
			return fmt.Errorf("unsupported Agent task outcome %q", outcome)
		}
	}
	return validateAgentExecutionLimits(task.ExecutionLimits)
}

func ValidateSkillDefinition(skill agentsdk.SkillSchema) error {
	if !stableDefinitionKey.MatchString(strings.TrimSpace(skill.Key)) || strings.TrimSpace(skill.Name) == "" {
		return fmt.Errorf("Agent skill key and name are required")
	}
	if err := validateUniqueAgentValues(skill.AllowedTools, "allowed tool"); err != nil {
		return err
	}
	if err := validateUniqueAgentValues(skill.AllowedObjects, "allowed object"); err != nil {
		return err
	}
	for _, key := range skill.AllowedTools {
		tool, found := agentsdk.LookupAgentTool(key)
		if !found {
			return fmt.Errorf("unsupported Agent tool %q", key)
		}
		if tool.RequiresAllowedObjects && len(skill.AllowedObjects) == 0 {
			return fmt.Errorf("Agent tool %q requires at least one allowed object", key)
		}
	}
	return nil
}

func ValidateAgentDefinition(agent agentsdk.AgentSchema) error {
	if !stableDefinitionKey.MatchString(strings.TrimSpace(agent.Key)) || strings.TrimSpace(agent.Name) == "" {
		return fmt.Errorf("Agent definition key and name are required")
	}
	if err := validateUniqueAgentValues(agent.Tools, "tool"); err != nil {
		return err
	}
	if err := validateUniqueAgentValues(agent.SkillKeys, "skill key"); err != nil {
		return err
	}
	for _, key := range agent.Tools {
		if _, found := agentsdk.LookupAgentTool(key); !found {
			return fmt.Errorf("unsupported Agent tool %q", key)
		}
	}
	return validateAgentExecutionLimits(agent.ExecutionLimits)
}

func ValidateEntrypointDefinition(entrypoint agentsdk.AgentEntrypointAssignment) error {
	if entrypoint.ContractVersion != agentsdk.AgentEntrypointContractVersion {
		return fmt.Errorf("unsupported Agent entrypoint contract %q", entrypoint.ContractVersion)
	}
	if !stableDefinitionKey.MatchString(strings.TrimSpace(entrypoint.Key)) || !stableDefinitionKey.MatchString(strings.TrimSpace(entrypoint.AgentKey)) {
		return fmt.Errorf("Agent entrypoint key and agent key are required")
	}
	for name, values := range map[string][]string{
		"required permission": entrypoint.RequiredPermissions, "route pattern": entrypoint.RoutePatterns,
		"allowed task key": entrypoint.AllowedTaskKeys, "allowed workflow key": entrypoint.AllowedWorkflowKeys,
	} {
		if err := validateUniqueAgentValues(values, name); err != nil {
			return err
		}
	}
	if len(entrypoint.RequiredPermissions) == 0 || len(entrypoint.RoutePatterns) == 0 {
		return fmt.Errorf("Agent entrypoint permissions and route patterns are required")
	}
	contextContract := entrypoint.ContextContract
	if contextContract.ContractVersion != agentsdk.GlobalAgentContextContractVersion || contextContract.MaxSelectedRecord < 1 || contextContract.MaxSelectedRecord > 100 || contextContract.MaxContextBytes < 1024 || contextContract.MaxContextBytes > 1<<20 {
		return fmt.Errorf("Agent entrypoint context contract is invalid")
	}
	if err := validateUniqueAgentValues(contextContract.AllowedHintFields, "context hint"); err != nil {
		return err
	}
	allowedHints := map[string]bool{"route_key": true, "object_key": true, "record_id": true, "selected_record_ids": true, "locale": true, "timezone": true}
	for _, value := range contextContract.AllowedHintFields {
		if !allowedHints[strings.TrimSpace(value)] {
			return fmt.Errorf("unsupported Agent context hint %q", value)
		}
	}
	routing := entrypoint.RoutingContract
	if routing.ContractVersion != agentsdk.AgentRoutingContractVersion || len(routing.AllowedRouteTypes) == 0 || routing.AllowRecursive {
		return fmt.Errorf("Agent entrypoint routing contract is invalid")
	}
	if err := validateUniqueAgentValues(routing.AllowedRouteTypes, "route type"); err != nil {
		return err
	}
	allowedRoutes := map[string]bool{agentsdk.AgentRouteInteractiveQuery: true, agentsdk.AgentRouteTask: true, agentsdk.AgentRouteWorkflow: true, agentsdk.AgentRouteProposal: true}
	for _, value := range routing.AllowedRouteTypes {
		if !allowedRoutes[strings.TrimSpace(value)] {
			return fmt.Errorf("unsupported Agent route type %q", value)
		}
	}
	return nil
}

func ValidateServicePrincipalDefinition(principal agentsdk.AgentServicePrincipalBinding) error {
	if principal.ContractVersion != agentsdk.AgentServicePrincipalContractVersion {
		return fmt.Errorf("unsupported Agent service-principal contract %q", principal.ContractVersion)
	}
	if !stableDefinitionKey.MatchString(strings.TrimSpace(principal.Key)) || !stableDefinitionKey.MatchString(strings.TrimSpace(principal.UserID)) || !stableDefinitionKey.MatchString(strings.TrimSpace(principal.RoleKey)) || principal.RotationVersion < 1 {
		return fmt.Errorf("Agent service-principal identity or rotation version is invalid")
	}
	return nil
}

func validateAgentExecutionLimits(limits agentsdk.AgentExecutionLimits) error {
	if limits.MaxSteps < 0 || limits.TimeoutSeconds < 0 || limits.MaxToolCalls < 0 || limits.MaxInputBytes < 0 || limits.MaxOutputBytes < 0 {
		return fmt.Errorf("Agent execution limits cannot be negative")
	}
	if budget := strings.ToLower(strings.TrimSpace(limits.CostBudget)); budget != "" && budget != "low" && budget != "standard" && budget != "high" {
		return fmt.Errorf("unsupported Agent cost budget %q", limits.CostBudget)
	}
	return nil
}

func validateUniqueAgentValues(values []string, label string) error {
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return fmt.Errorf("Agent %s must not be empty", label)
		}
		if seen[value] {
			return fmt.Errorf("Agent %s %q is duplicated", label, value)
		}
		seen[value] = true
	}
	return nil
}

func validateAgentObjectSchema(schema map[string]any) error {
	if schema == nil || strings.TrimSpace(fmt.Sprint(schema["type"])) != "object" {
		return fmt.Errorf("root type must be object")
	}
	return validateAgentSchemaValue(schema, schema, 0)
}

func validateAgentSchemaValue(value any, root map[string]any, depth int) error {
	if depth > 32 {
		return fmt.Errorf("schema exceeds maximum depth 32")
	}
	switch typed := value.(type) {
	case map[string]any:
		if raw, found := typed["$ref"]; found {
			reference, ok := raw.(string)
			if !ok || strings.TrimSpace(reference) == "" {
				return fmt.Errorf("schema reference must be a non-empty string")
			}
			reference = strings.TrimSpace(reference)
			switch {
			case strings.HasPrefix(reference, "#/$defs/"):
				definitions, _ := root["$defs"].(map[string]any)
				if _, found := definitions[strings.TrimPrefix(reference, "#/$defs/")]; !found {
					return fmt.Errorf("schema references unknown local definition %q", reference)
				}
			case strings.HasPrefix(reference, "domainry://objects/"), strings.HasPrefix(reference, "domainry://actions/"):
			default:
				return fmt.Errorf("unsupported schema reference %q", reference)
			}
		}
		for key, child := range typed {
			if key != "$ref" {
				if err := validateAgentSchemaValue(child, root, depth+1); err != nil {
					return err
				}
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateAgentSchemaValue(child, root, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateInteractiveRequest(request agentsdk.InteractiveRequest) error {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.Message) == "" {
		return fmt.Errorf("interactive Agent request is incomplete")
	}
	if request.Context.ContractVersion != agentsdk.GlobalAgentContextContractVersion || strings.TrimSpace(request.Context.ContextRevision) == "" || strings.TrimSpace(request.Context.EntrypointKey) == "" || strings.TrimSpace(request.Context.AgentKey) == "" {
		return fmt.Errorf("interactive Agent context is invalid")
	}
	if strings.TrimSpace(request.Context.Principal.WorkspaceID) == "" || strings.TrimSpace(request.Context.Principal.UserID) == "" {
		return fmt.Errorf("interactive Agent principal is incomplete")
	}
	for _, candidate := range request.Candidates {
		switch candidate.RouteType {
		case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal:
		default:
			return fmt.Errorf("unsupported Agent route candidate %q", candidate.RouteType)
		}
		if strings.TrimSpace(candidate.TargetKey) == "" {
			return fmt.Errorf("Agent route candidate target is required")
		}
	}
	return nil
}
