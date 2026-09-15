package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationForkTable = "_agent_conversation_forks"

func conversationForkMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := ormschema.NewTable(d, conversationForkTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)),
		required("conversation_id", ormschema.TextKey(96)),
		required("source_conversation_id", ormschema.TextKey(96)),
		required("source_run_id", ormschema.TextKey(96)),
		required("source_event_seq", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "conversation_id").Build()
	return modulehost.SchemaMigration{Version: 34, Name: "agent_conversation_forks", Statements: []string{table}}, err
}
