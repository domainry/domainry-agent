package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationTaskTable = "_agent_conversation_tasks"

func conversationTaskMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table := ormschema.NewTable(d, conversationTaskTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)),
		required("task_id", ormschema.TextKey(96)),
		required("runtime_id", ormschema.TextKey(255)),
		required("source_conversation_id", ormschema.TextKey(96)),
		required("source_run_id", ormschema.TextKey(96)),
		required("status", ormschema.TextKey(32)),
		required("authority_json", ormschema.LongText()),
		required("request_hash", ormschema.TextKey(64)),
		required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "task_id").Unique("owner_key", "source_conversation_id", "source_run_id", "request_hash")
	statement, _, err := table.Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_conversation_task_claim_v15", conversationTaskTable).Columns("runtime_id", "status", "created_at", "task_id").Build()
	return modulehost.SchemaMigration{Version: 15, Name: "agent_conversation_tasks", Statements: []string{statement, index}}, err
}

func conversationTaskScheduleMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	plan, _, err := ormschema.NewAddColumn(d, conversationTaskTable, ormschema.Column("scheduled_plan_id", ormschema.TextKey(191))).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	run, _, err := ormschema.NewAddColumn(d, conversationTaskTable, ormschema.Column("scheduler_run_id", ormschema.TextKey(191))).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	scheduledFor, _, err := ormschema.NewAddColumn(d, conversationTaskTable, ormschema.Column("scheduled_for", ormschema.BigInt())).Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 16, Name: "agent_scheduled_conversation_tasks", Statements: []string{plan, run, scheduledFor}}, nil
}

func conversationFollowUpMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	state, _, err := ormschema.NewTable(d, conversationFollowUpStateTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("plan_id", ormschema.TextKey(191)),
		required("runtime_id", ormschema.TextKey(255)), required("status", ormschema.TextKey(32)),
		required("observation_hash", ormschema.TextKey(64)), required("occurrence", ormschema.BigInt()),
		required("last_task_id", ormschema.TextKey(96)), required("created_at", ormschema.BigInt()), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "plan_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	events, _, err := ormschema.NewTable(d, conversationFollowUpEventTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("event_id", ormschema.TextKey(96)),
		required("runtime_id", ormschema.TextKey(255)), required("status", ormschema.TextKey(32)),
		required("lease_owner", ormschema.TextKey(96)), required("fence", ormschema.BigInt()),
		required("lease_expires_at", ormschema.BigInt()), required("attempt", ormschema.BigInt()),
		required("next_attempt_at", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
		required("created_at", ormschema.BigInt()), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "event_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_follow_up_event_claim_v17", conversationFollowUpEventTable).
		Columns("runtime_id", "status", "next_attempt_at", "lease_expires_at", "created_at").Build()
	return modulehost.SchemaMigration{Version: 17, Name: "agent_conversation_follow_ups", Statements: []string{state, events, index}}, err
}
