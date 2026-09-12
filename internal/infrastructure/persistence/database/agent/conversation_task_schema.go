package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationTaskTable = "_agent_conversation_tasks"

func conversationTaskMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table := ormschema.NewTable(d, conversationTaskTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)),
		required("task_id", ormschema.TextKey(96)),
		required("runtime_id", ormschema.TextKey(255)),
		required("source_conversation_id", ormschema.TextKey(96)),
		required("source_run_id", ormschema.TextKey(96)),
		required("status", ormschema.TextKey(32)),
		required("authority_json", ormschema.LongText()),
		required("request_hash", ormschema.TextKey(64)),
		required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "task_id").Unique("owner_key", "source_conversation_id", "source_run_id", "request_hash")
	statement, _, err := table.Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_conversation_task_claim_v15", conversationTaskTable).Columns("runtime_id", "status", "created_at", "task_id").Build()
	return modulehost.SchemaMigration{Version: 15, Name: "agent_conversation_tasks", Statements: []string{statement, index}}, err
}
