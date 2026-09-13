package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationAgentTable = "_agent_peer_instances"
const conversationDelegationTable = "_agent_delegations"
const conversationAgentMessageTable = "_agent_peer_messages"
const conversationCollaborationMutationTable = "_agent_collaboration_mutations"

func conversationCollaborationMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 20, Name: "agent_peer_collaboration"}
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(d, conversationAgentTable).IfNotExists().Columns(
			required("owner_key", ormschema.TextKey(64)), required("agent_id", ormschema.TextKey(96)),
			required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "agent_id"),
		ormschema.NewTable(d, conversationDelegationTable).IfNotExists().Columns(
			required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)),
			required("conversation_id", ormschema.TextKey(96)), required("source_conversation_id", ormschema.TextKey(96)),
			required("root_conversation_id", ormschema.TextKey(96)),
			required("status", ormschema.TextKey(32)), required("revision", ormschema.BigInt()),
			required("created_at", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "delegation_id").Unique("owner_key", "conversation_id"),
		ormschema.NewTable(d, conversationAgentMessageTable).IfNotExists().Columns(
			required("runtime_id", ormschema.TextKey(255)), required("authority_json", ormschema.LongText()), required("agent_json", ormschema.LongText()),
			required("owner_key", ormschema.TextKey(64)), required("message_id", ormschema.TextKey(96)),
			required("delegation_id", ormschema.TextKey(96)), required("conversation_id", ormschema.TextKey(96)),
			required("created_at", ormschema.BigInt()), required("consumed_run_id", ormschema.TextKey(96)),
			required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "message_id"),
		ormschema.NewTable(d, conversationCollaborationMutationTable).IfNotExists().Columns(
			required("owner_key", ormschema.TextKey(64)), required("mutation_id", ormschema.TextKey(64)),
			required("request_hash", ormschema.TextKey(64)), required("payload_json", ormschema.LongText()),
		).PrimaryKey("owner_key", "mutation_id"),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	for _, index := range []*ormschema.IndexBuilder{
		ormschema.NewIndex(d, "idx_agent_delegation_source_v20", conversationDelegationTable).Columns("owner_key", "source_conversation_id", "created_at"),
		ormschema.NewIndex(d, "idx_agent_peer_inbox_v20", conversationAgentMessageTable).Columns("owner_key", "conversation_id", "consumed_run_id", "created_at"),
	} {
		statement, _, err := index.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, statement)
	}
	return m, nil
}
