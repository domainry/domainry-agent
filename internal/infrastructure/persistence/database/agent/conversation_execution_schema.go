package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationExecutionMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 3, Name: "agent_conversation_execution"}
	statement, _, err := ormschema.NewTable(d, conversationRunStepTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)),
		required("conversation_id", ormschema.TextKey(96)),
		required("run_id", ormschema.TextKey(96)),
		required("record_kind", ormschema.TextKey(32)),
		required("step_no", ormschema.BigInt()),
		required("call_key", ormschema.TextKey(64)),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "conversation_id", "run_id", "record_kind", "step_no", "call_key").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	return m, nil
}
