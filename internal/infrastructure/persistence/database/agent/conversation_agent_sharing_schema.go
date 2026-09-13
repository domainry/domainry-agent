package agent

import (
	"github.com/domainry/domainry-agent-sdk/modulehost"
	orm "github.com/domainry/domainry-orm/schema"
)

const conversationAgentGrantTable = "_agent_peer_use_grants"

func conversationAgentSharingMigration(d modulehost.Dialect) (modulehost.SchemaMigration, error) {
	table, _, err := orm.NewTable(d, conversationAgentGrantTable).IfNotExists().Columns(
		required("owner_key", orm.TextKey(64)), required("agent_id", orm.TextKey(96)), required("viewer_key", orm.TextKey(64)), required("owner_user_id", orm.TextKey(255)),
	).PrimaryKey("owner_key", "agent_id", "viewer_key").Build()
	if err != nil {
		return modulehost.SchemaMigration{}, err
	}
	index, _, err := orm.NewIndex(d, "idx_agent_peer_use_grant_v25", conversationAgentGrantTable).Columns("viewer_key", "agent_id").Build()
	return modulehost.SchemaMigration{Version: 25, Name: "agent_explicit_use_grants", Statements: []string{table, index}}, err
}
