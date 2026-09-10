package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const attachmentTable = "_agent_conversation_attachments"
const attachmentCleanupTable = "_agent_attachment_cleanup"

func conversationAttachmentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 7, Name: "agent_conversation_attachments"}
	table, _, err := ormschema.NewTable(d, attachmentTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("attachment_id", ormschema.TextKey(96)), required("conversation_id", ormschema.TextKey(96)), required("state", ormschema.TextKey(24)), required("revision", ormschema.BigInt()), required("bytes", ormschema.BigInt()), required("updated_at", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "attachment_id").Build()
	if err != nil {
		return m, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_attachments_conversation_v7", attachmentTable).Columns("owner_key", "conversation_id", "attachment_id").Build()
	if err != nil {
		return m, err
	}
	cleanup, _, err := ormschema.NewIndex(d, "idx_agent_attachments_state_v7", attachmentTable).Columns("state", "updated_at").Build()
	if err != nil {
		return m, err
	}
	m.Statements = []string{table, index, cleanup}
	return m, nil
}

func conversationAttachmentCleanupMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 8, Name: "agent_attachment_cleanup"}
	table, _, err := ormschema.NewTable(d, attachmentCleanupTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("attachment_id", ormschema.TextKey(96)), required("runtime_id", ormschema.TextKey(255)), required("not_before", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "attachment_id").Build()
	if err != nil {
		return m, err
	}
	index, _, err := ormschema.NewIndex(d, "idx_agent_attachment_cleanup_due_v8", attachmentCleanupTable).Columns("runtime_id", "not_before").Build()
	if err != nil {
		return m, err
	}
	m.Statements = []string{table, index}
	return m, nil
}
