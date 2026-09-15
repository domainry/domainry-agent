package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationTaskCompletionTable = "_agent_conversation_task_completions"

func conversationTaskCompletionMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationTaskCompletionTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("task_id", ormschema.TextKey(96)),
		required("revision", ormschema.BigInt()), required("client_id", ormschema.TextKey(96)),
		required("request_hash", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()),
		required("created_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "task_id", "revision").Unique("owner_key", "task_id", "client_id").Build()
	return modulehost.SchemaMigration{Version: 33, Name: "agent_conversation_task_completions", Statements: []string{statement}}, err
}
