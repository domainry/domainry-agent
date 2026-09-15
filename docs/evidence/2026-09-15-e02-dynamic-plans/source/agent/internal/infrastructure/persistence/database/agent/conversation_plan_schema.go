package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationTaskPlanTable = "_agent_conversation_task_plans"

func conversationTaskPlanMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationTaskPlanTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("task_id", ormschema.TextKey(96)),
		required("version", ormschema.BigInt()), required("client_id", ormschema.TextKey(96)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "task_id", "version").Unique("owner_key", "task_id", "client_id").Build()
	return modulehost.SchemaMigration{Version: 32, Name: "agent_conversation_task_plans", Statements: []string{statement}}, err
}
