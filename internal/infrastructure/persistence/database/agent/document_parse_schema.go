package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const documentParseTable = "_agent_document_parses"

func documentParseMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	m := modulehost.SchemaMigration{Version: 12, Name: "agent_document_parses"}
	q, _, e := ormschema.NewTable(d, documentParseTable).IfNotExists().Columns(required("parse_id", ormschema.TextKey(96)), required("resource_key", ormschema.TextKey(64)), required("runtime_id", ormschema.TextKey(255)), required("parser_key", ormschema.TextKey(64)), required("state", ormschema.TextKey(24)), required("revision", ormschema.BigInt()), required("lease_until", ormschema.BigInt()), required("not_before", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("parse_id").Build()
	if e != nil {
		return m, e
	}
	m.Statements = append(m.Statements, q)
	for _, idx := range []*ormschema.IndexBuilder{
		ormschema.NewIndex(d, "idx_agent_document_parses_resource_v12", documentParseTable).Columns("resource_key", "state"),
		ormschema.NewIndex(d, "idx_agent_document_parses_due_v12", documentParseTable).Columns("runtime_id", "state", "not_before", "lease_until"),
	} {
		q, _, e = idx.Build()
		if e != nil {
			return m, e
		}
		m.Statements = append(m.Statements, q)
	}
	return m, nil
}

// No runtime reader or writer remains for this retired table. Retain v12 for
// already-installed migration histories; v13 removes its derived metadata.
func retireDocumentParsingMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	q, _, err := query.NewDeleteBuilder(d, documentParseTable).Where(query.IsNotNull("parse_id")).Build()
	return modulehost.SchemaMigration{Version: 13, Name: "agent_retire_document_parsing", Statements: []string{q}}, err
}
