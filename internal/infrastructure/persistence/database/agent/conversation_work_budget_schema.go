package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationWorkBudgetTable = "_agent_conversation_work_budgets"

func conversationWorkBudgetMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTableWithRequiredColumns(d, conversationWorkBudgetTable, physicalID()).IfNotExists().Columns(
		required("runtime_id", ormschema.TextKey(255)),
		required("workspace_id", ormschema.TextKey(255)),
		required("root_conversation_id", ormschema.TextKey(96)),
		required("updated_at", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("id").Unique("runtime_id", "workspace_id", "root_conversation_id").Build()
	return modulehost.SchemaMigration{Version: 30, Name: "agent_conversation_work_budget", Statements: []string{statement}}, err
}
