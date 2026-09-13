package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationAgreementTable = "_agent_delegation_agreements"

func conversationAgreementMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationAgreementTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)),
		required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "delegation_id", "revision").Build()
	return modulehost.SchemaMigration{Version: 21, Name: "agent_delegation_agreements", Statements: []string{statement}}, err
}
