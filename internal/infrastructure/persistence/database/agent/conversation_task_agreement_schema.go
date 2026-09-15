package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationTaskAgreementUpdateTable = "_agent_conversation_task_agreement_updates"

func conversationTaskAgreementUpdateMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationTaskAgreementUpdateTable).IfNotExists().Columns(
		required("owner_key", ormschema.TextKey(64)), required("task_id", ormschema.TextKey(96)),
		required("client_id", ormschema.TextKey(96)), required("request_hash", ormschema.TextKey(64)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
	).PrimaryKey("owner_key", "task_id", "client_id").Build()
	return modulehost.SchemaMigration{Version: 31, Name: "agent_conversation_task_agreements", Statements: []string{statement}}, err
}
