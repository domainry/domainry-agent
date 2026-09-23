package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationItemTable                  = "_agent_conversation_items"
	conversationItemMessage                = "message"
	conversationItemModelInput             = "model_input"
	conversationItemSummary                = "summary"
	conversationItemRunEvent               = "run_event"
	conversationItemAgreement              = "task_agreement"
	conversationItemCompletion             = "task_completion"
	conversationItemTaskPlan               = "task_plan"
	conversationItemDelegationAgreement    = "delegation_agreement"
	conversationItemDelegationAssignment   = "delegation_assignment"
	conversationItemDelegationDelivery     = "delegation_delivery"
	conversationItemDelegationDisagreement = "delegation_disagreement"
)

// Conversation roots and mutable runs stay separate. Immutable messages,
// model inputs, summaries, run events, task agreements and task completions
// share one typed item journal.
func conversationMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	scope := func() ormschema.ColumnDefinition { return required("owner_key", ormschema.TextKey(64)) }
	id := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.TextKey(96)) }
	text := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.LongText()) }
	number := func(n string) ormschema.ColumnDefinition { return required(n, ormschema.BigInt()) }
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(d, "_agent_conversations").IfNotExists().Columns(scope(), id("conversation_id"), id("client_id"), text("request_hash"), text("title"), number("updated_at"), number("archived"), number("revision"), text("payload_json")).PrimaryKey("owner_key", "conversation_id").Unique("owner_key", "client_id"),
		ormschema.NewTable(d, conversationItemTable).IfNotExists().Columns(scope(), id("conversation_id"), required("item_kind", ormschema.TextKey(32)), required("item_key", ormschema.TextKey(191)), required("reference_id", ormschema.TextKey(191)), ormschema.Column("subject_id", ormschema.TextKey(191)), id("run_id"), number("seq"), text("payload_json")).PrimaryKey("owner_key", "conversation_id", "item_kind", "item_key").Unique("owner_key", "item_kind", "reference_id"),
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
	sql, _, err := ormschema.NewIndex(d, "idx_agent_run_conversation_claim_v2", agentRunTable).Columns("run_kind", "runtime_id", "status", "lease_expires_at", "created_at").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	sql, _, err = ormschema.NewIndex(d, "idx_agent_run_conversation_scope_v2", agentRunTable).Columns("run_kind", "owner_key", "conversation_id", "run_id").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	sql, _, err = ormschema.NewIndex(d, "idx_agent_conversation_item_subject_v2", conversationItemTable).Columns("owner_key", "item_kind", "subject_id", "seq").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	sql, _, err = ormschema.NewIndex(d, "idx_agent_conversation_item_sequence_v2", conversationItemTable).Columns("owner_key", "conversation_id", "item_kind", "seq", "item_key").Build()
	if err != nil {
		return m, err
	}
	m.Statements = append(m.Statements, sql)
	sql, _, err = ormschema.NewIndex(d, "idx_agent_conversation_item_run_v2", conversationItemTable).Columns("owner_key", "conversation_id", "item_kind", "run_id", "seq").Build()
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
