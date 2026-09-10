package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationTodoMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 5, Name: "agent_personal_todos"}
	statement, _, err := ormschema.NewTable(d, "_agent_user_todos").IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("todo_id", ormschema.TextKey(96)), required("batch_id", ormschema.TextKey(96)), required("position", ormschema.BigInt()), required("created_at", ormschema.BigInt()), required("source_conversation_id", ormschema.TextKey(96)), required("status", ormschema.TextKey(16)), required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "todo_id").Unique("owner_key", "batch_id", "position").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	statement, _, err = ormschema.NewIndex(d, "idx_agent_todos_owner_v5", "_agent_user_todos").Columns("owner_key", "created_at", "batch_id", "position").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	statement, _, err = ormschema.NewTable(d, "_agent_todo_mutations").IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("client_key", ormschema.TextKey(64)), required("created_at", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "client_key").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	return m, nil
}
