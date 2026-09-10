package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const knowledgeDocumentTable = "_agent_knowledge_documents"
const knowledgeDocumentJobTable = "_agent_knowledge_document_jobs"
const knowledgeDocumentSourceTable = "_agent_knowledge_document_sources"

func knowledgeDocumentMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 10, Name: "agent_knowledge_documents"}
	for _, builder := range []*ormschema.TableBuilder{
		ormschema.NewTable(d, knowledgeDocumentSourceTable).IfNotExists().Columns(required("scope_key", ormschema.TextKey(64)), required("library_id", ormschema.TextKey(96)), required("source_key", ormschema.TextKey(64))).PrimaryKey("scope_key", "library_id").Unique("source_key"),
		ormschema.NewTable(d, knowledgeDocumentTable).IfNotExists().Columns(required("scope_key", ormschema.TextKey(64)), required("document_id", ormschema.TextKey(96)), required("library_id", ormschema.TextKey(96)), required("source_key", ormschema.TextKey(64)), required("remote_id", ormschema.TextKey(96)), required("state", ormschema.TextKey(24)), required("revision", ormschema.BigInt()), required("bytes", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("scope_key", "document_id").Unique("source_key", "remote_id"),
		ormschema.NewTable(d, knowledgeDocumentJobTable).IfNotExists().Columns(required("scope_key", ormschema.TextKey(64)), required("document_id", ormschema.TextKey(96)), required("runtime_id", ormschema.TextKey(255)), required("not_before", ormschema.BigInt()), required("lease_until", ormschema.BigInt()), required("fence", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("scope_key", "document_id"),
	} {
		q, _, e := builder.Build()
		if e != nil {
			return m, e
		}
		m.Statements = append(m.Statements, q)
	}
	for _, index := range []*ormschema.IndexBuilder{
		ormschema.NewIndex(d, "idx_agent_knowledge_documents_library_v10", knowledgeDocumentTable).Columns("scope_key", "library_id", "document_id"),
		ormschema.NewIndex(d, "idx_agent_knowledge_document_jobs_due_v10", knowledgeDocumentJobTable).Columns("runtime_id", "not_before", "lease_until"),
	} {
		q, _, e := index.Build()
		if e != nil {
			return m, e
		}
		m.Statements = append(m.Statements, q)
	}
	return m, nil
}
