package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationCapacityOwner    = sharedworkerscope.OwnerAgentConversationCapacity
	conversationCapacityRecovery = "transactional_guard"
)

// The guard is an Agent-owned row in the shared worker-scope table. It remains
// separate from Runtime rate limiting and serializes one workspace's admission
// without creating an Agent-specific physical table.
func conversationCapacityMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	runIndex, _, err := ormschema.NewIndex(d, "idx_agent_run_conversation_capacity_v19", agentRunTable).Columns("run_kind", "runtime_id", "workspace_id", "status").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	taskIndex, _, err := ormschema.NewIndex(d, "idx_agent_conversation_capacity_task_v19", conversationTaskTable).Columns("record_kind", "runtime_id", "workspace_key", "status").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 19, Name: "agent_conversation_capacity", Statements: []string{runIndex, taskIndex}}, nil
}
