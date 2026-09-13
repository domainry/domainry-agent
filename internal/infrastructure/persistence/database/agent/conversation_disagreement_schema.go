package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const conversationStepSourceTable = "_agent_conversation_step_sources"
const conversationDisagreementTable = "_agent_delegation_disagreements"

func conversationDisagreementMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	statement, _, err := ormschema.NewTable(d, conversationDisagreementTable).IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("delegation_id", ormschema.TextKey(96)), required("disagreement_id", ormschema.TextKey(96)), required("revision", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "delegation_id", "disagreement_id", "revision").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	sources, _, err := ormschema.NewTable(d, conversationStepSourceTable).IfNotExists().Columns(required("owner_key", ormschema.TextKey(64)), required("conversation_id", ormschema.TextKey(96)), required("run_id", ormschema.TextKey(96)), required("step_no", ormschema.BigInt()), required("payload_json", ormschema.LongText())).PrimaryKey("owner_key", "conversation_id", "run_id", "step_no").Build()
	return modulehost.SchemaMigration{Version: 24, Name: "agent_delegation_disagreements", Statements: []string{statement, sources}}, err
}
