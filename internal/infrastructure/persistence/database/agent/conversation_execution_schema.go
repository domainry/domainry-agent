package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func conversationExecutionMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 3, Name: "agent_conversation_execution"}
	scope := func() []ormschema.ColumnDefinition {
		return []ormschema.ColumnDefinition{
			required("owner_key", ormschema.TextKey(64)),
			required("conversation_id", ormschema.TextKey(96)),
			required("run_id", ormschema.TextKey(96)),
			required("step_no", ormschema.BigInt()),
		}
	}
	tables := []*ormschema.TableBuilder{
		// Hot result/policy metadata never contains the frozen transcript.
		ormschema.NewTable(d, conversationRunStepTable).IfNotExists().Columns(append(scope(), required("payload_json", ormschema.LongText()))...).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no"),
		// Immutable exact request (without the separately referenced tools and sources).
		ormschema.NewTable(d, conversationStepInputTable).IfNotExists().Columns(append(scope(),
			required("tools_hash", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()), required("source_context_json", ormschema.LongText()),
		)...).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no"),
		// Conversation scope also makes erasure and catalog privacy explicit.
		ormschema.NewTable(d, conversationToolCatalogTable).IfNotExists().Columns(
			required("owner_key", ormschema.TextKey(64)), required("conversation_id", ormschema.TextKey(96)),
			required("catalog_hash", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "conversation_id", "catalog_hash"),
		ormschema.NewTable(d, conversationToolCallTable).IfNotExists().Columns(append(scope(),
			required("call_key", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()),
		)...).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no", "call_key"),
		// One row per source edge; the ordinal preserves the exact frozen order.
		ormschema.NewTable(d, conversationStepSourceTable).IfNotExists().Columns(append(scope(),
			required("source_no", ormschema.BigInt()), required("source_conversation_id", ormschema.TextKey(96)),
			required("source_run_id", ormschema.TextKey(96)), required("before_step", ormschema.BigInt()),
		)...).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no", "source_no"),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	return m, nil
}
