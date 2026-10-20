package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const attachmentIndexJobTable = "_agent_attachment_index_jobs"
const attachmentKnowledgeSourceTable = "_agent_attachment_sources"

func attachmentIndexSchema(d modulehost.Dialect) ([]string, error) {
	statements := make([]string, 0, 3)
	for _, b := range []*ormschema.TableBuilder{
		ormschema.NewTable(d, attachmentIndexJobTable).IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("attachment_id", ormschema.TextKey(96)), required("runtime_id", ormschema.TextKey(255)), required("not_before", ormschema.BigInt()), required("lease_until", ormschema.BigInt()), required("fence", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "attachment_id"),
		ormschema.NewTable(d, attachmentKnowledgeSourceTable).IfNotExists().Columns(required("scope_key", ormschema.TextKey(64)), required("source_key", ormschema.TextKey(64))).PrimaryKey("scope_key").Unique("source_key"),
	} {
		q, _, err := b.Build()
		if err != nil {
			return nil, err
		}
		statements = append(statements, q)
	}
	q, _, err := ormschema.NewIndex(d, "idx_agent_attachment_index_jobs_due", attachmentIndexJobTable).Columns("runtime_id", "not_before", "lease_until").Build()
	if err != nil {
		return nil, err
	}
	return append(statements, q), nil
}

func conversationAttachmentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statements, err := attachmentIndexSchema(d)
	return modulehost.SchemaMigration{Version: 7, Name: "agent_conversation_attachments", Statements: statements}, err
}
