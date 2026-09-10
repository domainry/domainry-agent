package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationExecutionMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 3, Name: "agent_conversation_execution"}
	for _, name := range []string{"_agent_conversation_steps", "_agent_conversation_tool_calls"} {
		columns := []ormschema.ColumnDefinition{required("owner_key", ormschema.TextKey(64)), required("conversation_id", ormschema.TextKey(96)), required("run_id", ormschema.TextKey(96)), required("step_no", ormschema.BigInt()), required("payload_json", ormschema.LongText())}
		keys := []string{"owner_key", "conversation_id", "run_id", "step_no"}
		if name == "_agent_conversation_tool_calls" {
			columns = append(columns, required("call_key", ormschema.TextKey(64)))
			keys = append(keys, "call_key")
		}
		statement, _, err := ormschema.NewTable(d, name).IfNotExists().Columns(columns...).PrimaryKey(keys...).Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	return m, nil
}
