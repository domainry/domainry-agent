package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationMemoryChangeTable = "_agent_memory_changes"

func conversationMemoryChangeMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	changes, _, err := ormschema.NewTableWithRequiredColumns(d, conversationMemoryChangeTable, physicalID()).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("memory_id", ormschema.TextKey(96)),
		required("revision", ormschema.BigInt()), required("operation", ormschema.TextKey(16)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
	).PrimaryKey("id").Unique("owner_key", "memory_id", "revision").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 36, Name: "agent_scoped_memories", Statements: []string{changes}}, nil
}
