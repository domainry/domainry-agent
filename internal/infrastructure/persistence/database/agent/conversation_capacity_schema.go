package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationCapacityGuardTable = "_worker_scopes"
	conversationCapacityOwner      = "agent_conversation_capacity"
	conversationCapacityRecovery   = "transactional_guard"
)

// The guard is an Agent-owned row in the shared worker-scope table. It remains
// separate from Runtime rate limiting and serializes one workspace's admission
// without creating an Agent-specific physical table.
func conversationCapacityMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
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
	return modulehost.SchemaMigration{Version: 19, Name: "agent_conversation_capacity", Statements: []string{runWorkspace, taskWorkspace, runIndex, taskIndex}}, nil
}
