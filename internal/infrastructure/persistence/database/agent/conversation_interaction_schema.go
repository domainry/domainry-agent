package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationInteractionMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 4, Name: "agent_conversation_interactions"}
	statement, _, err := ormschema.NewTable(d, "_agent_conversation_interactions").IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("conversation_id", ormschema.TextKey(96)), required("run_id", ormschema.TextKey(96)), required("step_no", ormschema.BigInt()), required("call_key", ormschema.TextKey(64)), required("kind", ormschema.TextKey(32)), required("interaction_id", ormschema.TextKey(96)), required("runtime_id", ormschema.TextKey(255)), required("status", ormschema.TextKey(32)), required("expires_at", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no", "call_key", "kind").Unique("owner_key", "interaction_id").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	statement, _, err = ormschema.NewIndex(d, "idx_agent_interaction_expiry_v4", "_agent_conversation_interactions").Columns("runtime_id", "status", "expires_at").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, statement)
	return m, nil
}
