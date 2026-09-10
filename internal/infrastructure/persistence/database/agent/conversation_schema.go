package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

// Version one is immutable. Conversation has separate tables and its own runs.
func conversationMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	scope := func() ormschema.ColumnDefinition { return required("owner_key", ormschema.TextKey(64)) }
	id := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.TextKey(96)) }
	text := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.LongText()) }
	number := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.BigInt()) }
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(d, "_agent_conversations").IfNotExists().Columns(scope(), id("conversation_id"), id("client_id"), text("request_hash"), text("title"), number("updated_at"), number("archived"), number("revision"), text("payload_json")).PrimaryKey("owner_key", "conversation_id").Unique("owner_key", "client_id"),
		ormschema.NewTable(d, "_agent_conversation_inputs").IfNotExists().Columns(scope(), id("conversation_id"), id("run_id"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "run_id"),
		ormschema.NewTable(d, "_agent_conversation_messages").IfNotExists().Columns(scope(), id("conversation_id"), id("message_id"), id("run_id"), number("seq"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "seq").Unique("owner_key", "message_id"),
		ormschema.NewTable(d, "_agent_conversation_runs").IfNotExists().Columns(scope(), id("conversation_id"), id("run_id"), id("client_message_id"), required("runtime_id", ormschema.TextKey(255)), text("authority_json"), text("request_hash"), required("status", ormschema.TextKey(32)), id("lease_owner"), number("fence"), number("lease_expires_at"), number("event_seq"), number("created_at"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "run_id").Unique("owner_key", "conversation_id", "client_message_id"),
		ormschema.NewTable(d, "_agent_conversation_summaries").IfNotExists().Columns(scope(), id("conversation_id"), id("summary_id"), number("through_seq"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "summary_id"),
		ormschema.NewTable(d, "_agent_conversation_events").IfNotExists().Columns(scope(), id("conversation_id"), id("run_id"), number("seq"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "run_id", "seq"),
		ormschema.NewTable(d, "_agent_user_memories").IfNotExists().Columns(scope(), id("memory_id"), number("revision"), text("payload_json")).PrimaryKey("owner_key", "memory_id"),
	}
	m := modulehost.SchemaMigration{Version: 2, Name: "agent_conversations"}
	for _, table := range tables {
		sql, _, err := table.Build()
		if err != nil {
			return m, err
		}
		m.Statements = append(m.Statements, sql)
	}
	sql, _, err := ormschema.NewIndex(d, "idx_agent_conversation_claim_v2", "_agent_conversation_runs").Columns("runtime_id", "status", "lease_expires_at", "created_at").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	sql, _, err = ormschema.NewIndex(d, "idx_agent_conversation_list_v2", "_agent_conversations").Columns("owner_key", "archived", "updated_at", "conversation_id").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	return m, nil
}
