package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	orm "github.com/domainry/domainry-orm/schema"
)

const conversationParticipantTable = "_agent_delegation_participants"

func conversationParticipantsMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := orm.NewTable(d, conversationParticipantTable).IfNotExists().Columns(
		required("owner_key", orm.TextKey(64)), required("delegation_id", orm.TextKey(96)), required("viewer_key", orm.TextKey(64)), required("owner_user_id", orm.TextKey(255)), required("revision", orm.BigInt()), required("created_at", orm.BigInt()),
	).PrimaryKey("owner_key", "delegation_id", "viewer_key").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := orm.NewIndex(d, "idx_agent_participant_v26", conversationParticipantTable).Columns("viewer_key", "created_at", "delegation_id").Build()
	return modulehost.SchemaMigration{Version: 26, Name: "delegation_participant_grants", Statements: []string{table, index}}, err
}
