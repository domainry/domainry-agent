package composition

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"

	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// applyManifestMetadata replaces the RuntimeServices-owned schema indexes. It is
// mutable composition state, not a Metadata application use case.
func (s *runtimeAssembly) applyManifestMetadata(templateID, templateVersion, name, timeZone string, objects []definitionmodel.ObjectSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []appschemamodel.DictionarySchema, integrations connectormodel.IntegrationSchema, reports []reportmodel.ReportSchema, skills []agentsdk.SkillSchema, agents []agentsdk.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	objects = appschemaprojection.ApplicationSchemaEnrichObjectsWithFieldValueDomains(objects, dictionaries)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templateID = strings.TrimSpace(templateID)
	s.templateVersion = strings.TrimSpace(templateVersion)
	s.name = strings.TrimSpace(name)
	s.timeZone = timeZone
	s.schema = make(map[string]definitionmodel.ObjectSchema, len(objects))
	s.actions = make(map[string]definitionmodel.ActionSchema, len(actions))
	s.workflows = make(map[string]definitionmodel.WorkflowSchema, len(workflows))
	s.automationRules = make(map[string]automationmodel.AutomationRuleSchema, len(automationRules))
	s.dictionaries = append([]appschemamodel.DictionarySchema(nil), dictionaries...)
	s.integrations = appschemaservice.CloneIntegrationSchema(integrations)
	if s.connectorRegistry == nil {
		s.connectorRegistry = newRuntimeConnectorCatalog(integrations)
	} else {
		s.connectorRegistry.ReplaceSchema(integrations)
	}
	s.reports = append([]reportmodel.ReportSchema(nil), reports...)
	s.skills = append([]agentsdk.SkillSchema(nil), skills...)
	s.agents = append([]agentsdk.AgentSchema(nil), agents...)
	s.identityProfileExtensions = append([]profilebindingmodel.Binding(nil), profileBindings...)
	for _, object := range objects {
		if strings.TrimSpace(object.Key) != "" {
			s.schema[object.Key] = object
		}
	}
	for _, action := range actions {
		if strings.TrimSpace(action.Key) != "" {
			s.actions[action.Key] = action
		}
	}
	for _, workflow := range workflows {
		if strings.TrimSpace(workflow.Key) != "" {
			s.workflows[workflow.Key] = workflow
		}
	}
	for _, rule := range automationRules {
		if strings.TrimSpace(rule.Key) != "" {
			s.automationRules[rule.Key] = rule
		}
	}
}

func (s *runtimeAssembly) applyManifestAgentMetadata(tasks []agentsdk.AgentTaskDefinition, entrypoints []agentsdk.AgentEntrypointAssignment, principals []agentsdk.AgentServicePrincipalBinding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentTasks = append([]agentsdk.AgentTaskDefinition(nil), tasks...)
	s.agentEntrypoints = append([]agentsdk.AgentEntrypointAssignment(nil), entrypoints...)
	s.agentServicePrincipals = append([]agentsdk.AgentServicePrincipalBinding(nil), principals...)
}
