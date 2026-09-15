package composition

import (
	"context"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// RecordSchemaSnapshotProvider projects mutable Runtime schema state.
type RecordSchemaSnapshotProvider struct {
	snapshot func() appschemamodel.ApplicationSchemaSnapshot
}

func ensureRecordSchemaSnapshotProvider(records *runtimeAssembly) {
	if records != nil && records.RecordSchemaSnapshotProvider == nil {
		records.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() appschemamodel.ApplicationSchemaSnapshot { return recordSchemaSnapshot(records) }}
	}
}

func (s *RecordSchemaSnapshotProvider) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot()
}

func recordSchemaSnapshot(s *runtimeAssembly) appschemamodel.ApplicationSchemaSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make([]definitionmodel.ObjectSchema, 0, len(s.schema))
	for _, object := range s.schema {
		objects = append(objects, object)
	}
	actions := make([]definitionmodel.ActionSchema, 0, len(s.actions))
	for _, action := range s.actions {
		actions = append(actions, action)
	}
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(s.workflows))
	for _, workflow := range s.workflows {
		workflows = append(workflows, workflow)
	}
	automationRules := make([]automationmodel.AutomationRuleSchema, 0, len(s.automationRules))
	for _, rule := range s.automationRules {
		automationRules = append(automationRules, rule)
	}
	return appschemaservice.BuildSchemaSnapshot(appschemaservice.SchemaSnapshotState{
		TemplateID: s.templateID, TemplateVersion: s.templateVersion, Name: s.name, TimeZone: s.timeZone,
		Objects: objects, Actions: actions, Workflows: workflows, AutomationRules: automationRules,
		Dictionaries: s.dictionaries, Integrations: s.integrations, Reports: s.reports,
		Skills: s.skills, Agents: s.agents, AgentTasks: s.agentTasks, AgentEntrypoints: s.agentEntrypoints, AgentServicePrincipals: s.agentServicePrincipals,
		IdentityProfileExtensions: s.identityProfileExtensions,
	})
}

func recordSchemaObjects(s *runtimeAssembly) []definitionmodel.ObjectSchema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objects := make([]definitionmodel.ObjectSchema, 0, len(s.schema))
	for _, object := range s.schema {
		objects = append(objects, object)
	}
	return appschemaservice.ProjectSchemaObjects(objects)
}

func (s *RecordSchemaSnapshotProvider) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return appschemaservice.SnapshotForPrincipal(s.Schema(), principal)
}
