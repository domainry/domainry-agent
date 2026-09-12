package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationCapacityGuardTable = "_agent_conversation_capacity_guards"

// The guard is deliberately separate from Runtime rate limiting. It belongs to
// Agent's durable execution queue and serializes admission for one workspace
// across processes without making Agent depend on a host implementation.
func conversationCapacityMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := ormschema.NewTable(d, conversationCapacityGuardTable).IfNotExists().Columns(
		required("runtime_id", ormschema.TextKey(255)),
		required("workspace_key", ormschema.TextKey(64)),
		required("revision", ormschema.BigInt()),
	).PrimaryKey("runtime_id", "workspace_key").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	runWorkspace, _, err := ormschema.NewAddColumn(d, "_agent_conversation_runs", ormschema.Column("workspace_key", ormschema.TextKey(64))).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	taskWorkspace, _, err := ormschema.NewAddColumn(d, conversationTaskTable, ormschema.Column("workspace_key", ormschema.TextKey(64))).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	runIndex, _, err := ormschema.NewIndex(d, "idx_agent_conversation_capacity_run_v19", "_agent_conversation_runs").Columns("runtime_id", "workspace_key", "status").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	taskIndex, _, err := ormschema.NewIndex(d, "idx_agent_conversation_capacity_task_v19", conversationTaskTable).Columns("runtime_id", "workspace_key", "status").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 19, Name: "agent_conversation_capacity", Statements: []string{table, runWorkspace, taskWorkspace, runIndex, taskIndex}}, nil
}
