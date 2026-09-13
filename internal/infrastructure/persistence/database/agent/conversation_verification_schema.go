package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationDeliveryRecordTable = "_agent_delegation_deliveries"

func conversationDeliveryRecordMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationDeliveryRecordTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)), required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText()),
	).PrimaryKey("owner_key", "delegation_id", "revision").Build()
	return modulehost.SchemaMigration{Version: 23, Name: "agent_delegation_deliveries", Statements: []string{statement}}, err
}
