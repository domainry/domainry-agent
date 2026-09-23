package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	conversationTaskTable           = "_agent_tasks"
	conversationTaskKindTask        = "task"
	conversationTaskKindFollowState = "follow_up_state"
	conversationTaskKindFollowEvent = "follow_up_event"
)

func conversationTaskKindPredicate(kind string) query.Predicate {
	return query.Equal("record_kind", kind)
}

func conversationTaskRecordPredicate(kind, owner, id string) query.Predicate {
	return query.And(conversationTaskKindPredicate(kind), query.Equal("owner_key", owner), query.Equal("task_id", id))
}

func conversationTaskPredicate(owner, taskID string) query.Predicate {
	return conversationTaskRecordPredicate(conversationTaskKindTask, owner, taskID)
}

func conversationTaskMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table := ormschema.NewTable(d, conversationTaskTable).IfNotExists().Columns(
		required("record_kind", ormschema.TextKey(32)),
		required("owner_key", ormschema.TextKey(64)),
		// task_id is the kind-local identity: task ID, follow-up plan ID or
		// follow-up event ID. record_kind makes those namespaces independent.
		required("task_id", ormschema.TextKey(96)),
		required("runtime_id", ormschema.TextKey(255)),
		optional("workspace_key", ormschema.TextKey(64)),
		optional("source_conversation_id", ormschema.TextKey(96)),
		optional("source_run_id", ormschema.TextKey(96)),
		required("status", ormschema.TextKey(32)),
		optional("authority_json", ormschema.LongText()),
		optional("request_hash", ormschema.TextKey(64)),
		optional("scheduled_plan_id", ormschema.TextKey(191)),
		optional("scheduler_run_id", ormschema.TextKey(191)),
		optional("scheduled_for", ormschema.BigInt()),
		optional("observation_hash", ormschema.TextKey(64)),
		required("occurrence", ormschema.BigInt()).DefaultValue(0),
		optional("last_task_id", ormschema.TextKey(96)),
		required("lease_owner", ormschema.TextKey(96)).DefaultValue(""),
		required("fencing_token", ormschema.BigInt()).DefaultValue(0),
		required("lease_expires_at", ormschema.BigInt()).DefaultValue(0),
		required("attempt", ormschema.BigInt()).DefaultValue(0),
		required("next_attempt_at", ormschema.BigInt()).DefaultValue(0),
		required("created_at", ormschema.BigInt()),
		required("updated_at", ormschema.BigInt()),
		required("payload_json", ormschema.LongText()),
	).PrimaryKey("record_kind", "owner_key", "task_id").Unique("record_kind", "owner_key", "source_conversation_id", "source_run_id", "request_hash")
	statement, _, err := table.Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	taskIndex, _, err := ormschema.NewIndex(d, "idx_agent_task_claim_v15", conversationTaskTable).Columns("record_kind", "runtime_id", "status", "created_at", "task_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	followUpIndex, _, err := ormschema.NewIndex(d, "idx_agent_task_follow_up_claim_v15", conversationTaskTable).
		Columns("record_kind", "runtime_id", "status", "next_attempt_at", "lease_expires_at", "created_at", "task_id").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	return modulehost.SchemaMigration{Version: 15, Name: "agent_tasks", Statements: []string{statement, taskIndex, followUpIndex}}, nil
}
