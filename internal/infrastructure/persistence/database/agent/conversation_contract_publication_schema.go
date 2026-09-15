package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationContractPublicationTable = "_agent_contract_publications"

func conversationContractPublicationMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationContractPublicationTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)), required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "delegation_id", "revision").Build()
	return modulehost.SchemaMigration{Version: 29, Name: "agent_contract_publications", Statements: []string{statement}}, err
}
