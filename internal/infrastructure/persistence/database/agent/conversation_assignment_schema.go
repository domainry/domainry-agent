package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationAssignmentTable = "_agent_delegation_assignments"

func conversationAssignmentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationAssignmentTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)), required("number", ormschema.BigInt()),
		required("conversation_id", ormschema.TextKey(96)), required("task_id", ormschema.TextKey(96)), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "delegation_id", "number").Unique("owner_key", "conversation_id").Build()
	return modulehost.SchemaMigration{Version: 22, Name: "agent_delegation_assignments", Statements: []string{statement}}, err
}
